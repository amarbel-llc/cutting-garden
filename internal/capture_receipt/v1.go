package capture_receipt

import "io/fs"

// EntryV1 is one filesystem entry in a cutting_garden-capture_receipt-fs-v1
// receipt body.
//
// Mode carries the lstat permission bits (only Mode.Perm() is rendered;
// type bits are dropped). Size and BlobId apply to regular files;
// Target applies to symlinks. The Root/Path split lets a single receipt
// describe entries from multiple capture-roots without path collisions.
type EntryV1 struct {
	Path   string
	Root   string
	Type   string
	Mode   fs.FileMode
	Size   int64
	BlobId string
	Target string
}

// V1 is the parsed shape of a cutting_garden-capture_receipt-fs-v1 blob.
// Hint is non-nil iff the receipt's hyphence metadata block carried a
// store-hint line.
type V1 struct {
	Hint    *StoreHint
	Entries []EntryV1
}

// EntryType categorizes a v1 filesystem entry. The string values are
// the canonical wire tags written into the receipt's "type" field.
const (
	TypeFile    = "file"
	TypeDir     = "dir"
	TypeSymlink = "symlink"
	TypeOther   = "other"
)

// StoreHint is the optional `- store/<id> < <markl-id>` metadata line
// per RFC 0003 §Producer Rules §Receipt Metadata: Store Hint.
// Consumers (restore) use it to auto-resolve the source store.
//
// The hint is wire-shape-shared across receipt versions: a v2 receipt
// would carry the same hint structure even if its Entries change.
type StoreHint struct {
	StoreId       string
	ConfigMarklId string
}
