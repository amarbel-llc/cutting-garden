# Capture Progress Prototype (Reporter + WET viewport) Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use eng:subagent-driven-development to implement this plan task-by-task.

**Goal:** Build the in-process `Reporter` contract and a WET TTY viewport (spinner + rolling log tail + progress bar) so the capture/restore/diff progress UX can be eyeballed and its rendering choices decided by experiment.

**Architecture:** Layer 1 is a `Reporter` interface (`Plan`/`Progress`/`Log`) in `internal/cutting_garden_plugins`, threaded into the four request structs and defaulting to a no-op. Layer 2 is a `ProgramReporter` adapter that translates `Reporter` calls into viewport messages. Layer 3 is a cg-local `internal/capture_viewport` package — the raw bubbletea `Model` tier of purse-first FDR 0010's `operation_viewport` (NOT the `Run`/`RunBatch`/PTY half, which cutting-garden's in-process plugins don't need). A throwaway `cmd/capture-viewport-demo` drives the viewport with synthetic events so a human can watch it on a real TTY.

**Tech Stack:** Go 1.26; `charmbracelet/bubbletea` v1 + `bubbles/spinner` + `bubbles/progress` + `lipgloss` (all already in the dependency closure as indirect deps via `dewey`/`lipgloss`); `nix develop` devshell; `just` recipes.

**Scope:** Steps 1–2 of the design only (`docs/plans/2026-06-05-capture-progress-protocol-design.md` §Sequencing). This is a **prototype / UX spike**. Steps 3–5 (wiring fs/git/ytdlp to emit; `-progress`/`-color` flags + confirm-gate + TAP color; JSON-RPC notifications) are explicitly OUT OF SCOPE — they are gated on this spike's UX findings and on the v2 transport (#51) landing.

**Rollback:** Purely additive. `internal/capture_viewport/` and `internal/cutting_garden_plugins/reporter.go` are new; the request-struct fields default to a nil→no-op `Reporter` and have no callers yet (emission is step 3); `cmd/capture-viewport-demo` is a spike artifact. To revert: delete the two new packages/files, the demo dir + recipe, and the four one-line struct fields. Nothing else depends on them.

---

## Notes before you start

- **Run everything in the devshell.** Single-test form: `nix develop --command go test ./internal/<pkg>/ -run TestName -v`. Whole suite is `just test`.
- **The byte-identity conformance test is deferred to step 3**, not this plan. It asserts a plugin produces identical receipts with and without a `Reporter` — meaningless until a plugin actually *emits* (step 3). Here we only prove the no-op is safe. Do not write a vacuous byte-identity test.
- **bubbletea/bubbles are already in `go.sum`** (indirect via `dewey`). Importing them compiles immediately; Task 3 includes a one-time `just update-go` to flip them to direct `require` lines and refresh `gomod2nix.toml`, then `git add` so `nix build` sees the change.
- **Reference precedents:** `internal/diff/render.go` (lipgloss/termenv profile pattern), `clown:cmd/clown/tent_loader.go` and `amarbel-llc/tap docs/features/0001-tty-viewport.md` (the WET viewport sources), `amarbel-llc/purse-first docs/features/0010-operation-viewport.md` (the message vocabulary this mirrors).

---

### Task 1: `Reporter` interface, value structs, and no-op default

**Promotion criteria:** N/A (new code, no old approach).

**Files:**
- Create: `internal/cutting_garden_plugins/reporter.go`
- Test: `internal/cutting_garden_plugins/reporter_test.go`

**Step 1: Write the failing test**

Create `internal/cutting_garden_plugins/reporter_test.go`:

```go
package cutting_garden_plugins

import "testing"

// recordingReporter is a pointer-identity-comparable Reporter for tests.
type recordingReporter struct{ plans int }

func (r *recordingReporter) Plan(ReportPlan)         { r.plans++ }
func (r *recordingReporter) Progress(ReportProgress) {}
func (r *recordingReporter) Log(string, ...any)      {}

func TestNopReporter_MethodsAreSafeNoOps(t *testing.T) {
	var r Reporter = NopReporter{}
	r.Plan(ReportPlan{Items: 10, Bytes: 100, Label: "x"})
	r.Progress(ReportProgress{Item: "f", Items: 1, Bytes: 10})
	r.Log("hello %s", "world")
	// Reaching here without panic is the assertion.
}

func TestReporterOrNop_NilYieldsUsableNoOp(t *testing.T) {
	r := ReporterOrNop(nil)
	if r == nil {
		t.Fatal("ReporterOrNop(nil) returned nil; want a usable no-op Reporter")
	}
	r.Plan(ReportPlan{})
	r.Progress(ReportProgress{})
	r.Log("ok")
}

func TestReporterOrNop_NonNilPassesThrough(t *testing.T) {
	rec := &recordingReporter{}
	if got := ReporterOrNop(rec); got != rec {
		t.Fatal("ReporterOrNop should return the same non-nil Reporter")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `nix develop --command go test ./internal/cutting_garden_plugins/ -run 'TestNopReporter|TestReporterOrNop' -v`
Expected: FAIL — `undefined: Reporter`, `undefined: NopReporter`, `undefined: ReporterOrNop`, etc. (compile failure).

**Step 3: Write minimal implementation**

Create `internal/cutting_garden_plugins/reporter.go`:

```go
package cutting_garden_plugins

// Reporter carries non-identity observability (plan / progress / log) from
// a capture, restore, or diff plugin to the orchestrator. It is the
// in-process analogue of RFC 0006's JSON-RPC notifications (see
// docs/plans/2026-06-05-capture-progress-protocol-design.md).
//
// These events are SEMANTICS, NOT IDENTITY: an implementation MUST NOT let
// them influence blob bytes, receipt shape, or any returned result. A
// Reporter is opt-in (the #50 SourceValidator capability ethos): a nil
// Reporter is valid and means "no observability"; plugins MAY omit any or
// all calls. Use ReporterOrNop at the call site to drop the nil check.
type Reporter interface {
	// Plan reports an up-front estimate of the work about to be done.
	// Called at most once, before any Progress. Optional — a plugin that
	// cannot estimate (streaming sources) never calls it, and the consumer
	// falls back to an indeterminate display.
	Plan(ReportPlan)

	// Progress reports incremental advancement, called many times as work
	// proceeds. ReportProgress.Items SHOULD be monotonic non-decreasing.
	Progress(ReportProgress)

	// Log emits a freeform human-readable line for the consumer's tail.
	// Signature mirrors fmt.Printf; pass "%s" when holding a pre-formatted
	// string.
	Log(format string, args ...any)
}

// ReportPlan is the up-front work estimate. A zero field means "unknown":
// Items == 0 yields an indeterminate display rather than a bar.
type ReportPlan struct {
	Items int64  // estimated total operations (e.g. filesystem entries)
	Bytes int64  // estimated total bytes
	Label string // human-readable scope, e.g. "walking ./src"
}

// ReportProgress is one incremental advancement sample.
type ReportProgress struct {
	Item  string // current item label, e.g. "src/main.go"
	Items int64  // operations completed so far (the bar numerator)
	Bytes int64  // bytes processed so far
}

// NopReporter is a Reporter whose methods do nothing — the default when no
// consumer is attached, so plugin code can report unconditionally.
type NopReporter struct{}

func (NopReporter) Plan(ReportPlan)         {}
func (NopReporter) Progress(ReportProgress) {}
func (NopReporter) Log(string, ...any)      {}

// ReporterOrNop returns r, or a NopReporter when r is nil.
func ReporterOrNop(r Reporter) Reporter {
	if r == nil {
		return NopReporter{}
	}
	return r
}
```

**Step 4: Run test to verify it passes**

Run: `nix develop --command go test ./internal/cutting_garden_plugins/ -run 'TestNopReporter|TestReporterOrNop' -v`
Expected: PASS (3 tests).

**Step 5: Commit**

```bash
git add internal/cutting_garden_plugins/reporter.go internal/cutting_garden_plugins/reporter_test.go
git commit -m "feat(plugins): add in-process Reporter contract (plan/progress/log)"
```

---

### Task 2: Thread `Reporter` into the four request structs

**Promotion criteria:** N/A.

**Files:**
- Modify: `internal/cutting_garden_plugins/plugin.go` (`CaptureRootRequest` ~L41, `RestoreRequest` ~L77, `DiffScanRequest` ~L105)
- Modify: `internal/cutting_garden_plugins/protocol.go` (`ProtocolCaptureRequest` ~L15)
- Test: `internal/cutting_garden_plugins/request_reporter_test.go`

> Scope note: `ProtocolDiffRequest` is left untouched here — adding its `Reporter` is trivially symmetric and belongs with diff wiring (step 3). Keep this prototype to the four structs the design named.

**Step 1: Write the failing test**

Create `internal/cutting_garden_plugins/request_reporter_test.go`:

```go
package cutting_garden_plugins

import "testing"

// Each request struct must carry an opt-in Reporter the orchestrator can
// populate. This test fixes the field name/type across all four and proves
// ReporterOrNop works off the field.
func TestRequests_CarryReporterField(t *testing.T) {
	rec := &recordingReporter{}

	cr := CaptureRootRequest{Reporter: rec}
	pr := ProtocolCaptureRequest{Reporter: rec}
	rr := RestoreRequest{Reporter: rec}
	dr := DiffScanRequest{Reporter: rec}

	for _, r := range []Reporter{cr.Reporter, pr.Reporter, rr.Reporter, dr.Reporter} {
		ReporterOrNop(r).Plan(ReportPlan{})
	}
	if rec.plans != 4 {
		t.Errorf("expected 4 Plan calls through the request fields, got %d", rec.plans)
	}

	// A zero-value request has a nil Reporter that ReporterOrNop tolerates.
	ReporterOrNop(CaptureRootRequest{}.Reporter).Log("no panic")
}
```

**Step 2: Run test to verify it fails**

Run: `nix develop --command go test ./internal/cutting_garden_plugins/ -run TestRequests_CarryReporterField -v`
Expected: FAIL — `unknown field 'Reporter' in struct literal` for each of the four.

**Step 3: Add the field to each struct**

In `internal/cutting_garden_plugins/plugin.go`, add a `Reporter` field to `CaptureRootRequest` (after `Sink capture_sink.Sink`):

```go
	Sink      capture_sink.Sink

	// Reporter receives non-identity plan/progress/log events. Optional —
	// nil is a valid no-op (use ReporterOrNop). See reporter.go.
	Reporter Reporter
```

Add to `RestoreRequest` (after `RawDest string`):

```go
	RawDest   string

	// Reporter receives non-identity plan/progress/log events. Optional.
	Reporter Reporter
```

Add to `DiffScanRequest` (after `ReceiptEntries []capture_receipt.EntryV1`):

```go
	ReceiptEntries []capture_receipt.EntryV1

	// Reporter receives non-identity plan/progress/log events. Optional.
	Reporter Reporter
```

In `internal/cutting_garden_plugins/protocol.go`, add to `ProtocolCaptureRequest` (after the `BlobStore` / `StoreName` block — place it just before `PriorReceiptDigest`):

```go
	// Reporter receives non-identity plan/progress/log events. Optional.
	Reporter Reporter
```

**Step 4: Run test to verify it passes**

Run: `nix develop --command go test ./internal/cutting_garden_plugins/ -run TestRequests_CarryReporterField -v`
Expected: PASS.

Then confirm the package still builds: `nix develop --command go build ./internal/cutting_garden_plugins/`
Expected: no output (success).

**Step 5: Commit**

```bash
git add internal/cutting_garden_plugins/plugin.go internal/cutting_garden_plugins/protocol.go internal/cutting_garden_plugins/request_reporter_test.go
git commit -m "feat(plugins): thread optional Reporter through capture/restore/diff requests"
```

---

### Task 3: Viewport package — message types + `Model` (tail + done/collapse)

**Promotion criteria:** N/A.

**Files:**
- Create: `internal/capture_viewport/messages.go`
- Create: `internal/capture_viewport/model.go`
- Test: `internal/capture_viewport/model_test.go`
- Modify (dependency hygiene): `go.mod`, `go.sum`, `gomod2nix.toml`

**Step 1: Write the failing test**

Create `internal/capture_viewport/model_test.go`:

```go
package capture_viewport

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func updateAll(m tea.Model, msgs ...tea.Msg) tea.Model {
	for _, msg := range msgs {
		m, _ = m.Update(msg)
	}
	return m
}

func TestModel_TailKeepsLastNLines(t *testing.T) {
	got := updateAll(New(WithTailLines(3)),
		LogLine{Text: "a"}, LogLine{Text: "b"},
		LogLine{Text: "c"}, LogLine{Text: "d"},
	)
	view := got.View()
	if strings.Contains(view, "a") {
		t.Errorf("oldest line should have rolled off; view:\n%s", view)
	}
	for _, want := range []string{"b", "c", "d"} {
		if !strings.Contains(view, want) {
			t.Errorf("tail missing %q; view:\n%s", want, view)
		}
	}
}

func TestModel_CollapsesTailOnSuccessfulDone(t *testing.T) {
	got := updateAll(New(WithTitle("cap")),
		LogLine{Text: "noisy"},
		BatchDone{Err: nil},
	)
	view := got.View()
	if strings.Contains(view, "noisy") {
		t.Errorf("successful BatchDone should collapse the tail; view:\n%s", view)
	}
	if !strings.Contains(view, "cap") {
		t.Errorf("done view should show the title; view:\n%s", view)
	}
}

func TestModel_HoldsAndShowsErrorOnFailure(t *testing.T) {
	got := updateAll(New(WithTitle("cap")),
		BatchDone{Err: errors.New("boom")},
	)
	if view := got.View(); !strings.Contains(view, "boom") {
		t.Errorf("failed done should surface the error; view:\n%s", view)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `nix develop --command go test ./internal/capture_viewport/ -run TestModel -v`
Expected: FAIL — package/symbols don't exist (`New`, `WithTailLines`, `LogLine`, `BatchDone`, …).

**Step 3: Write the message types**

Create `internal/capture_viewport/messages.go`:

```go
// Package capture_viewport is cutting-garden's WET copy of the raw Model
// tier of purse-first FDR 0010's operation_viewport: a bubbletea model
// that renders a spinner + rolling log tail + (when a total is known) a
// progress bar. The Run/RunBatch + PTY helpers from FDR 0010 are
// intentionally omitted — cutting-garden's plugins are in-process and emit
// structured events, so there is no child PTY to scan. Upstreaming this to
// dewey/pkgs/operation_viewport is a tracked follow-up.
package capture_viewport

// Message types mirror FDR 0010's vocabulary so this copy can be absorbed
// upstream. They are delivered to the Model via tea.Program.Send.

// LogLine appends one line to the rolling tail.
type LogLine struct{ Text string }

// OperationStarted (re)labels the header and, when Total > 0, arms the bar.
type OperationStarted struct {
	Name  string // label, e.g. "capture ./src"
	Index int    // 1-based position in a batch; 0 when not batched
	Total int    // total operations; 0 = unknown (indeterminate)
}

// OperationProgress advances the bar numerator.
type OperationProgress struct {
	Current int // numerator
	Total   int // denominator; 0 leaves the existing total unchanged
}

// OperationDone ends one operation: success collapses its tail, failure
// holds it and records the error.
type OperationDone struct{ Err error }

// BatchDone ends the whole run and quits the program.
type BatchDone struct{ Err error }
```

**Step 4: Write the Model**

Create `internal/capture_viewport/model.go`:

```go
package capture_viewport

import (
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const defaultTailLines = 5

// Model renders a spinner + rolling log tail and, when a total is known, a
// progress bar. Driven entirely by the messages in messages.go.
type Model struct {
	title    string
	tailMax  int
	tail     []string
	spinner  spinner.Model
	progress progress.Model

	current int // bar numerator
	total   int // bar denominator; 0 = indeterminate
	done    bool
	err     error
}

// Option configures a Model.
type Option func(*Model)

// WithTailLines sets the rolling-tail height (default 5). TUNING LEVER.
func WithTailLines(n int) Option { return func(m *Model) { m.tailMax = n } }

// WithTitle sets the header label.
func WithTitle(s string) Option { return func(m *Model) { m.title = s } }

// New builds a Model ready for tea.NewProgram.
func New(opts ...Option) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	m := Model{
		tailMax:  defaultTailLines,
		spinner:  sp,
		progress: progress.New(progress.WithDefaultGradient()),
	}
	for _, o := range opts {
		o(&m)
	}
	return m
}

func (m Model) Init() tea.Cmd { return m.spinner.Tick }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case LogLine:
		m.tail = append(m.tail, msg.Text)
		if len(m.tail) > m.tailMax {
			m.tail = m.tail[len(m.tail)-m.tailMax:]
		}
		return m, nil
	case OperationStarted:
		if msg.Name != "" {
			m.title = msg.Name
		}
		if msg.Total > 0 {
			m.total = msg.Total
		}
		if msg.Index > 0 {
			m.current = msg.Index - 1
		}
		return m, nil
	case OperationProgress:
		m.current = msg.Current
		if msg.Total > 0 {
			m.total = msg.Total
		}
		return m, nil
	case OperationDone:
		if msg.Err != nil {
			m.err = msg.Err
		} else {
			m.tail = nil // collapse on success
		}
		return m, nil
	case BatchDone:
		m.done = true
		m.err = msg.Err
		return m, tea.Quit
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	default:
		return m, nil
	}
}

var (
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	failStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	tailStyle    = lipgloss.NewStyle().Faint(true)
)

func (m Model) View() string {
	var b strings.Builder

	switch {
	case m.done && m.err == nil:
		b.WriteString(successStyle.Render("✓ " + m.title))
		b.WriteByte('\n')
		return b.String()
	case m.done && m.err != nil:
		b.WriteString(failStyle.Render("✗ " + m.title + ": " + m.err.Error()))
		b.WriteByte('\n')
		return b.String()
	}

	b.WriteString(m.spinner.View())
	b.WriteByte(' ')
	b.WriteString(m.title)
	if m.total > 0 {
		ratio := float64(m.current) / float64(m.total)
		b.WriteString("  ")
		b.WriteString(m.progress.ViewAs(ratio))
	}
	b.WriteByte('\n')

	for _, line := range m.tail {
		b.WriteString(tailStyle.Render("│ " + line))
		b.WriteByte('\n')
	}
	return b.String()
}
```

**Step 5: Run test to verify it passes**

Run: `nix develop --command go test ./internal/capture_viewport/ -run TestModel -v`
Expected: PASS (3 tests). bubbletea/bubbles/lipgloss resolve from the existing `go.sum` (indirect deps) — no fetch needed.

**Step 6: Dependency hygiene (flip indirect→direct, refresh nix lock)**

Run: `just update-go`
This runs `go mod tidy` (moving `bubbletea`/`bubbles` to direct `require` lines now that we import them) then `gomod2nix` (refresh `gomod2nix.toml`). The `.toml` may be unchanged (the versions were already locked transitively); that is fine.

Run: `nix develop --command go build ./...`
Expected: success.

**Step 7: Commit**

```bash
git add internal/capture_viewport/messages.go internal/capture_viewport/model.go internal/capture_viewport/model_test.go go.mod go.sum gomod2nix.toml
git commit -m "feat(capture_viewport): WET viewport Model — spinner + rolling tail + collapse"
```

---

### Task 4: Wire the progress bar (determinate vs indeterminate)

**Promotion criteria:** N/A.

**Files:**
- Test: `internal/capture_viewport/model_test.go` (add a test)

> The bar is already implemented in Task 3's `View`/`Update`; this task adds the test that pins the determinate-vs-indeterminate contract (BAR BINDING is a tuning lever — see the design).

**Step 1: Write the failing test**

Append to `internal/capture_viewport/model_test.go`:

```go
func TestModel_BarOnlyWhenTotalKnown(t *testing.T) {
	indeterminate := New(WithTitle("cap")).View()
	determinate := updateAll(New(WithTitle("cap")),
		OperationStarted{Name: "cap", Total: 10},
		OperationProgress{Current: 5, Total: 10},
	).View()

	if determinate == indeterminate {
		t.Fatalf("determinate view should differ from indeterminate:\n%s", determinate)
	}
	if !strings.Contains(determinate, "%") {
		t.Errorf("determinate view should render a percentage bar:\n%s", determinate)
	}
	if strings.Contains(indeterminate, "%") {
		t.Errorf("indeterminate view should not render a bar:\n%s", indeterminate)
	}
}
```

> If `bubbles/progress` defaults ever drop the inline percentage, replace the `"%"` assertions with `len(determinate) > len(indeterminate)`.

**Step 2: Run test to verify it fails or passes**

Run: `nix develop --command go test ./internal/capture_viewport/ -run TestModel_BarOnlyWhenTotalKnown -v`
Expected: PASS (the bar logic already exists). If it FAILS on the `"%"` assertion, apply the length-based fallback above and re-run.

**Step 3: Commit**

```bash
git add internal/capture_viewport/model_test.go
git commit -m "test(capture_viewport): pin determinate-vs-indeterminate bar contract"
```

---

### Task 5: The adapter — `ProgramReporter` (Layer 2)

**Promotion criteria:** N/A.

**Files:**
- Create: `internal/capture_viewport/adapter.go`
- Test: `internal/capture_viewport/adapter_test.go`

**Step 1: Write the failing test**

Create `internal/capture_viewport/adapter_test.go`:

```go
package capture_viewport

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	cgp "github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
)

// fakeSender captures messages without a running tea.Program.
type fakeSender struct{ msgs []tea.Msg }

func (f *fakeSender) Send(m tea.Msg) { f.msgs = append(f.msgs, m) }

func TestProgramReporter_PlanBecomesOperationStarted(t *testing.T) {
	fs := &fakeSender{}
	NewReporter(fs).Plan(cgp.ReportPlan{Items: 42, Label: "walking ./src"})

	if len(fs.msgs) != 1 {
		t.Fatalf("want 1 msg, got %d", len(fs.msgs))
	}
	got, ok := fs.msgs[0].(OperationStarted)
	if !ok {
		t.Fatalf("want OperationStarted, got %T", fs.msgs[0])
	}
	if got.Total != 42 || got.Name != "walking ./src" {
		t.Errorf("unexpected OperationStarted: %+v", got)
	}
}

func TestProgramReporter_LogBecomesLogLine(t *testing.T) {
	fs := &fakeSender{}
	NewReporter(fs).Log("hello %s", "world")

	if len(fs.msgs) != 1 {
		t.Fatalf("want 1 msg, got %d", len(fs.msgs))
	}
	if got := fs.msgs[0].(LogLine); got.Text != "hello world" {
		t.Errorf("want %q, got %q", "hello world", got.Text)
	}
}

func TestProgramReporter_ProgressEmitsItemThenAdvance(t *testing.T) {
	fs := &fakeSender{}
	NewReporter(fs).Progress(cgp.ReportProgress{Item: "src/main.go", Items: 7})

	if len(fs.msgs) != 2 {
		t.Fatalf("want 2 msgs, got %d: %+v", len(fs.msgs), fs.msgs)
	}
	if _, ok := fs.msgs[0].(LogLine); !ok {
		t.Errorf("first msg want LogLine, got %T", fs.msgs[0])
	}
	op, ok := fs.msgs[1].(OperationProgress)
	if !ok || op.Current != 7 {
		t.Errorf("second msg want OperationProgress{Current:7}, got %+v", fs.msgs[1])
	}
}

// Compile-time proof the adapter satisfies the plugin Reporter contract.
var _ cgp.Reporter = ProgramReporter{}
```

**Step 2: Run test to verify it fails**

Run: `nix develop --command go test ./internal/capture_viewport/ -run TestProgramReporter -v`
Expected: FAIL — `undefined: NewReporter`, `undefined: ProgramReporter`.

**Step 3: Write the adapter**

Create `internal/capture_viewport/adapter.go`:

```go
package capture_viewport

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	cgp "github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
)

// sender is the subset of *tea.Program the adapter uses, narrowed so tests
// can inject a fake without a running program. *tea.Program satisfies it.
type sender interface {
	Send(tea.Msg)
}

// ProgramReporter implements cutting_garden_plugins.Reporter by translating
// each event into a viewport message and sending it to a bubbletea program.
// This is Layer 2 of the design — the adapter between the plugin event
// stream and the Model.
type ProgramReporter struct {
	p sender
}

var _ cgp.Reporter = ProgramReporter{}

// NewReporter wraps a *tea.Program (or any sender) as a Reporter.
func NewReporter(p sender) ProgramReporter { return ProgramReporter{p: p} }

func (r ProgramReporter) Plan(pl cgp.ReportPlan) {
	r.p.Send(OperationStarted{Name: pl.Label, Total: int(pl.Items)})
}

func (r ProgramReporter) Progress(pr cgp.ReportProgress) {
	if pr.Item != "" {
		r.p.Send(LogLine{Text: pr.Item})
	}
	r.p.Send(OperationProgress{Current: int(pr.Items)})
}

func (r ProgramReporter) Log(format string, args ...any) {
	r.p.Send(LogLine{Text: fmt.Sprintf(format, args...)})
}
```

**Step 4: Run test to verify it passes**

Run: `nix develop --command go test ./internal/capture_viewport/ -v`
Expected: PASS (all viewport tests).

**Step 5: Commit**

```bash
git add internal/capture_viewport/adapter.go internal/capture_viewport/adapter_test.go
git commit -m "feat(capture_viewport): ProgramReporter adapter (Reporter -> viewport messages)"
```

---

### Task 6: Demo harness + `debug-viewport-demo` recipe (the UX spike)

**Promotion criteria:** This is a throwaway spike artifact. Remove (or fold into the real capture wiring) once the Section-3 rendering levers are decided in Task 7.

**Files:**
- Create: `cmd/capture-viewport-demo/main.go`
- Modify: `justfile` (add a `[group('debug')]` recipe)

**Step 1: Write the demo program**

Create `cmd/capture-viewport-demo/main.go`:

```go
// Command capture-viewport-demo drives the WET capture viewport with
// synthetic plan/progress/log events so the prototype UX can be eyeballed
// on a real TTY. Throwaway spike artifact — see
// docs/plans/2026-06-05-capture-progress-prototype.md (#28). Remove or
// promote once the rendering levers are decided.
package main

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/amarbel-llc/cutting-garden/internal/capture_viewport"
	cgp "github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
)

func main() {
	m := capture_viewport.New(capture_viewport.WithTitle("capture .tmp/cap-fixture"))
	p := tea.NewProgram(m)

	go func() {
		r := capture_viewport.NewReporter(p)
		const total = 30
		r.Plan(cgp.ReportPlan{Items: total, Label: "capture .tmp/cap-fixture"})
		for i := 1; i <= total; i++ {
			time.Sleep(120 * time.Millisecond)
			r.Progress(cgp.ReportProgress{
				Item:  fmt.Sprintf("file-%02d.txt", i),
				Items: int64(i),
			})
			if i%10 == 0 {
				r.Log("store group %d flushed", i/10)
			}
		}
		time.Sleep(250 * time.Millisecond)
		p.Send(capture_viewport.BatchDone{Err: nil})
	}()

	if _, err := p.Run(); err != nil {
		fmt.Println("demo error:", err)
	}
}
```

**Step 2: Verify it builds**

Run: `nix develop --command go build ./cmd/capture-viewport-demo`
Expected: success (no output).

**Step 3: Add the explore recipe**

Append to `justfile`:

```just
# Drive the WET capture viewport with synthetic plan/progress/log events on
# a real TTY, so the prototype's UX (collapse-on-done, tail height, bar
# binding) can be eyeballed. Prototype/UX-spike artifact — see
# docs/plans/2026-06-05-capture-progress-prototype.md. (#28)
[group('debug')]
debug-viewport-demo:
    nix develop --command go run ./cmd/capture-viewport-demo
```

**Step 4: Run the demo and watch it**

Run (in an interactive terminal, NOT piped): `just debug-viewport-demo`
Expected: a live spinner + title + filling progress bar + a 5-line rolling tail of `file-NN.txt`, collapsing to a green `✓ capture .tmp/cap-fixture` line on completion.

> If running headless, note that this needs a real TTY; the maintainer runs it via `! just debug-viewport-demo` in the session, or in their own terminal.

**Step 5: Commit**

```bash
git add cmd/capture-viewport-demo/main.go justfile
git commit -m "feat(debug): capture-viewport-demo + debug-viewport-demo recipe (UX spike)"
```

---

### Task 7: Capture UX-spike findings + decide the levers

**Promotion criteria:** N/A (documentation / decision-capture).

**Files:**
- Modify: `docs/plans/2026-06-05-capture-progress-protocol-design.md` (Tuning levers table)

**Step 1: Run the demo and evaluate the design's tuning levers**

With `just debug-viewport-demo` running, evaluate each prototype-driven lever from the design's §Tuning levers:
- **collapse vs coexist** — does collapsing the tail on done read well?
- **tail height** — is 5 lines right? Try `WithTailLines(3)` / `WithTailLines(8)` in the demo.
- **bar binding** — does a single whole-capture bar read clearly? (Multi-root is not exercised by the demo — note that as a gap.)
- **repaint/coalesce rate** — any flicker at the 120 ms cadence?

**Step 2: Record the decisions**

For each lever, update the design doc's §Tuning levers "Current" column with what the spike settled (or leave it and note "needs multi-root demo"). Add a short "Prototype findings (2026-…)" note under that table.

**Step 3: Commit**

```bash
git add docs/plans/2026-06-05-capture-progress-protocol-design.md
git commit -m "docs(plan): record capture viewport UX-spike findings"
```

---

## After the prototype

Steps 3–5 from the design remain, gated as noted:
1. Wire fs/git/ytdlp to emit `Reporter` calls + the byte-identity conformance test (step 3).
2. `-progress`/`-color` flags + confirm-gate + TAP color (step 4).
3. JSON-RPC `capture.plan`/`capture.progress`/`capture.log` notifications once the v2 transport (#51) lands and chrest opts in (step 5).

Each is its own writing-plans pass, informed by Task 7's findings.
