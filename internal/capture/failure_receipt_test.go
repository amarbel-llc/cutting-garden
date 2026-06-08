package capture

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/capture_failures"
	"github.com/amarbel-llc/cutting-garden/internal/capture_log"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/madder/go/pkgs/domain_interfaces"
	"github.com/amarbel-llc/madder/go/pkgs/markl"
	_ "github.com/amarbel-llc/madder/go/pkgs/markl_registrations"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/files"
)

// stubTimestamp pins capture_log.Timestamp (a package var, indirected
// exactly so tests can stub it) for the duration of the test.
func stubTimestamp(t *testing.T, ts string) {
	t.Helper()
	orig := capture_log.Timestamp
	capture_log.Timestamp = func() string { return ts }
	t.Cleanup(func() { capture_log.Timestamp = orig })
}

func sampleFailures() []capture_failures.FailureV1 {
	return []capture_failures.FailureV1{
		{
			Root: ".", Path: "a/b.txt", Op: capture_failures.OpBlobWrite,
			Error: "read: permission denied",
		},
		{
			Root: ".", Path: "c.txt", Op: capture_failures.OpStat,
			Error: "stale handle",
		},
	}
}

func TestBuildFailureReceipt_OutcomeFailures(t *testing.T) {
	stubTimestamp(t, "2026-06-07T12:00:00Z")

	v := buildFailureReceipt(
		[]string{"./", "other/"}, 41, sampleFailures(),
		"sha256-abc", false, "",
	)

	want := capture_failures.Meta{
		Ts:       "2026-06-07T12:00:00Z",
		Outcome:  capture_failures.OutcomeFailures,
		Signal:   "",
		Receipt:  "sha256-abc",
		Roots:    []string{"./", "other/"},
		Captured: 41,
		Failed:   2,
	}
	if !reflect.DeepEqual(v.Meta, want) {
		t.Errorf("Meta = %+v, want %+v", v.Meta, want)
	}
	if !reflect.DeepEqual(v.Failures, sampleFailures()) {
		t.Errorf("Failures = %+v", v.Failures)
	}
}

// TestBuildFailureReceipt_AbortedWinsOverFailures pins the outcome
// rule: when entries failed AND a signal cut the run short, the
// outcome is aborted (the failure list is still present).
func TestBuildFailureReceipt_AbortedWinsOverFailures(t *testing.T) {
	stubTimestamp(t, "2026-06-07T12:00:00Z")

	v := buildFailureReceipt(
		[]string{"."}, 7, sampleFailures(), "", true, "interrupt",
	)

	if v.Meta.Outcome != capture_failures.OutcomeAborted {
		t.Errorf("Outcome = %q, want %q (aborted wins over failures)",
			v.Meta.Outcome, capture_failures.OutcomeAborted)
	}
	if v.Meta.Signal != "interrupt" {
		t.Errorf("Signal = %q, want %q", v.Meta.Signal, "interrupt")
	}
	if v.Meta.Failed != 2 || len(v.Failures) != 2 {
		t.Errorf("Failed = %d, len(Failures) = %d, want 2/2",
			v.Meta.Failed, len(v.Failures))
	}
}

// TestBuildFailureReceipt_SignalRecordedOnlyWhenAborted pins the
// design rule that `signal` only appears on aborted receipts — a
// stray signal name with aborted=false is blanked.
func TestBuildFailureReceipt_SignalRecordedOnlyWhenAborted(t *testing.T) {
	stubTimestamp(t, "2026-06-07T12:00:00Z")

	v := buildFailureReceipt(
		[]string{"."}, 1, sampleFailures(), "", false, "interrupt",
	)

	if v.Meta.Outcome != capture_failures.OutcomeFailures {
		t.Errorf("Outcome = %q, want failures", v.Meta.Outcome)
	}
	if v.Meta.Signal != "" {
		t.Errorf("Signal = %q, want \"\" when not aborted", v.Meta.Signal)
	}
}

// TestSignalCauseName pins the dewey RFC 0002 signal-cause extraction:
// dewey's signal handler cancels the context with
// errors.Signal{Signal: sig}; signalCauseName recovers the name from
// context.Cause without parsing error strings.
func TestSignalCauseName(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errors.Signal{Signal: os.Interrupt})
	if got := signalCauseName(ctx); got != "interrupt" {
		t.Errorf("signalCauseName(signal-cancelled) = %q, want %q",
			got, "interrupt")
	}

	if got := signalCauseName(context.Background()); got != "" {
		t.Errorf("signalCauseName(live ctx) = %q, want \"\"", got)
	}

	plain, plainCancel := context.WithCancelCause(context.Background())
	plainCancel(errors.Errorf("not a signal"))
	if got := signalCauseName(plain); got != "" {
		t.Errorf("signalCauseName(non-signal cancel) = %q, want \"\"", got)
	}
}

// failingBlobWriterFactory is the minimal store double for the spill
// path: writeFailureReceipt only needs MakeBlobWriter, so the double
// is one method erroring unconditionally.
type failingBlobWriterFactory struct{}

func (failingBlobWriterFactory) MakeBlobWriter(
	domain_interfaces.FormatHash,
) (domain_interfaces.BlobWriter, error) {
	return nil, errors.Errorf("store offline")
}

func sampleFailureReceipt() *capture_failures.V1 {
	return &capture_failures.V1{
		Meta: capture_failures.Meta{
			Ts:       "2026-06-07T12:00:00Z",
			Outcome:  capture_failures.OutcomeFailures,
			Receipt:  "sha256-abc",
			Roots:    []string{"./"},
			Captured: 41,
			Failed:   2,
		},
		Failures: sampleFailures(),
	}
}

func TestWriteFailureReceipt_StoreWriteSucceeds(t *testing.T) {
	cg := setupCgEnvDir(t)
	store := blob_stores.NewDiscardBlobStore(markl.FormatHashSha256)

	id, spillPath, err := writeFailureReceipt(store, cg, sampleFailureReceipt())
	if err != nil {
		t.Fatalf("writeFailureReceipt: %v", err)
	}
	if spillPath != "" {
		t.Errorf("spillPath = %q, want \"\" on store success", spillPath)
	}

	var parsed markl.Id
	if perr := parsed.Set(id); perr != nil {
		t.Errorf("id %q does not parse as a markl id: %v", id, perr)
	}

	failuresDir := cg.GetXDG().State.MakePath("failures").String()
	if _, serr := os.Stat(failuresDir); !os.IsNotExist(serr) {
		t.Errorf("spill dir %q should not exist on store success (err=%v)",
			failuresDir, serr)
	}
}

// TestWriteFailureReceipt_SpillsWhenStoreWriteFails pins the fallback:
// a failing store write degrades to a local NDJSON spill under
// $XDG_STATE_HOME/cutting-garden/failures/<ts>.ndjson (':' → '-'),
// byte-identical to the wire (round-trips via ReadV1).
func TestWriteFailureReceipt_SpillsWhenStoreWriteFails(t *testing.T) {
	cg := setupCgEnvDir(t)
	want := sampleFailureReceipt()

	id, spillPath, err := writeFailureReceipt(failingBlobWriterFactory{}, cg, want)
	if err != nil {
		t.Fatalf("writeFailureReceipt: %v (spill must not surface as fatal)", err)
	}
	if id != "" {
		t.Errorf("id = %q, want \"\" when spilled", id)
	}

	const wantName = "2026-06-07T12-00-00Z.ndjson"
	if got := filepath.Base(spillPath); got != wantName {
		t.Errorf("spill filename = %q, want %q", got, wantName)
	}
	if got := filepath.Base(filepath.Dir(spillPath)); got != "failures" {
		t.Errorf("spill parent dir = %q, want %q", got, "failures")
	}

	file, oerr := os.Open(spillPath)
	if oerr != nil {
		t.Fatalf("open spill file: %v", oerr)
	}
	defer files.CloseReadOnly(file)

	got, rerr := capture_failures.ReadV1(file)
	if rerr != nil {
		t.Fatalf("spill bytes do not parse as failures-v1: %v", rerr)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("spill round-trip:\n got %+v\nwant %+v", got, want)
	}
}

// TestSpillFailureReceipt_SameTsDoesNotOverwrite pins the multi-group
// collision case: two groups spilling within the same wall-clock
// second (identical Meta.Ts) must land in two distinct files — the
// second spill must not truncate the first ("triage info must survive
// the outage", design §Write path).
func TestSpillFailureReceipt_SameTsDoesNotOverwrite(t *testing.T) {
	cg := setupCgEnvDir(t)
	const ts = "2026-06-07T12:00:00Z"

	first, err := spillFailureReceipt(cg, ts, []byte("group-a\n"))
	if err != nil {
		t.Fatalf("first spill: %v", err)
	}
	second, err := spillFailureReceipt(cg, ts, []byte("group-b\n"))
	if err != nil {
		t.Fatalf("second spill: %v", err)
	}

	if first == second {
		t.Fatalf("both spills landed at %q; want distinct paths", first)
	}
	for path, want := range map[string]string{
		first:  "group-a\n",
		second: "group-b\n",
	} {
		got, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Fatalf("read %q: %v", path, rerr)
		}
		if string(got) != want {
			t.Errorf("%q = %q, want %q", path, got, want)
		}
	}
}

// TestWriteFailureReceipt_SpillFailureReturnsError pins the
// double-failure contract: when the store write AND the spill both
// fail, writeFailureReceipt reports an error (the caller degrades it
// to a notice) instead of pretending durability.
func TestWriteFailureReceipt_SpillFailureReturnsError(t *testing.T) {
	cg := setupCgEnvDir(t)

	// Plant a regular file where the failures/ dir must be created so
	// the spill's MkdirAll fails without needing root.
	blocker := cg.GetXDG().State.MakePath("failures").String()
	if err := os.MkdirAll(filepath.Dir(blocker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocker, []byte("blocker"), 0o644); err != nil {
		t.Fatal(err)
	}

	id, spillPath, err := writeFailureReceipt(
		failingBlobWriterFactory{}, cg, sampleFailureReceipt(),
	)
	if err == nil {
		t.Fatal("err = nil, want non-nil when store write and spill both fail")
	}
	if id != "" || spillPath != "" {
		t.Errorf("id = %q, spillPath = %q, want both empty", id, spillPath)
	}
}
