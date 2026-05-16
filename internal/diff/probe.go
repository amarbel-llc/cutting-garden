package diff

import (
	"path/filepath"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/madder/go/pkgs/domain_interfaces"
	"github.com/amarbel-llc/madder/go/pkgs/markl"
)

// blobProber is the narrow surface probeMissingBlobs consumes from
// blob_stores.BlobStoreInitialized. HasBlob takes the
// domain_interfaces.MarklId interface (which *markl.Id implements),
// matching the concrete BlobStoreInitialized's signature exactly so
// it satisfies blobProber structurally. Declared here so unit tests
// can substitute a fake without the full store machinery.
type blobProber interface {
	HasBlob(domain_interfaces.MarklId) bool
}

// probeMissingBlobs is the receipt-vs-store check gated by
// -verify-blobs-exist (FDR §Receipt-vs-store probe). For every file
// entry with a non-empty BlobId, it parses the id and probes the
// source store via HasBlob. Returns a map keyed by the rel-to-<dir>
// materialization path (matching the key compareEntries uses) →
// the missing blob-id string.
//
// Two outcomes produce a missing entry:
//
//   - BlobId parses but the store reports HasBlob == false.
//   - BlobId fails to parse — treated as missing because there is no
//     resolvable address. (The receipt was hand-crafted with a bogus
//     id, or the wire format encodes an id the local markl library
//     can't parse.)
//
// Returns nil error in v1; the signature carries the error for
// symmetry with madder's diff.go (which reserves room for "the
// source store reported it can't be probed" failure modes that may
// surface from remote backends).
func probeMissingBlobs(
	sourceStore blobProber,
	entries []capture_receipt.EntryV1,
) (map[string]string, error) {
	missing := map[string]string{}

	for i := range entries {
		e := entries[i]
		if e.Type != capture_receipt.TypeFile || e.BlobId == "" {
			continue
		}

		key := filepath.ToSlash(filepath.Clean(filepath.Join(e.Root, e.Path)))

		var blobID markl.Id
		if err := blobID.Set(e.BlobId); err != nil {
			missing[key] = e.BlobId
			continue
		}

		if !sourceStore.HasBlob(&blobID) {
			missing[key] = e.BlobId
		}
	}

	return missing, nil
}
