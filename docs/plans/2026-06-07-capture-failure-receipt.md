# Capture Failure Receipts Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use eng:subagent-driven-development to implement this plan task-by-task.

**Goal:** Whenever a capture has failed entries or is aborted, write a durable, content-addressed failure receipt (`cutting_garden-capture_failures-v1`) listing every failure's root/path/op/error, journal it in captures.log, and add a `cg failures` reader.

**Architecture:** A new `internal/capture_failures` package owns the wire format (hyphence-typed blob, metadata `- key value` lines + NDJSON body), mirroring `internal/capture_receipt`'s coder pattern. Failure detail flows through **plugin return values** (`CaptureRootResult.Failures`), NOT the event stream — `capture_events` doc-comment forbids events influencing receipts ("SEMANTICS, NOT IDENTITY"). The capture orchestrator aggregates per store group and writes one failure receipt per group after the success receipt, with a local-NDJSON spill fallback when the store write fails.

**Tech Stack:** Go, madder `hyphence` (`CoderToTypedBlob`), markl ids, dewey errors/flags, existing bats harness.

**Rollback:** N/A — purely additive (new type tag, new optional log fields, new subcommand). Stop-writing is the rollback; the design doc records this (docs/plans/2026-06-07-capture-failure-receipt-design.md).

**Design doc (normative for format decisions):** `docs/plans/2026-06-07-capture-failure-receipt-design.md`

**Conventions to honor** (read before starting):
- CLAUDE.md (repo root) — build/test commands, exit semantics, opt-in command interfaces.
- The merge gate runs dewey analyzers (`just lint-go-analyzers`): seqerror, repool, **defererr**. `defer x.Close()` discarding an error fails the gate — use `errors.DeferredCloser(&err, x)` with a named return, `files.CloseReadOnly` (read-only `*os.File`), or a `//defer:err-checked`-marked helper (see `internal/serve/localsend_test.go` `closeBody`).
- Run `just lint-go-analyzers` before declaring any task done.

---

### Task 1: `internal/capture_failures` wire format package

**Files:**
- Create: `internal/capture_failures/failures.go`
- Create: `internal/capture_failures/coder.go`
- Create: `internal/capture_failures/io.go`
- Test: `internal/capture_failures/io_test.go`

Model everything on `internal/capture_receipt/{main.go,coder.go,v1_io.go,store_write.go}` — same hyphence pattern, simpler metadata.

**Step 1: Write the failing round-trip test**

`internal/capture_failures/io_test.go`:

```go
package capture_failures

import (
	"bytes"
	"strings"
	"testing"
)

func sample() *V1 {
	return &V1{
		Meta: Meta{
			Ts:       "2026-06-07T12:00:00Z",
			Outcome:  OutcomeAborted,
			Signal:   "interrupt",
			Receipt:  "sha256-abc",
			Roots:    []string{"./", "other/"},
			Captured: 6018,
			Failed:   2,
		},
		Failures: []FailureV1{
			{Root: "./", Path: "a/b.ts", Op: OpBlobWrite, Error: "read: permission denied"},
			{Root: "./", Path: "c.txt", Op: OpStat, Error: "stale handle"},
		},
	}
}

func TestWriteV1ReadV1_RoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if _, err := WriteV1(&buf, sample()); err != nil {
		t.Fatalf("WriteV1: %v", err)
	}
	wire := buf.String()
	if !strings.Contains(wire, "! "+TypeTagV1) {
		t.Fatalf("missing type tag in wire: %q", wire)
	}

	got, err := ReadV1(strings.NewReader(wire))
	if err != nil {
		t.Fatalf("ReadV1: %v", err)
	}
	want := sample()
	if got.Meta != wantMetaComparable(want.Meta) ... // compare Meta fields and Failures slice elementwise; Roots with slices.Equal
}

func TestReadV1_RejectsUnknownTypeTag(t *testing.T) {
	// mirror capture_receipt/v1_io_read_test.go: feed a blob whose `!` line
	// names a different tag; expect an error mentioning the tag.
}

func TestWriteV1_OmitsEmptySignalAndReceipt(t *testing.T) {
	// Outcome failures, Signal "", Receipt "" — wire must not contain
	// "- signal" or "- receipt" lines; round-trip preserves zero values.
}
```

(Write real comparisons, not the `...` sketch — `reflect.DeepEqual` on the whole `*V1` is fine here.)

**Step 2: Run to verify failure**

Run: `go test ./internal/capture_failures/` → FAIL (package does not exist).

**Step 3: Implement the types**

`internal/capture_failures/failures.go`:

```go
// Package capture_failures owns the cutting_garden-capture_failures-v1
// wire format: the durable record of WHICH entries failed (and why)
// during a capture, written alongside the success receipt whenever
// failCount > 0 or the run was aborted. Design:
// docs/plans/2026-06-07-capture-failure-receipt-design.md.
package capture_failures

const TypeTagV1 = "cutting_garden-capture_failures-v1"

// Outcome values for Meta.Outcome.
const (
	// OutcomeFailures: the run completed; some entries failed.
	OutcomeFailures = "failures"
	// OutcomeAborted: a signal cut the run short (failures, if any,
	// are still listed). Aborted wins when both apply.
	OutcomeAborted = "aborted"
)

// Op values for FailureV1.Op — the operation that failed.
const (
	OpWalk         = "walk"
	OpStat         = "stat"
	OpReadlink     = "readlink"
	OpBlobWrite    = "blob-write"
	OpReceiptWrite = "receipt-write"
	OpPlugin       = "plugin"
)

// Meta is the failure receipt's hyphence metadata block.
type Meta struct {
	Ts       string   // RFC3339 UTC
	Outcome  string   // OutcomeFailures | OutcomeAborted
	Signal   string   // signal name; "" unless aborted by signal
	Receipt  string   // markl id of the paired success receipt; "" if its write failed
	Roots    []string // the capture's root args for this store group
	Captured int64
	Failed   int64
}

// FailureV1 is one NDJSON body line.
type FailureV1 struct {
	Root  string `json:"root"`
	Path  string `json:"path"`
	Op    string `json:"op"`
	Error string `json:"error"`
}

// V1 is the in-memory form of a v1 failure receipt.
type V1 struct {
	Meta     Meta
	Failures []FailureV1
}
```

**Step 4: Implement the coder**

`internal/capture_failures/coder.go` — copy `capture_receipt/coder.go`'s shape exactly (same imports). Metadata lines, all `- key value`, written in fixed order for byte-stable output: `ts`, `outcome`, `signal` (omit if ""), `receipt` (omit if ""), one `root <arg>` line per root, `captured N`, `failed N`. Decode via the same `ohio.MakeLineReaderKeyValues` map (`"!"` → Type, `"-"` → a `setMeta` closure switching on the first token). Truncate `Error` to 1 KiB at encode time (design: tuning lever).

Body coder: NDJSON `FailureV1` per line via `json.Encoder`/`json.Decoder` streaming into the pre-allocated `*V1` (metadata coder pre-populates `typedBlob.Blob = &V1{Meta: captured}` when Type matches, mirroring `capture_receipt`).

**Step 5: Implement io.go**

```go
// WriteV1 serializes v as a hyphence-wrapped failures-v1 blob to w.
func WriteV1(w io.Writer, v *V1) (int64, error)

// ReadV1 parses a failures-v1 blob.
func ReadV1(r io.Reader) (*V1, error)

// WriteV1ToStore encodes v and writes it into blobStore; returns the
// markl id string. Mirrors capture_receipt.WriteV1ToStore.
func WriteV1ToStore(blobStore blob_stores.BlobStoreInitialized, v *V1) (string, error)

// Read fetches and parses the blob named by id. Mirrors capture_receipt.Read.
func Read(blobStore domain_interfaces.BlobReaderFactory, id domain_interfaces.MarklId) (*V1, error)
```

**Step 6: Run tests** → PASS. Also `go build ./...`, `just lint-go-analyzers`.

**Step 7: Commit** — `feat(capture_failures): failures-v1 wire format + coder`

---

### Task 2: failures through plugin results

**Files:**
- Modify: `internal/cutting_garden_plugins/plugin.go` (CaptureRootResult, ~line 58)
- Modify: `internal/cutting_garden_plugin_file/plugin.go` (walkRoot, lines ~183–287)
- Modify: `internal/cutting_garden_plugin_ytdlp/capture.go` (walkArtifacts + CaptureRoot)
- Tests: `internal/cutting_garden_plugin_file/reporter_test.go` (extend), ytdlp `plugin_test.go` (extend)

**Step 1: Failing test** — in the file plugin's tests: capture a fixture tree containing an unreadable file (`os.Chmod(f, 0o000)`, restore in cleanup; skip if running as root — `os.Getuid() == 0`), assert `result.Failures` has exactly one element with `Op == capture_failures.OpBlobWrite`, `Path` ending in the file name, non-empty `Error`, and `result.FailCount == len(result.Failures)`.

**Step 2:** Run → FAIL (no Failures field).

**Step 3:** Add to `CaptureRootResult`:

```go
// Failures records each failed entry (root/path/op/error) for the
// capture orchestrator's failure receipt. FailCount stays the count
// authority for exit semantics; producers MUST keep
// FailCount == len(Failures) unless a failure has no per-entry
// identity (then FailCount may exceed).
Failures []capture_failures.FailureV1
```

In `walkRoot`, each of the six `stream.Failure(p, err)` sites also appends `capture_failures.FailureV1{Root: rootArg, Path: <rel or p>, Op: <site-specific>, Error: err.Error()}` to a slice returned alongside failCount (change signature `func walkRoot(...) (failures []capture_failures.FailureV1)` and derive count via `len`, keeping the trailing walk-abort handler's failure as `OpWalk`). Site→op mapping: walkErr → OpWalk, `d.Info()` → OpStat, `filepath.Rel` → OpStat, readlink → OpReadlink, `WriteFileBlob` → OpBlobWrite. ytdlp: every `failCount++` site appends with `OpPlugin` (or OpBlobWrite for its blob-write site — read the file; pick per site, the test pins at least one).

**Steps 4–5:** tests pass; `just lint-go-analyzers`; commit `feat(plugins): return per-entry failure detail in CaptureRootResult`.

---

### Task 3: orchestrator collection + failure-receipt write + spill

**Files:**
- Modify: `internal/capture/capture.go` (Run, lines ~470–655: the failCount accumulation sites and the post-receipt block)
- Create: `internal/capture/failure_receipt.go`
- Test: `internal/capture/failure_receipt_test.go`

**Step 1: Failing unit test** on the new helper, in isolation (no store): `buildFailureReceipt(groupRoots, captured, failures, receiptID, aborted, signalName)` returns a `*capture_failures.V1` with outcome rules: aborted wins over failures; `Ts` from `capture_log.Timestamp()`.

**Step 2–3:** implement `failure_receipt.go`:

```go
// writeFailureReceipt writes v into blobStore; on error spills the
// wire bytes to $XDG_STATE_HOME/cutting-garden/failures/<ts>.ndjson
// (MkdirAll 0o755) and returns ("", spillPath, nil). Never returns a
// fatal error — failure-receipt durability must not change the run's
// exit code.
func writeFailureReceipt(blobStore ..., cgEnvDir env_dir.Env, v *capture_failures.V1) (id, spillPath string, err error)
```

Spill filename: `Meta.Ts` with `:` replaced by `-`, plus `.ndjson`.

**Step 4: Wire into Run** — collect `[]capture_failures.FailureV1` per store group alongside `failCount` (classify failures at line ~482: `Op: OpPlugin, Path: cf.arg`; protocol per-root failures ~533: `OpPlugin`; `result.Failures` append at ~557–560; receipt-write failure ~602: `OpReceiptWrite, Path: "(receipt)"`). After the success-receipt write + captures.log entry build, when `len(groupFailures) > 0 || ctxAborted`:

- `ctxAborted := ctx.Err() != nil`; signal name: investigate `dewey/pkgs/errors` for the RFC 0002 signal cause (the framework prints `received signal: "interrupt"` — find the cause type via `rg 'received signal' $(go env GOMODCACHE)/github.com/amarbel-llc/purse-first/...`). If no exported accessor exists, record `Signal: ""` and file a dewey issue; do NOT parse strings.
- Build + write the failure receipt; print via the pipeline: `p.notice("failures store=%s id=%s count=%d", quoteEmpty(storeName), id, len(groupFailures))` (or the spill path variant).
- Stash `failureReceiptID` for Task 4's log entry.

Also extend the viewport reprint loop (~line 638) so failure-receipt ids survive viewport teardown like success receipts do.

**Step 5: Capture-level test** — extend the existing Run-level test harness (see `capture_run_test.go` patterns) with an unreadable-file fixture: assert success receipt AND `failures store=` line AND the failure blob parses via `capture_failures.Read` with the right entry.

**Step 6:** `go test ./internal/capture/ ./internal/capture_failures/`, analyzers, commit `feat(capture): write failure receipts (+ local spill fallback)`.

---

### Task 4: captures.log outcome + failure_receipt_id

**Files:**
- Modify: `internal/capture_log/capture_log.go` (Entry)
- Modify: `internal/capture/capture.go` (entry build sites ~541, ~610)
- Test: extend `internal/capture/capture_log_test.go`

**Step 1: Failing test** — round-trip an Entry with `Outcome: "failures"`, `FailureReceiptID: "sha256-x"`; assert a clean entry's JSON contains neither key (`omitempty`).

**Steps 2–3:**

```go
	// Outcome is "" for a clean capture, else "failures" or "aborted"
	// (capture_failures outcome values).
	Outcome string `json:"outcome,omitempty"`
	// FailureReceiptID is the markl id of the failure receipt, when one
	// was written ("" when clean or when the write spilled locally).
	FailureReceiptID string `json:"failure_receipt_id,omitempty"`
```

Populate at both entry-build sites from Task 3's stash. serve's entries are untouched (zero values omitted).

**Step 4:** tests + analyzers, commit `feat(capture_log): record outcome and failure receipt id`.

---

### Task 5: `cg failures` reader subcommand

**Files:**
- Create: `internal/failures/failures.go`
- Create: `internal/failures/manpage.go`
- Modify: `internal/cgapp/build.go` (+1 AddCmd, update doc comments "four user-facing" → "five")
- Test: `internal/failures/failures_test.go`

Model the package on `internal/restore/restore.go` (markl id parse → `command_components.MakeBlobStoreEnv` → `command_components.LocateReceiptStore(env, &id, cmd.Store)` → `capture_failures.Read`) and on `internal/diff` for the `-format` flag validation pattern.

Surface: `cg failures [-store STORE_ID] [-format text|json] FAILURE_RECEIPT_ID`

- text (default): metadata header lines (`outcome: aborted (interrupt)`, `receipt: sha256-…`, `roots: …`, `captured/failed: N/M`) then one `<op>\t<root>\t<path>\t<error>` line per failure on stdout.
- json: re-emit raw NDJSON body lines.
- Exit 0 on success; unreadable/missing receipt → plain error (exit 2); bad flags/args → BadRequest (exit 64). Use `newWithOutput(w io.Writer)` test constructor like restore's `newWithDiagnostics`.

**Steps:** golden-output failing test first (feed a `*V1` through a test double or write a real blob into a temp store via `WriteV1ToStore` — prefer the real store: see how restore's tests build one); implement; register in `cgapp.Build`; add `GetDescription` + manpage interfaces (`GetExamples`, `GetSeeAlso` listing capture/restore); verify `just debug-manpage cutting-garden-failures` renders. Tests + analyzers, commit `feat(failures): cg failures reader subcommand`.

---

### Task 6: bats end-to-end lane

**Files:**
- Create or extend: `zz-tests_bats/` — follow @eng:wiring-bats-tests and the existing lane layout (`just test-bats` runs it; read an existing .bats file first for the harness conventions).

Scenario: build fixture dir with one `chmod 000` file (skip under root); `cutting-garden capture <store> <dir>` → expect exit 2, stdout contains `receipt store=` and `failures store=`; extract the failures id; `cutting-garden failures <id>` lists the path with op `blob-write`; captures.log line has `outcome` and `failure_receipt_id`. Commit `test(bats): failure receipt end-to-end`.

---

### Task 7: docs + wrap

**Files:**
- Create: `docs/features/00NN-capture-failure-receipts.md` (next free FDR number) via @eng:fdr — Tuning Levers section carries the design doc's two levers (entry cap: uncapped; error truncation: 1 KiB).
- Modify: `CLAUDE.md` project-status sentence (subcommand list) and `README.md` Commands list (+`failures`).
- File followup issue: `capture --retry <failure-receipt>` (format-ready; semantics out of scope), referencing the FDR.

Commit `docs: FDR for capture failure receipts`.

---

### Execution notes

- Tasks 1→5 are strictly ordered; 6–7 after.
- Full gate (`just`) runs in the merge hook — do NOT run plain `just` redundantly; per-package `go test` + `just lint-go-analyzers` per task is the loop.
- If the dewey signal-cause investigation (Task 3) dead-ends, ship with `Signal: ""` + a filed dewey issue; do not block.
