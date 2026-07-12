package diff

import (
	"fmt"
	"path/filepath"
	"sort"

	"code.linenisgreat.com/cutting-garden/internal/capture_receipt"
)

// compareEntries computes the per-path symmetric difference between
// receipt entries and on-disk entries, per FDR §Comparison rules.
//
// The key for both sides is the rel-to-<dir> materialization path:
//
//   - Receipt entry: filepath.ToSlash(filepath.Clean(filepath.Join(e.Root, e.Path)))
//   - Disk entry:    filepath.ToSlash(filepath.Clean(e.Path))
//     (walkForDiff already records Path as rel-to-<dir>)
//
// missingBlobs is the receipt-vs-store probe result (step 5); it is
// keyed identically and may be nil when -verify-blobs-exist was not
// set. Each entry produces a `B` line orthogonal to A/D/M/T. A path
// may produce BOTH e.g. an `M ... blob` (drifted content) AND a
// `B ... blob` (referenced blob missing in store); the two describe
// distinct failures.
//
// Output lines are sorted lexicographically by key with each path's
// per-marker lines emitted in compareEntries's natural insertion
// order (A/D/T as a single line; M may be 1-2 lines; B at most 1).
// Tests rely on the per-key contiguity.
func compareEntries(
	receipt []capture_receipt.EntryV1,
	disk []capture_receipt.EntryV1,
	missingBlobs map[string]string,
) []string {
	receiptByPath := make(map[string]capture_receipt.EntryV1, len(receipt))
	for _, e := range receipt {
		key := filepath.ToSlash(filepath.Clean(filepath.Join(e.Root, e.Path)))
		receiptByPath[key] = e
	}

	diskByPath := make(map[string]capture_receipt.EntryV1, len(disk))
	for _, e := range disk {
		key := filepath.ToSlash(filepath.Clean(e.Path))
		diskByPath[key] = e
	}

	// The on-disk "." entry is the dir argument itself — the container
	// in which the receipt's tree materializes, not part of the
	// receipt unless the receipt was captured with Root="." and
	// Path=".". When the receipt has no "." key, the dir's own mode
	// is conceptually outside the comparison and reporting it as
	// `A  .` is noise.
	if _, ok := receiptByPath["."]; !ok {
		delete(diskByPath, ".")
	}

	allKeys := make(map[string]struct{}, len(receiptByPath)+len(diskByPath))
	for k := range receiptByPath {
		allKeys[k] = struct{}{}
	}
	for k := range diskByPath {
		allKeys[k] = struct{}{}
	}

	keys := make([]string, 0, len(allKeys))
	for k := range allKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var lines []string
	for _, k := range keys {
		recv, inReceipt := receiptByPath[k]
		dsk, onDisk := diskByPath[k]

		switch {
		case inReceipt && !onDisk:
			lines = append(lines, fmt.Sprintf("D  %s\t%s", k, recv.Type))
		case onDisk && !inReceipt:
			lines = append(lines, fmt.Sprintf("A  %s\t%s", k, dsk.Type))
		case recv.Type != dsk.Type:
			lines = append(lines, fmt.Sprintf("T  %s\t%s -> %s",
				k, recv.Type, dsk.Type))
		default:
			lines = append(lines, perTypeDiffs(k, recv, dsk)...)
		}

		if missingID, missing := missingBlobs[k]; missing {
			lines = append(lines, fmt.Sprintf(
				"B  %s\tblob %s missing in source store",
				k, missingID,
			))
		}
	}

	return lines
}

// perTypeDiffs emits zero or more lines comparing the type-specific
// fields of two entries known to share the same type. Diff is
// reported per-attribute so a single path can produce multiple lines
// (e.g. mode AND blob differ → two lines).
func perTypeDiffs(
	path string,
	recv, dsk capture_receipt.EntryV1,
) []string {
	var out []string

	switch recv.Type {
	case capture_receipt.TypeFile:
		if recv.Mode.Perm() != dsk.Mode.Perm() {
			out = append(out, fmt.Sprintf("M  %s\tmode %s -> %s",
				path, recv.Mode.Perm(), dsk.Mode.Perm()))
		}
		if recv.BlobId != dsk.BlobId {
			out = append(out, fmt.Sprintf("M  %s\tblob %s -> %s",
				path, recv.BlobId, dsk.BlobId))
		}

	case capture_receipt.TypeDir:
		if recv.Mode.Perm() != dsk.Mode.Perm() {
			out = append(out, fmt.Sprintf("M  %s\tmode %s -> %s",
				path, recv.Mode.Perm(), dsk.Mode.Perm()))
		}

	case capture_receipt.TypeSymlink:
		if recv.Target != dsk.Target {
			out = append(out, fmt.Sprintf("M  %s\ttarget %q -> %q",
				path, recv.Target, dsk.Target))
		}

	case capture_receipt.TypeOther:
		// Capture is lossy here (FDR 0001 §Limitations): the receipt
		// records "other" with no comparable content, and the disk
		// walk likewise can't classify it. Skip — emitting nothing
		// is the honest answer.
	}

	return out
}
