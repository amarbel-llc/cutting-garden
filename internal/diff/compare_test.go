package diff

import (
	"io/fs"
	"reflect"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/capture_receipt"
)

// Helpers — build EntryV1 values without ceremony.

func fileEntry(root, path, blobID string, mode fs.FileMode) capture_receipt.EntryV1 {
	return capture_receipt.EntryV1{
		Path:   path,
		Root:   root,
		Type:   capture_receipt.TypeFile,
		Mode:   mode,
		BlobId: blobID,
	}
}

func dirEntry(root, path string, mode fs.FileMode) capture_receipt.EntryV1 {
	return capture_receipt.EntryV1{
		Path: path,
		Root: root,
		Type: capture_receipt.TypeDir,
		Mode: mode,
	}
}

func symlinkEntry(root, path, target string) capture_receipt.EntryV1 {
	return capture_receipt.EntryV1{
		Path:   path,
		Root:   root,
		Type:   capture_receipt.TypeSymlink,
		Mode:   0o777,
		Target: target,
	}
}

// ---------------------------------------------------------------------
// compareEntries: per-key matrix
// ---------------------------------------------------------------------

func TestCompareEntries_CleanMatch_NoLines(t *testing.T) {
	receipt := []capture_receipt.EntryV1{
		dirEntry(".", ".", 0o755),
		fileEntry(".", "a.txt", "blake2b256-a", 0o644),
	}
	disk := []capture_receipt.EntryV1{
		// Disk-side keys are rel-to-<dir>; single-root receipt
		// already collapsed e.Root to "." so the keys line up.
		dirEntry("", ".", 0o755),
		fileEntry("", "a.txt", "blake2b256-a", 0o644),
	}

	got := compareEntries(receipt, disk, nil)
	if len(got) != 0 {
		t.Errorf("expected no diff lines for clean match, got %v", got)
	}
}

func TestCompareEntries_AddedOnDisk_EmitsALine(t *testing.T) {
	receipt := []capture_receipt.EntryV1{
		dirEntry(".", ".", 0o755),
	}
	disk := []capture_receipt.EntryV1{
		dirEntry("", ".", 0o755),
		fileEntry("", "extra.txt", "blake2b256-e", 0o644),
	}

	got := compareEntries(receipt, disk, nil)
	want := []string{"A  extra.txt\tfile"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCompareEntries_DeletedFromDisk_EmitsDLine(t *testing.T) {
	receipt := []capture_receipt.EntryV1{
		dirEntry(".", ".", 0o755),
		fileEntry(".", "missing.txt", "blake2b256-m", 0o644),
	}
	disk := []capture_receipt.EntryV1{
		dirEntry("", ".", 0o755),
	}

	got := compareEntries(receipt, disk, nil)
	want := []string{"D  missing.txt\tfile"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCompareEntries_TypeChanged_EmitsTLine(t *testing.T) {
	receipt := []capture_receipt.EntryV1{
		fileEntry(".", "x", "blake2b256-x", 0o644),
	}
	disk := []capture_receipt.EntryV1{
		symlinkEntry("", "x", "target"),
	}

	got := compareEntries(receipt, disk, nil)
	want := []string{"T  x\tfile -> symlink"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCompareEntries_BlobModified_EmitsMLine(t *testing.T) {
	receipt := []capture_receipt.EntryV1{
		fileEntry(".", "x", "blake2b256-old", 0o644),
	}
	disk := []capture_receipt.EntryV1{
		fileEntry("", "x", "blake2b256-new", 0o644),
	}

	got := compareEntries(receipt, disk, nil)
	want := []string{"M  x\tblob blake2b256-old -> blake2b256-new"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCompareEntries_ModeModified_EmitsMLine(t *testing.T) {
	receipt := []capture_receipt.EntryV1{
		fileEntry(".", "x", "blake2b256-x", 0o644),
	}
	disk := []capture_receipt.EntryV1{
		fileEntry("", "x", "blake2b256-x", 0o755),
	}

	got := compareEntries(receipt, disk, nil)
	want := []string{"M  x\tmode -rw-r--r-- -> -rwxr-xr-x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCompareEntries_ModeAndBlobBothChanged_EmitsTwoMLines(t *testing.T) {
	receipt := []capture_receipt.EntryV1{
		fileEntry(".", "x", "blake2b256-old", 0o644),
	}
	disk := []capture_receipt.EntryV1{
		fileEntry("", "x", "blake2b256-new", 0o755),
	}

	got := compareEntries(receipt, disk, nil)
	want := []string{
		"M  x\tmode -rw-r--r-- -> -rwxr-xr-x",
		"M  x\tblob blake2b256-old -> blake2b256-new",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCompareEntries_SymlinkTargetChanged_EmitsMLine(t *testing.T) {
	receipt := []capture_receipt.EntryV1{
		symlinkEntry(".", "link", "old-target"),
	}
	disk := []capture_receipt.EntryV1{
		symlinkEntry("", "link", "new-target"),
	}

	got := compareEntries(receipt, disk, nil)
	want := []string{`M  link	target "old-target" -> "new-target"`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCompareEntries_DiskDotFilteredWhenNoReceiptDot(t *testing.T) {
	// FDR §Comparison rules: the on-disk "." is filtered unless the
	// receipt also has a "." key. A schemeless capture of a tree
	// with Root="src" Path="." in the receipt would NOT collapse;
	// here we test the collapsed-single-root case where receipt has
	// no "." key.
	receipt := []capture_receipt.EntryV1{
		fileEntry("src", "x.txt", "blake2b256-x", 0o644),
	}
	disk := []capture_receipt.EntryV1{
		dirEntry("", ".", 0o755), // container dir; should be filtered
		fileEntry("", "src/x.txt", "blake2b256-x", 0o644),
	}

	got := compareEntries(receipt, disk, nil)
	if len(got) != 0 {
		t.Errorf("expected no diff lines (dot filtered + match), got %v", got)
	}
}

func TestCompareEntries_MissingBlobs_EmitsBLine(t *testing.T) {
	// Step 5's -verify-blobs-exist surface: a missingBlobs map keyed
	// by materialization path produces a B line orthogonal to A/D/M/T.
	receipt := []capture_receipt.EntryV1{
		fileEntry(".", "x", "blake2b256-x", 0o644),
	}
	disk := []capture_receipt.EntryV1{
		fileEntry("", "x", "blake2b256-x", 0o644),
	}
	missing := map[string]string{"x": "blake2b256-x"}

	got := compareEntries(receipt, disk, missing)
	want := []string{"B  x\tblob blake2b256-x missing in source store"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCompareEntries_MissingBlobAndDeletedSamePath_BothLines(t *testing.T) {
	// Doubly-broken path: receipt names a file that's missing on
	// disk AND whose blob is also missing in the source store.
	// Emits both D and B lines.
	receipt := []capture_receipt.EntryV1{
		fileEntry(".", "x", "blake2b256-x", 0o644),
	}
	disk := []capture_receipt.EntryV1{
		dirEntry("", ".", 0o755),
	}
	missing := map[string]string{"x": "blake2b256-x"}

	got := compareEntries(receipt, disk, missing)
	want := []string{
		"D  x\tfile",
		"B  x\tblob blake2b256-x missing in source store",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCompareEntries_SortedByPath(t *testing.T) {
	// Insertion-order would be {"z", "a", "m"} but output must
	// be lexicographically sorted.
	receipt := []capture_receipt.EntryV1{
		dirEntry(".", ".", 0o755),
		fileEntry(".", "z.txt", "blake2b256-z", 0o644),
		fileEntry(".", "a.txt", "blake2b256-a", 0o644),
		fileEntry(".", "m.txt", "blake2b256-m", 0o644),
	}
	disk := []capture_receipt.EntryV1{
		dirEntry("", ".", 0o755),
		// All gone — D lines for each.
	}

	got := compareEntries(receipt, disk, nil)
	want := []string{
		"D  a.txt\tfile",
		"D  m.txt\tfile",
		"D  z.txt\tfile",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
