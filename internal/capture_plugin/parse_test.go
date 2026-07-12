package capture_plugin

import (
	"bytes"
	"io"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/capture_receipt"
)

func TestParseNode_RoundTripsBuildNode(t *testing.T) {
	refs := []Ref{
		{Alias: "identity", Digest: "sha256-id", TypeString: TypeIdentity},
		{Alias: "payload", Digest: "sha256-pay", TypeString: "jcs-git-capture-payload-v1"},
	}
	body := []byte(`{"object_count":3,"tip":"abc"}`)

	// Metadata-only node.
	meta := BuildNode(ReceiptType("git"), refs, nil)
	got, err := ParseNode(bytes.NewReader(meta))
	if err != nil {
		t.Fatalf("ParseNode(meta): %v", err)
	}
	if got.Type != "cutting_garden-capture-receipt-git-v1" {
		t.Errorf("type = %q", got.Type)
	}
	if len(got.Refs) != 2 || got.Refs[0].Alias != "identity" || got.Refs[1].Digest != "sha256-pay" {
		t.Errorf("refs round-trip wrong: %+v", got.Refs)
	}
	if got.Body != nil {
		t.Errorf("metadata-only node should have nil body, got %q", got.Body)
	}
	if r, ok := got.RefByAlias("payload"); !ok || r.TypeString != "jcs-git-capture-payload-v1" {
		t.Errorf("RefByAlias(payload) = %+v ok=%v", r, ok)
	}

	// Bodied node.
	bodied := BuildNode("jcs-git-capture-payload-v1", refs[1:], body)
	gotB, err := ParseNode(bytes.NewReader(bodied))
	if err != nil {
		t.Fatalf("ParseNode(bodied): %v", err)
	}
	if string(bytes.TrimSpace(gotB.Body)) != string(body) {
		t.Errorf("body round-trip = %q, want %q", gotB.Body, body)
	}
}

func TestParseNodeHeader_BodyMatchesParseNode(t *testing.T) {
	refs := []Ref{
		{Alias: "payload", Digest: "sha256-pay", TypeString: "jcs-git-capture-payload-v1"},
	}
	cases := map[string][]byte{
		"nil body":            nil,
		"json body":           []byte(`{"object_count":3,"tip":"abc"}`),
		"no trailing newline": []byte("no-newline-here"),
		// Embedded newline + non-text bytes + no trailing newline: the
		// stream path must reproduce these byte-for-byte, not line-split.
		"binary body": {0x00, 0x01, 0x02, 0xff, '\n', 'x'},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			blob := BuildNode("jcs-git-capture-payload-v1", refs, body)

			want, err := ParseNode(bytes.NewReader(blob))
			if err != nil {
				t.Fatalf("ParseNode: %v", err)
			}

			gotNode, bodyReader, err := ParseNodeHeader(bytes.NewReader(blob))
			if err != nil {
				t.Fatalf("ParseNodeHeader: %v", err)
			}
			gotBody, err := io.ReadAll(bodyReader)
			if err != nil {
				t.Fatalf("read streamed body: %v", err)
			}

			if gotNode.Type != want.Type {
				t.Errorf("type = %q, want %q", gotNode.Type, want.Type)
			}
			if len(gotNode.Refs) != len(want.Refs) {
				t.Errorf("refs len = %d, want %d", len(gotNode.Refs), len(want.Refs))
			}
			// The streamed body must equal ParseNode's Body byte-for-byte.
			if !bytes.Equal(gotBody, want.Body) {
				t.Errorf("streamed body = %q, want ParseNode body %q", gotBody, want.Body)
			}
		})
	}
}

func TestParseNode_DropsOptionalSig(t *testing.T) {
	// A sig-bearing reference (`!type@sig`) should parse with the sig
	// stripped from the type-string.
	node := "---\n- payload < @sha256-x !jcs-git-capture-payload-v1@sha256-sig\n! cutting_garden-capture-receipt-git-v1\n---\n"
	got, err := ParseNode(bytes.NewReader([]byte(node)))
	if err != nil {
		t.Fatalf("ParseNode: %v", err)
	}
	r, ok := got.RefByAlias("payload")
	if !ok || r.TypeString != "jcs-git-capture-payload-v1" {
		t.Errorf("ref = %+v ok=%v, want type without @sig", r, ok)
	}
}

func TestKindFromReceiptType(t *testing.T) {
	cases := []struct {
		in       string
		wantKind string
		wantOK   bool
	}{
		// Legacy hyphen prefix (frozen git/web receipts) — read-compat:
		// these must keep dispatching after the #112 convergence.
		{"cutting_garden-capture-receipt-git-v1", "git", true},
		{"cutting_garden-capture-receipt-web-v1", "web", true},
		// Converged underscore prefix (#112) — new protocol families.
		{"cutting_garden-capture_receipt-caldav-v1", "caldav", true},
		{"cutting_garden-capture_receipt-git-v2", "git", true},
		// The flat fs tag shares the underscore prefix but is NOT a
		// protocol receipt — it must NOT match (the discriminator the
		// orchestrator relies on to route flat vs protocol receipts).
		{"cutting_garden-capture_receipt-fs-v1", "", false},
		{"jcs-git-capture-payload-v1", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		kind, ok := KindFromReceiptType(tc.in)
		if ok != tc.wantOK || kind != tc.wantKind {
			t.Errorf("KindFromReceiptType(%q) = (%q, %v), want (%q, %v)",
				tc.in, kind, ok, tc.wantKind, tc.wantOK)
		}
	}
}

func TestReceiptType_PrefixByKind(t *testing.T) {
	cases := []struct {
		kind string
		want string
	}{
		// Frozen pre-#112 kinds keep the legacy hyphen prefix so their
		// immutable receipts stay byte-identical.
		{"git", "cutting_garden-capture-receipt-git-v1"},
		{"web", "cutting_garden-capture-receipt-web-v1"},
		// New kinds get the converged underscore prefix (#112).
		{"caldav", "cutting_garden-capture_receipt-caldav-v1"},
	}
	for _, tc := range cases {
		if got := ReceiptType(tc.kind); got != tc.want {
			t.Errorf("ReceiptType(%q) = %q, want %q", tc.kind, got, tc.want)
		}
	}

	// Round-trip: every kind ReceiptType emits must parse back to that
	// kind via KindFromReceiptType, regardless of which prefix it used.
	for _, kind := range []string{"git", "web", "caldav"} {
		ts := ReceiptType(kind)
		got, ok := KindFromReceiptType(ts)
		if !ok || got != kind {
			t.Errorf("round-trip %q: KindFromReceiptType(%q) = (%q, %v)",
				kind, ts, got, ok)
		}
	}
}

// TestFlatFSTag_MatchesCaptureReceipt pins the local flatFSTag literal to
// the canonical flat tag defined in internal/capture_receipt. The literal
// is duplicated (not imported) in production code to avoid a dependency
// cycle; this test keeps the two from drifting.
func TestFlatFSTag_MatchesCaptureReceipt(t *testing.T) {
	if flatFSTag != capture_receipt.TypeTagV1 {
		t.Errorf("flatFSTag = %q, want capture_receipt.TypeTagV1 = %q",
			flatFSTag, capture_receipt.TypeTagV1)
	}
}
