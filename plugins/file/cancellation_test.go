package cutting_garden_plugin_file

import (
	"context"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/capture_receipt"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// TestCaptureRoot_AbortsOnContextCancellation pins prompt-cancel
// semantics for the capture walk (SIGINT/SIGTERM cancel the
// errors.Context that req.Context derives from): a cancelled context
// must abort the walk itself, not just fail each file's blob write.
// The wire shape is one Failure carrying context.Canceled and a
// not-OK phase verdict — NOT one failure per remaining file with
// dir/symlink entries still captured.
func TestCaptureRoot_AbortsOnContextCancellation(t *testing.T) {
	dir := t.TempDir()
	writeWalkFixture(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rep := &recordingStream{}
	result := Plugin{}.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Context:   ctx,
		Source:    &url.URL{Path: dir},
		RawArg:    "fixture-arg",
		BlobStore: newDiscardStore(),
		Reporter:  rep,
	})

	if len(result.Entries) != 0 {
		t.Errorf("entries = %d, want 0 (cancelled before any entry): %+v",
			len(result.Entries), result.Entries)
	}
	if result.FailCount != 1 {
		t.Errorf("FailCount = %d, want 1 (single walk abort, not per-file failures): %v",
			result.FailCount, rep.failures)
	}
	if len(rep.failures) != 1 {
		t.Fatalf("failures = %d, want exactly one: %v", len(rep.failures), rep.failures)
	}
	if !errors.Is(rep.failures[0].err, context.Canceled) {
		t.Errorf("failure err = %v, want context.Canceled in chain", rep.failures[0].err)
	}

	// The phase still closes — aborted walks must not leave a dangling
	// PhaseStart on the wire.
	if len(rep.phaseEnds) != 1 {
		t.Fatalf("phaseEnds = %d, want 1: %+v", len(rep.phaseEnds), rep.phaseEnds)
	}
	if rep.phaseEnds[0].OK {
		t.Errorf("phase verdict OK = true, want false on cancellation")
	}
}

// TestRestore_AbortsOnContextCancellation pins the restore side: the
// materialize loop must consult the context between entries, not just
// inside per-file blob copies (a dir/symlink-only receipt performs
// mkdir/symlink IO without ever touching CtxReader).
func TestRestore_AbortsOnContextCancellation(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out")
	entries := []capture_receipt.EntryV1{
		{Path: ".", Root: "r", Type: capture_receipt.TypeDir, Mode: fs.ModeDir | 0o755},
		{Path: "sub", Root: "r", Type: capture_receipt.TypeDir, Mode: fs.ModeDir | 0o755},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Plugin{}.Restore(cutting_garden_plugins.RestoreRequest{
		Context:   ctx,
		Dest:      &url.URL{Path: dest},
		Entries:   entries,
		BlobStore: newDiscardStore(),
	})

	if err == nil {
		t.Fatal("err = nil, want cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled in chain", err)
	}
	if _, statErr := os.Lstat(filepath.Join(dest, "r")); !os.IsNotExist(statErr) {
		t.Errorf("entry materialized despite cancelled context (lstat err = %v)", statErr)
	}
}

// TestScanForDiff_AbortsOnContextCancellation is the diff-side twin:
// walkForDiff must unwind with context.Canceled in the error chain
// instead of enumerating the remaining tree into per-entry failures.
func TestScanForDiff_AbortsOnContextCancellation(t *testing.T) {
	dir := t.TempDir()
	writeWalkFixture(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	entries, err := Plugin{}.ScanForDiff(cutting_garden_plugins.DiffScanRequest{
		Context:   ctx,
		Dir:       &url.URL{Path: dir},
		BlobStore: newDiscardStore(),
	})

	if err == nil {
		t.Fatal("err = nil, want cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled in chain", err)
	}
	if entries != nil {
		t.Errorf("entries = %+v, want nil on cancellation", entries)
	}
}
