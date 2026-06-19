package caldav

import (
	"context"
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
)

// captureForDiff runs CaptureProtocol against the fake and returns the
// receipt digest + the store, so the diff tests can compare a live source
// against it.
func captureForDiff(
	t *testing.T,
	arg string,
) (string, blob_stores.BlobStoreInitialized) {
	t.Helper()
	store := newMemStore(t)
	res, err := Plugin{}.CaptureProtocol(cutting_garden_plugins.ProtocolCaptureRequest{
		Context: context.Background(), Source: mustParseURL(t, arg),
		RawArg: arg, BlobStore: store,
	})
	if err != nil {
		t.Fatalf("CaptureProtocol: %v", err)
	}
	return res.ReceiptDigest, store
}

func diffNow(t *testing.T, arg, receipt string, store blob_stores.BlobStoreInitialized) []string {
	t.Helper()
	res, err := Plugin{}.DiffProtocol(cutting_garden_plugins.ProtocolDiffRequest{
		Context:       context.Background(),
		BlobStore:     store,
		ReceiptDigest: receipt,
		Source:        mustParseURL(t, arg),
		RawSource:     arg,
	})
	if err != nil {
		t.Fatalf("DiffProtocol: %v", err)
	}
	return res.Differences
}

// TestDiffProtocol_CleanWhenUnchanged: diffing a receipt against the same
// unchanged source reports no differences (and, via the etag fast path,
// transfers no bodies).
func TestDiffProtocol_CleanWhenUnchanged(t *testing.T) {
	_, arg := startFake(t)
	receipt, store := captureForDiff(t, arg)

	diffs := diffNow(t, arg, receipt, store)
	if len(diffs) != 0 {
		t.Errorf("clean diff = %v, want none", diffs)
	}
}

// TestDiffProtocol_DetectsAddRemoveModify exercises all three: one added
// resource, one removed, one whose body (and thus etag) changed.
func TestDiffProtocol_DetectsAddRemoveModify(t *testing.T) {
	f, arg := startFake(t)
	receipt, store := captureForDiff(t, arg)

	// Mutate the live source:
	//   - add a new event
	//   - remove an existing task
	//   - change an existing task's body (moves its etag)
	f.seed("/dav/cal/event2.ics", "VEVENT", vevent("event2", "Retro"))
	f.remove("/dav/cal/task2.ics")
	f.seed("/dav/cal/task1.ics", "VTODO", vtodo("task1", "Buy oat milk"))

	diffs := diffNow(t, arg, receipt, store)
	got := strings.Join(diffs, "\n")

	wants := []string{
		"A cal/VEVENT/event2",
		"D cal/VTODO/task2",
		"M cal/VTODO/task1",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("diff missing %q\n got:\n%s", w, got)
		}
	}
	if len(diffs) != 3 {
		t.Errorf("diff = %v, want exactly 3 lines (A/D/M)", diffs)
	}
}

// TestDiffProtocol_NoModifyOnIdenticalReseed: re-seeding a resource with
// byte-identical content keeps its etag (the fake derives etag from body),
// so the etag fast path reports no change — no false M.
func TestDiffProtocol_NoModifyOnIdenticalReseed(t *testing.T) {
	f, arg := startFake(t)
	receipt, store := captureForDiff(t, arg)

	// Re-seed task1 with byte-identical content: same body ⇒ same fakeEtag
	// ⇒ etag fast path ⇒ no diff.
	f.seed("/dav/cal/task1.ics", "VTODO", vtodo("task1", "Buy milk"))

	diffs := diffNow(t, arg, receipt, store)
	if len(diffs) != 0 {
		t.Errorf("identical reseed diff = %v, want none", diffs)
	}
}
