package caldav

import (
	"context"
	"sort"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/capture_plugin"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
)

func TestCaptureProtocol_EmitsCaldavReceiptTree(t *testing.T) {
	_, arg := startFake(t)
	store := newMemStore(t)

	res, err := Plugin{}.CaptureProtocol(cutting_garden_plugins.ProtocolCaptureRequest{
		Context:   context.Background(),
		Source:    mustParseURL(t, arg),
		RawArg:    arg,
		BlobStore: store,
	})
	if err != nil {
		t.Fatalf("CaptureProtocol: %v", err)
	}

	if res.ObjectCount != 3 {
		t.Errorf("ObjectCount = %d, want 3", res.ObjectCount)
	}
	if res.ReceiptDigest == "" {
		t.Fatal("empty ReceiptDigest")
	}

	// The receipt dispatches as a caldav protocol receipt on the converged
	// underscore prefix (#112 / RFC 0011).
	receipt, err := capture_plugin.ReadNode(store, res.ReceiptDigest)
	if err != nil {
		t.Fatalf("ReadNode(receipt): %v", err)
	}
	if receipt.Type != "cutting_garden-capture_receipt-caldav-v1" {
		t.Errorf("receipt type = %q, want cutting_garden-capture_receipt-caldav-v1", receipt.Type)
	}
	if kind, ok := capture_plugin.KindFromReceiptType(receipt.Type); !ok || kind != "caldav" {
		t.Errorf("KindFromReceiptType = (%q, %v), want (caldav, true)", kind, ok)
	}

	// The payload node references each object by native identity
	// <collection>/<component>/<UID>, each typed by its component
	// (caldav-object-<kind>-v1) — the receipt carries the same per-component
	// types traversal emits.
	payload := payloadOf(t, store, receipt)
	gotIDs := make([]string, 0, len(payload.Refs))
	for _, r := range payload.Refs {
		gotIDs = append(gotIDs, r.Alias)
		parts := strings.Split(r.Alias, "/")
		var want string
		if len(parts) == 3 {
			want = objectType(parts[1])
		}
		if want == "" || r.TypeString != want {
			t.Errorf("object ref %q: type = %q, want %q", r.Alias, r.TypeString, want)
		}
	}
	wantIDs := []string{
		"cal/VEVENT/event1",
		"cal/VTODO/task1",
		"cal/VTODO/task2",
	}
	sort.Strings(gotIDs)
	if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
		t.Errorf("object identities = %v, want %v", gotIDs, wantIDs)
	}
}

// TestCaptureProtocol_PayloadIdentityStable proves the payload node is a
// content fingerprint: two captures of the same calendar state yield a
// byte-identical payload (same digest), regardless of run. (The receipt
// itself carries a per-run outcome datetime, so only the payload — pure
// content — is identity-stable; the same split git's invariance test
// relies on.)
func TestCaptureProtocol_PayloadIdentityStable(t *testing.T) {
	_, arg := startFake(t)

	firstStore := newMemStore(t)
	first, err := Plugin{}.CaptureProtocol(cutting_garden_plugins.ProtocolCaptureRequest{
		Context: context.Background(), Source: mustParseURL(t, arg),
		RawArg: arg, BlobStore: firstStore,
	})
	if err != nil {
		t.Fatalf("first CaptureProtocol: %v", err)
	}

	secondStore := newMemStore(t)
	second, err := Plugin{}.CaptureProtocol(cutting_garden_plugins.ProtocolCaptureRequest{
		Context: context.Background(), Source: mustParseURL(t, arg),
		RawArg: arg, BlobStore: secondStore,
	})
	if err != nil {
		t.Fatalf("second CaptureProtocol: %v", err)
	}

	firstPayload := payloadRefDigest(t, firstStore, first.ReceiptDigest)
	secondPayload := payloadRefDigest(t, secondStore, second.ReceiptDigest)
	if firstPayload != secondPayload {
		t.Errorf("payload digest not stable across captures:\n  first:  %s\n  second: %s",
			firstPayload, secondPayload)
	}
}

// TestCaptureProtocol_ReporterDoesNotAffectIdentity pins that the Reporter
// is pure observability: a capture with a (non-nil) Reporter produces the
// same payload digest as one without.
func TestCaptureProtocol_ReporterDoesNotAffectIdentity(t *testing.T) {
	_, arg := startFake(t)

	plainStore := newMemStore(t)
	plain, err := Plugin{}.CaptureProtocol(cutting_garden_plugins.ProtocolCaptureRequest{
		Context: context.Background(), Source: mustParseURL(t, arg),
		RawArg: arg, BlobStore: plainStore,
	})
	if err != nil {
		t.Fatalf("plain CaptureProtocol: %v", err)
	}

	reportedStore := newMemStore(t)
	reported, err := Plugin{}.CaptureProtocol(cutting_garden_plugins.ProtocolCaptureRequest{
		Context: context.Background(), Source: mustParseURL(t, arg),
		RawArg: arg, BlobStore: reportedStore,
		Reporter: cutting_garden_plugins.NopReporter{},
	})
	if err != nil {
		t.Fatalf("reported CaptureProtocol: %v", err)
	}

	if a, b := payloadRefDigest(t, plainStore, plain.ReceiptDigest),
		payloadRefDigest(t, reportedStore, reported.ReceiptDigest); a != b {
		t.Errorf("Reporter changed payload identity:\n  plain:    %s\n  reported: %s", a, b)
	}
}

// payloadOf follows a receipt's payload reference and reads that node.
func payloadOf(
	t *testing.T,
	store blob_stores.BlobStoreInitialized,
	receipt capture_plugin.Node,
) capture_plugin.Node {
	t.Helper()
	ref, ok := receipt.RefByAlias("payload")
	if !ok {
		t.Fatal("receipt has no payload reference")
	}
	payload, err := capture_plugin.ReadNode(store, ref.Digest)
	if err != nil {
		t.Fatalf("ReadNode(payload): %v", err)
	}
	return payload
}

// payloadRefDigest reads a receipt and returns its payload reference's
// digest — the content fingerprint of the captured object set.
func payloadRefDigest(
	t *testing.T,
	store blob_stores.BlobStoreInitialized,
	receiptDigest string,
) string {
	t.Helper()
	receipt, err := capture_plugin.ReadNode(store, receiptDigest)
	if err != nil {
		t.Fatalf("ReadNode(receipt): %v", err)
	}
	ref, ok := receipt.RefByAlias("payload")
	if !ok {
		t.Fatal("receipt has no payload reference")
	}
	return ref.Digest
}
