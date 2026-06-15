package cutting_garden_plugin_git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/amarbel-llc/cutting-garden/pkgs/capture_events"
	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
)

// recordingReporter captures every Plan/Progress/Log/Phase* call the
// plugin makes so a test can assert on the emitted observability without
// inspecting any blob bytes. It embeds capture_events.Nop and overrides
// only what it records.
type recordingReporter struct {
	capture_events.Nop
	mu          sync.Mutex
	plans       []cutting_garden_plugins.ReportPlan
	progress    []cutting_garden_plugins.ReportProgress
	logs        []string
	phaseStarts []string
	phaseEnds   []capture_events.Verdict
}

func (r *recordingReporter) PhaseStart(description string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phaseStarts = append(r.phaseStarts, description)
}

func (r *recordingReporter) PhaseEnd(v capture_events.Verdict) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phaseEnds = append(r.phaseEnds, v)
}

func (r *recordingReporter) Plan(p cutting_garden_plugins.ReportPlan) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plans = append(r.plans, p)
}

func (r *recordingReporter) Progress(p cutting_garden_plugins.ReportProgress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.progress = append(r.progress, p)
}

func (r *recordingReporter) Log(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, format)
}

// TestCaptureProtocol_Reporter_EmitsStructuralPlanProgressLog drives a
// full capture of a small local repo through CaptureProtocol with a
// recording reporter and asserts the non-identity observability contract.
// Plan/Progress are framed over the STRUCTURAL skeleton (commit+tree
// objects) only — blobs are still written but not reported individually,
// since blob leaves dominate the object count and are too noisy. So:
// exactly one Plan whose Items equals the repo's commit+tree count, one
// monotonic Progress per structural object (1..N), each Item prefixed
// "commit "/"tree " (never "blob"), and at least the clone + receipt Logs.
func TestCaptureProtocol_Reporter_EmitsStructuralPlanProgressLog(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	repo := newLocalRepo(t)
	store := newMemStore(t)
	rec := &recordingReporter{}

	res, err := (Plugin{}).CaptureProtocol(cutting_garden_plugins.ProtocolCaptureRequest{
		Context:   context.Background(),
		BlobStore: store,
		RawArg:    "git:" + repo + "#main",
		Source:    mustParseURL(t, "git:"+repo+"#main"),
		Reporter:  rec,
	})
	if err != nil {
		t.Fatalf("CaptureProtocol: %v", err)
	}

	// Independent oracle: the source repo's commit+tree count via real git.
	wantStructural := countCommitTreeObjects(t, repo)
	if wantStructural == 0 {
		t.Fatalf("test repo has no commit/tree objects")
	}
	// Sanity: structural count is strictly less than the total object count
	// (the repo has blobs), so this test would catch a regression to
	// per-object framing.
	if wantStructural >= res.ObjectCount {
		t.Fatalf("structural count %d not < total object count %d; repo lacks blobs?",
			wantStructural, res.ObjectCount)
	}

	// Exactly one Plan, with Items == the structural (commit+tree) count.
	if len(rec.plans) != 1 {
		t.Fatalf("expected exactly 1 Plan, got %d: %v", len(rec.plans), rec.plans)
	}
	if got, want := rec.plans[0].Items, int64(wantStructural); got != want {
		t.Errorf("Plan.Items = %d, want structural count %d", got, want)
	}
	if rec.plans[0].Label == "" {
		t.Errorf("Plan.Label is empty")
	}

	// Progress fired once per structural object, monotonic 1..N, each
	// labeled "commit "/"tree " (never "blob").
	if got, want := len(rec.progress), wantStructural; got != want {
		t.Fatalf("Progress called %d times, want %d (structural count)", got, want)
	}
	for i, p := range rec.progress {
		if got, want := p.Items, int64(i+1); got != want {
			t.Errorf("Progress[%d].Items = %d, want %d (monotonic 1..N)", i, p.Items, want)
		}
		if !strings.HasPrefix(p.Item, "commit ") && !strings.HasPrefix(p.Item, "tree ") {
			t.Errorf("Progress[%d].Item = %q, want a commit/tree prefix", i, p.Item)
		}
		if strings.HasPrefix(p.Item, "blob ") {
			t.Errorf("Progress[%d].Item = %q reports a blob (should be structural-only)", i, p.Item)
		}
	}

	// The receipt Log is still present (the clone Log folded into the
	// clone PhaseStart description below).
	if !logsContainSubstr(rec.logs, "receipt") {
		t.Errorf("expected a receipt Log, got %v", rec.logs)
	}

	// Full path emits exactly two phases: a clone phase (the old
	// "cloning…" Log folded into its description) and a store phase whose
	// count is the structural count — both ending OK with no directive.
	if len(rec.phaseStarts) != 2 {
		t.Fatalf("expected 2 PhaseStarts, got %d: %v", len(rec.phaseStarts), rec.phaseStarts)
	}
	if !strings.HasPrefix(rec.phaseStarts[0], "clone ") {
		t.Errorf("phaseStarts[0] = %q, want prefix \"clone \"", rec.phaseStarts[0])
	}
	if got, want := rec.phaseStarts[1], fmt.Sprintf("store %d objects", wantStructural); got != want {
		t.Errorf("phaseStarts[1] = %q, want %q", got, want)
	}
	if len(rec.phaseEnds) != 2 {
		t.Fatalf("expected 2 PhaseEnds, got %d: %v", len(rec.phaseEnds), rec.phaseEnds)
	}
	for i, v := range rec.phaseEnds {
		if !v.OK {
			t.Errorf("phaseEnds[%d].OK = false, want true", i)
		}
		if v.Directive != nil {
			t.Errorf("phaseEnds[%d].Directive = %+v, want nil", i, v.Directive)
		}
	}

	// The resolved-tip Log survives as a clone-phase tail detail.
	if !logsContainSubstr(rec.logs, "resolved") {
		t.Errorf("expected a resolved-tip Log, got %v", rec.logs)
	}
}

// TestIncrementalCapture_Reporter_PhaseEvents drives the incremental
// probe against a real local repo and asserts its phase emissions:
//
//   - same-tip (nothing changed): one "check prior capture" phase that
//     closes with an OK verdict carrying a SKIP directive (replacing the
//     old bare "no changes" Log);
//   - delta path: the check phase closes OK (changes found), then a
//     clone-equivalent "fetch delta from …" phase, then a
//     "store N delta objects" phase whose N matches the structural Plan.
func TestIncrementalCapture_Reporter_PhaseEvents(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	repo := newLocalRepo(t)
	store := newMemStore(t)

	res1, err := captureProtocol(context.Background(),
		capturePluginWriter(store), repo, "main", cutting_garden_plugins.NopReporter{})
	if err != nil {
		t.Fatalf("full capture: %v", err)
	}

	// Same-tip probe: the check phase ends with a skip directive.
	recSame := &recordingReporter{}
	_, ok, err := tryIncrementalCapture(context.Background(),
		store, capturePluginWriter(store), repo, "main", res1.ReceiptDigest, recSame)
	if err != nil || !ok {
		t.Fatalf("incremental (unchanged): ok=%v err=%v", ok, err)
	}
	if len(recSame.phaseStarts) != 1 || recSame.phaseStarts[0] != "check prior capture" {
		t.Fatalf("same-tip phaseStarts = %v, want [\"check prior capture\"]", recSame.phaseStarts)
	}
	if len(recSame.phaseEnds) != 1 {
		t.Fatalf("same-tip phaseEnds = %v, want exactly 1", recSame.phaseEnds)
	}
	skip := recSame.phaseEnds[0]
	if !skip.OK {
		t.Errorf("skip verdict OK = false, want true")
	}
	if skip.Directive == nil {
		t.Fatalf("skip verdict has no directive: %+v", skip)
	}
	if got, want := skip.Directive.Kind, capture_events.DirectiveSkip; got != want {
		t.Errorf("skip directive kind = %q, want %q", got, want)
	}
	if got, want := skip.Directive.Reason, "no changes since prior capture"; got != want {
		t.Errorf("skip directive reason = %q, want %q", got, want)
	}
	if logsContainSubstr(recSame.logs, "no changes") {
		t.Errorf("the bare \"no changes\" Log should be replaced by the skip directive; logs: %v", recSame.logs)
	}

	// Advance the branch so the next probe takes the delta path.
	if err := os.WriteFile(filepath.Join(repo, "another.txt"), []byte("more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCLI(t, repo, "add", "-A")
	gitCLI(t, repo, "commit", "-q", "-m", "second")

	recDelta := &recordingReporter{}
	_, ok, err = tryIncrementalCapture(context.Background(),
		store, capturePluginWriter(store), repo, "main", res1.ReceiptDigest, recDelta)
	if err != nil || !ok {
		t.Fatalf("incremental (delta): ok=%v err=%v", ok, err)
	}

	if len(recDelta.phaseStarts) != 3 {
		t.Fatalf("delta phaseStarts = %v, want 3 (check, fetch, store)", recDelta.phaseStarts)
	}
	if got, want := recDelta.phaseStarts[0], "check prior capture"; got != want {
		t.Errorf("delta phaseStarts[0] = %q, want %q", got, want)
	}
	if !strings.HasPrefix(recDelta.phaseStarts[1], "fetch delta from ") {
		t.Errorf("delta phaseStarts[1] = %q, want prefix \"fetch delta from \"", recDelta.phaseStarts[1])
	}
	if len(recDelta.plans) != 1 {
		t.Fatalf("delta plans = %v, want exactly 1", recDelta.plans)
	}
	if got, want := recDelta.phaseStarts[2],
		fmt.Sprintf("store %d delta objects", recDelta.plans[0].Items); got != want {
		t.Errorf("delta phaseStarts[2] = %q, want %q (N matching the structural Plan)", got, want)
	}
	if len(recDelta.phaseEnds) != 3 {
		t.Fatalf("delta phaseEnds = %v, want 3", recDelta.phaseEnds)
	}
	for i, v := range recDelta.phaseEnds {
		if !v.OK {
			t.Errorf("delta phaseEnds[%d].OK = false, want true", i)
		}
		if v.Directive != nil {
			t.Errorf("delta phaseEnds[%d].Directive = %+v, want nil (no skip on the delta path)", i, v.Directive)
		}
	}
}

// TestIncrementalCapture_Reporter_FetchFailureTodoDirective provokes a
// fetch failure on the delta path: the branch ref is pointed at a
// fabricated oid, so the tip probe (which reads refs only) sees a change
// while the fetch — which must build a pack starting from that missing
// commit — fails. The fetch phase must close as TAP's tolerated-failure
// form (OK=false with a TODO directive "fell back to full capture"), not
// a bare not-ok that strict harnesses would fail inside a passing run,
// and the swallowed fetch error must ride in the verdict diagnostic. The
// soft-fallback contract is unchanged: (ok=false, err=nil) so the caller
// runs a full capture.
func TestIncrementalCapture_Reporter_FetchFailureTodoDirective(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	repo := newLocalRepo(t)
	store := newMemStore(t)

	res1, err := captureProtocol(context.Background(),
		capturePluginWriter(store), repo, "main", cutting_garden_plugins.NopReporter{})
	if err != nil {
		t.Fatalf("full capture: %v", err)
	}

	// Point main at an oid no object database holds. Written directly
	// because `git update-ref` refuses a nonexistent object; the loose-ref
	// write is safe here since go-git (and so this whole suite) requires
	// the files ref backend anyway.
	bogus := strings.Repeat("deadbeef", 5)
	refPath := filepath.Join(repo, ".git", "refs", "heads", "main")
	if err := os.WriteFile(refPath, []byte(bogus+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := &recordingReporter{}
	_, ok, err := tryIncrementalCapture(context.Background(),
		store, capturePluginWriter(store), repo, "main", res1.ReceiptDigest, rec)
	if err != nil {
		t.Fatalf("fetch failure must be a soft miss (err=nil), got: %v", err)
	}
	if ok {
		t.Fatalf("fetch failure must report ok=false (fall back to full capture)")
	}

	if len(rec.phaseStarts) != 2 {
		t.Fatalf("phaseStarts = %v, want 2 (check, fetch)", rec.phaseStarts)
	}
	if !strings.HasPrefix(rec.phaseStarts[1], "fetch delta from ") {
		t.Errorf("phaseStarts[1] = %q, want prefix \"fetch delta from \"", rec.phaseStarts[1])
	}
	if len(rec.phaseEnds) != 2 {
		t.Fatalf("phaseEnds = %v, want 2", rec.phaseEnds)
	}

	v := rec.phaseEnds[1]
	if v.OK {
		t.Errorf("fetch verdict OK = true, want false")
	}
	if v.Directive == nil {
		t.Fatalf("fetch verdict carries no directive (a bare not-ok fails strict TAP harnesses): %+v", v)
	} else {
		if got, want := v.Directive.Kind, capture_events.DirectiveTodo; got != want {
			t.Errorf("directive kind = %q, want %q", got, want)
		}
		if got, want := v.Directive.Reason, "fell back to full capture"; got != want {
			t.Errorf("directive reason = %q, want %q", got, want)
		}
	}
	errVal, present := v.Diagnostic["error"]
	if !present {
		t.Fatalf("verdict diagnostic missing \"error\": %+v", v.Diagnostic)
	}
	if s, isStr := errVal.(string); !isStr || s == "" {
		t.Errorf("diagnostic[\"error\"] = %#v, want a non-empty string", errVal)
	}
}

// countCommitTreeObjects returns the number of commit and tree objects in
// repo's object database, via the real-git oracle — the expected
// structural Plan/Progress count.
func countCommitTreeObjects(t *testing.T, repo string) int {
	t.Helper()
	var n int
	for _, o := range realRepoObjects(t, repo) {
		if o.typ == "commit" || o.typ == "tree" {
			n++
		}
	}
	return n
}

// TestCaptureProtocol_Reporter_DoesNotAffectIdentity is the critical
// non-identity guarantee: capturing the SAME repo twice — once with a
// recording reporter, once with a nil Reporter — yields an IDENTICAL
// captured object graph. Emission is semantics, never identity.
//
// The comparison is on the payload digest, not the receipt digest: the
// receipt's outcome node carries the capture datetime (time.Now via
// capture_plugin.ReceiptParams.Now), so two captures at different wall
// clocks always differ at the receipt root regardless of the reporter.
// The payload node — the object-graph content this plugin emits — is the
// byte-stable identity (sorted by oid in writeGitReceipt); it is exactly
// what TestIncrementalCapture_RealGit_MatchesFullCapture compares.
func TestCaptureProtocol_Reporter_DoesNotAffectIdentity(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	repo := newLocalRepo(t)
	arg := "git:" + repo + "#main"

	withStore := newMemStore(t)
	withReporter, err := (Plugin{}).CaptureProtocol(cutting_garden_plugins.ProtocolCaptureRequest{
		Context:   context.Background(),
		BlobStore: withStore,
		RawArg:    arg,
		Source:    mustParseURL(t, arg),
		Reporter:  &recordingReporter{},
	})
	if err != nil {
		t.Fatalf("CaptureProtocol (with reporter): %v", err)
	}

	withoutStore := newMemStore(t)
	withoutReporter, err := (Plugin{}).CaptureProtocol(cutting_garden_plugins.ProtocolCaptureRequest{
		Context:   context.Background(),
		BlobStore: withoutStore,
		RawArg:    arg,
		Source:    mustParseURL(t, arg),
		Reporter:  nil,
	})
	if err != nil {
		t.Fatalf("CaptureProtocol (nil reporter): %v", err)
	}

	withPayload := receiptPayloadDigest(t, withStore, withReporter.ReceiptDigest)
	withoutPayload := receiptPayloadDigest(t, withoutStore, withoutReporter.ReceiptDigest)
	if withPayload != withoutPayload {
		t.Errorf("payload digest differs with vs without reporter:\n with    = %s\n without = %s",
			withPayload, withoutPayload)
	}
}

// TestProgressLogWriter_SplitsOnCRandLF is a pure unit test for the clone
// progress sideband adapter: go-git's server progress uses BOTH '\r'
// (in-place percent updates) and '\n' (phase end) as separators, so the
// writer must flush a trimmed log line on either, skipping empty segments,
// and keep any trailing partial segment buffered.
func TestProgressLogWriter_SplitsOnCRandLF(t *testing.T) {
	var got []string
	w := &progressLogWriter{log: func(s string) { got = append(got, s) }}

	in := "Counting objects: 5%\rCounting objects: 100%\nReceiving objects: 50%\rDone\n"
	if _, err := w.Write([]byte(in)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	want := []string{
		"Counting objects: 5%",
		"Counting objects: 100%",
		"Receiving objects: 50%",
		"Done",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d log lines %q, want %d %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("log[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestProgressLogWriter_BuffersPartialSegment confirms a trailing segment
// with no terminator stays buffered across Writes (no premature flush)
// and is emitted once its terminator arrives.
func TestProgressLogWriter_BuffersPartialSegment(t *testing.T) {
	var got []string
	w := &progressLogWriter{log: func(s string) { got = append(got, s) }}

	if _, err := w.Write([]byte("Receiving ")); err != nil {
		t.Fatalf("Write 1: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("partial segment flushed prematurely: %q", got)
	}
	if _, err := w.Write([]byte("objects: 100%\n")); err != nil {
		t.Fatalf("Write 2: %v", err)
	}
	if len(got) != 1 || got[0] != "Receiving objects: 100%" {
		t.Fatalf("got %q, want [\"Receiving objects: 100%%\"]", got)
	}
}

func logsContainSubstr(logs []string, substr string) bool {
	for _, l := range logs {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}
