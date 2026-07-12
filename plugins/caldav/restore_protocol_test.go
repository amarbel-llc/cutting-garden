package caldav

import (
	"context"
	"sort"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// TestRestoreProtocol_PutsByNativeIdentity captures from one server, then
// restores to a separate (empty) server and asserts each object is PUT at
// <matched-calendar-href>/<UID>.ics — reconstructed from native identity,
// with the destination's real collection layout discovered via PROPFIND
// (so the /dav/cal/ prefix is preserved, not dropped).
func TestRestoreProtocol_PutsByNativeIdentity(t *testing.T) {
	_, srcArg := startFake(t)
	store := newMemStore(t)

	captured, err := Plugin{}.CaptureProtocol(cutting_garden_plugins.ProtocolCaptureRequest{
		Context: context.Background(), Source: mustParseURL(t, srcArg),
		RawArg: srcArg, BlobStore: store,
	})
	if err != nil {
		t.Fatalf("CaptureProtocol: %v", err)
	}

	destFake, destArg := startFakeEmpty(t)
	if err := (Plugin{}).RestoreProtocol(cutting_garden_plugins.ProtocolRestoreRequest{
		Context:       context.Background(),
		BlobStore:     store,
		ReceiptDigest: captured.ReceiptDigest,
		Dest:          mustParseURL(t, destArg),
		RawDest:       destArg,
	}); err != nil {
		t.Fatalf("RestoreProtocol: %v", err)
	}

	gotPaths := make([]string, 0, len(destFake.puts))
	for p := range destFake.puts {
		gotPaths = append(gotPaths, p)
	}
	sort.Strings(gotPaths)
	// Resolved under the destination's real calendar href (/dav/cal/), keyed
	// by UID — the component is part of identity but not the filename.
	wantPaths := []string{"/dav/cal/event1.ics", "/dav/cal/task1.ics", "/dav/cal/task2.ics"}
	if strings.Join(gotPaths, ",") != strings.Join(wantPaths, ",") {
		t.Errorf("PUT paths = %v, want %v", gotPaths, wantPaths)
	}
	if body := destFake.puts["/dav/cal/task1.ics"]; !strings.Contains(body, "UID:task1") {
		t.Errorf("restored task1 body = %q, want the task1 VTODO", body)
	}
}

// TestRestoreProtocol_CrossHostRoundTripDiffsClean is the proof that native
// identity is host-independent end to end: capture from server A, restore
// to a DIFFERENT (empty) server B, then diff the original receipt against
// B. The result is clean — even though A and B assign different hrefs —
// because diff correlates by native identity (UID+component+collection),
// not by server path. This is the case that the earlier href-keyed diff
// got wrong.
func TestRestoreProtocol_CrossHostRoundTripDiffsClean(t *testing.T) {
	_, srcArg := startFake(t)
	store := newMemStore(t)

	captured, err := Plugin{}.CaptureProtocol(cutting_garden_plugins.ProtocolCaptureRequest{
		Context: context.Background(), Source: mustParseURL(t, srcArg),
		RawArg: srcArg, BlobStore: store,
	})
	if err != nil {
		t.Fatalf("CaptureProtocol: %v", err)
	}

	_, destArg := startFakeEmpty(t)
	if err := (Plugin{}).RestoreProtocol(cutting_garden_plugins.ProtocolRestoreRequest{
		Context: context.Background(), BlobStore: store,
		ReceiptDigest: captured.ReceiptDigest,
		Dest:          mustParseURL(t, destArg), RawDest: destArg,
	}); err != nil {
		t.Fatalf("RestoreProtocol: %v", err)
	}

	diffs := diffNow(t, destArg, captured.ReceiptDigest, store)
	if len(diffs) != 0 {
		t.Errorf("cross-host restore→diff = %v, want clean (native identity is host-independent)", diffs)
	}
}
