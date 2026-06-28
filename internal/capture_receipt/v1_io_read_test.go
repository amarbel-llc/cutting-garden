package capture_receipt

import (
	"bytes"
	"strings"
	"testing"
)

func TestCoder_DecodeFrom_RoundTripNoHint(t *testing.T) {
	input := []EntryV1{
		{Path: "a.txt", Root: "src", Type: TypeFile, Mode: 0o644, Size: 10, BlobId: "blake2b256-x"},
		{Path: ".", Root: "src", Type: TypeDir, Mode: 0o755},
		{Path: "link", Root: "src", Type: TypeSymlink, Mode: 0o777, Target: "../bar"},
	}
	wantSorted := []EntryV1{
		{Path: ".", Root: "src", Type: TypeDir, Mode: 0o755},
		{Path: "a.txt", Root: "src", Type: TypeFile, Mode: 0o644, Size: 10, BlobId: "blake2b256-x"},
		{Path: "link", Root: "src", Type: TypeSymlink, Mode: 0o777, Target: "../bar"},
	}

	var buf bytes.Buffer
	if _, err := WriteV1(&buf, append([]EntryV1{}, input...)); err != nil {
		t.Fatalf("WriteV1: %v", err)
	}

	tb := &hyphenceBlob[Blob]{}
	if _, err := Coder.DecodeFrom(tb, &buf); err != nil {
		t.Fatalf("Coder.DecodeFrom: %v", err)
	}

	if tb.Type != TypeStructV1 {
		t.Errorf("Type: got %v want %v", tb.Type, TypeStructV1)
	}

	v1, ok := tb.Blob.(*V1)
	if !ok {
		t.Fatalf("Blob: got %T want *V1", tb.Blob)
	}

	if v1.Hint != nil {
		t.Errorf("expected nil Hint, got %+v", v1.Hint)
	}

	if len(v1.Entries) != len(wantSorted) {
		t.Fatalf("entries len: got %d want %d", len(v1.Entries), len(wantSorted))
	}
	for i := range wantSorted {
		if v1.Entries[i] != wantSorted[i] {
			t.Errorf("entries[%d]:\n  got:  %+v\n  want: %+v", i, v1.Entries[i], wantSorted[i])
		}
	}
}

func TestCoder_DecodeFrom_RoundTripWithHint(t *testing.T) {
	hint := &StoreHint{
		StoreId:       ".work",
		ConfigMarklId: "blake2b256-9ft3m74l5t2ppwjrvfg3wp380jqj2zfrm6zevxqx34sdethvey0s5vm9gd",
	}
	entries := []EntryV1{
		{Path: "a.txt", Root: "src", Type: TypeFile, Mode: 0o644, Size: 10, BlobId: "blake2b256-x"},
	}

	var buf bytes.Buffer
	if _, err := WriteV1WithHint(&buf, append([]EntryV1{}, entries...), hint); err != nil {
		t.Fatalf("WriteV1WithHint: %v", err)
	}

	tb := &hyphenceBlob[Blob]{}
	if _, err := Coder.DecodeFrom(tb, &buf); err != nil {
		t.Fatalf("Coder.DecodeFrom: %v", err)
	}

	v1, ok := tb.Blob.(*V1)
	if !ok {
		t.Fatalf("Blob: got %T want *V1", tb.Blob)
	}

	if v1.Hint == nil {
		t.Fatalf("expected non-nil Hint, got nil")
	}
	if v1.Hint.StoreId != hint.StoreId {
		t.Errorf("Hint.StoreId: got %q want %q", v1.Hint.StoreId, hint.StoreId)
	}
	if v1.Hint.ConfigMarklId != hint.ConfigMarklId {
		t.Errorf("Hint.ConfigMarklId: got %q want %q", v1.Hint.ConfigMarklId, hint.ConfigMarklId)
	}
}

func TestCoder_DecodeFrom_RejectsUnknownTypeTag(t *testing.T) {
	receipt := "---\n! cutting_garden-capture_receipt-fs-v999\n---\n\n"

	tb := &hyphenceBlob[Blob]{}
	if _, err := Coder.DecodeFrom(tb, strings.NewReader(receipt)); err == nil {
		t.Fatal("expected error for unknown type-tag, got nil")
	}
}

func TestCoder_DecodeFrom_TolerantOfUnknownDashKey(t *testing.T) {
	// Hyphence(7) tolerates unknown metadata keys. A `- future-key …`
	// line whose value does NOT start with `store/` is silently
	// ignored by our metadata coder.
	receipt := "---\n" +
		"- store/.work < blake2b256-x\n" +
		"- future-key value\n" +
		"! " + TypeTagV1 + "\n" +
		"---\n\n" +
		`{"path":"a","root":".","type":"file","mode":"0644","size":1,"blob_id":"x"}` + "\n"

	tb := &hyphenceBlob[Blob]{}
	if _, err := Coder.DecodeFrom(tb, strings.NewReader(receipt)); err != nil {
		t.Fatalf("Coder.DecodeFrom: %v", err)
	}

	v1, ok := tb.Blob.(*V1)
	if !ok {
		t.Fatalf("Blob: got %T want *V1", tb.Blob)
	}

	if v1.Hint == nil || v1.Hint.StoreId != ".work" {
		t.Errorf("expected hint .work, got %+v", v1.Hint)
	}
}
