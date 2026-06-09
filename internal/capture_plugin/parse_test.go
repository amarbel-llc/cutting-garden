package capture_plugin

import (
	"bytes"
	"io"
	"testing"
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
		{"cutting_garden-capture-receipt-git-v1", "git", true},
		{"cutting_garden-capture-receipt-web-v1", "web", true},
		// Underscored legacy fs tag must NOT match (discriminator).
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
