package diff

import (
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/capture_receipt"
	"code.linenisgreat.com/madder/go/pkgs/domain_interfaces"
)

// fakeProber answers HasBlob from a set of known-present id strings.
// Each test arranges the set, then asserts which keys land in the
// returned missing-blobs map. Signature matches blobProber (and the
// concrete BlobStoreInitialized) exactly: HasBlob takes the
// domain_interfaces.MarklId interface.
type fakeProber struct {
	present map[string]bool
}

func (f fakeProber) HasBlob(id domain_interfaces.MarklId) bool {
	return f.present[id.String()]
}

func TestProbeMissingBlobs_AllPresent_EmptyMap(t *testing.T) {
	// Use real blob-ids the markl decoder can parse. blake2b256 has a
	// 32-byte payload (52 blech32 chars after the prefix).
	const blobID = "blake2b256-9ft3m74l5t2ppwjrvfg3wp380jqj2zfrm6zevxqx34sdethvey0s5vm9gd"

	entries := []capture_receipt.EntryV1{
		{Type: capture_receipt.TypeFile, Root: ".", Path: "x", BlobId: blobID},
	}
	prober := fakeProber{present: map[string]bool{blobID: true}}

	missing, err := probeMissingBlobs(prober, entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("expected empty map, got %v", missing)
	}
}

func TestProbeMissingBlobs_BlobAbsent_MapEntry(t *testing.T) {
	const blobID = "blake2b256-9ft3m74l5t2ppwjrvfg3wp380jqj2zfrm6zevxqx34sdethvey0s5vm9gd"

	entries := []capture_receipt.EntryV1{
		{Type: capture_receipt.TypeFile, Root: ".", Path: "x", BlobId: blobID},
	}
	prober := fakeProber{present: nil}

	missing, err := probeMissingBlobs(prober, entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := len(missing), 1; got != want {
		t.Fatalf("missing entries: got %d, want %d", got, want)
	}
	if got, want := missing["x"], blobID; got != want {
		t.Errorf("missing[\"x\"]: got %q, want %q", got, want)
	}
}

func TestProbeMissingBlobs_MalformedID_TreatedAsMissing(t *testing.T) {
	// A receipt could carry an unparseable blob-id (hand-crafted or
	// produced by a future hash family the local markl library can't
	// decode). probeMissingBlobs treats unparseable as missing —
	// there's no resolvable address.
	entries := []capture_receipt.EntryV1{
		{Type: capture_receipt.TypeFile, Root: ".", Path: "x", BlobId: "bogus"},
	}
	// Even if the prober would say "present" for a parseable id, an
	// unparseable id never reaches HasBlob — it short-circuits to
	// "missing".
	prober := fakeProber{present: nil}

	missing, err := probeMissingBlobs(prober, entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := missing["x"], "bogus"; got != want {
		t.Errorf("missing[\"x\"]: got %q, want %q", got, want)
	}
}

func TestProbeMissingBlobs_SkipsNonFileEntries(t *testing.T) {
	// Dirs, symlinks, and "other" entries have no BlobId to probe.
	// Files with empty BlobId (degenerate; capture wouldn't emit
	// them) are also skipped.
	entries := []capture_receipt.EntryV1{
		{Type: capture_receipt.TypeDir, Root: ".", Path: "."},
		{Type: capture_receipt.TypeSymlink, Root: ".", Path: "link", Target: "x"},
		{Type: capture_receipt.TypeOther, Root: ".", Path: "fifo"},
		{Type: capture_receipt.TypeFile, Root: ".", Path: "empty-blob", BlobId: ""},
	}
	prober := fakeProber{present: nil}

	missing, err := probeMissingBlobs(prober, entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("expected no missing entries, got %v", missing)
	}
}

func TestProbeMissingBlobs_KeyShapeMatchesCompareEntries(t *testing.T) {
	// The map key must match compareEntries's receipt-side key shape
	// so the B line lands on the same path. Multi-root case: the
	// key is filepath.ToSlash(filepath.Clean(Join(Root, Path))).
	const blobID = "blake2b256-9ft3m74l5t2ppwjrvfg3wp380jqj2zfrm6zevxqx34sdethvey0s5vm9gd"

	entries := []capture_receipt.EntryV1{
		{Type: capture_receipt.TypeFile, Root: "src", Path: "sub/x.txt", BlobId: blobID},
	}
	prober := fakeProber{present: nil}

	missing, err := probeMissingBlobs(prober, entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := missing["src/sub/x.txt"]; !ok {
		t.Errorf("expected missing key \"src/sub/x.txt\", got keys %v", keysOf(missing))
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
