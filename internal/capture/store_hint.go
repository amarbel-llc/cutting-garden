package capture

import (
	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/madder/go/pkgs/blob_store_configs"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/madder/go/pkgs/hyphence"
	"github.com/amarbel-llc/madder/go/pkgs/markl_io"
	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/errors"
)

// computeStoreHint builds the RFC 0003 store-hint metadata for a
// receipt. storeIdString is the resolved id of the destination store
// (the default-store case is resolved to its actual id by the caller,
// not bypassed here). An empty string is the "still couldn't determine
// an id" sentinel — returns (nil, nil), the MAY-omit path RFC 0003
// §Producer Rules §Receipt Metadata: Store Hint permits.
//
// Returns a non-nil error when the hint should have been computable
// but failed; callers MAY treat that as a soft failure (sink notice).
//
// The hint pairs storeIdString with the markl-id of the store's
// blob_store-config blob, computed in the store's default hash family
// so consumers can validate it under the same hash the store publishes
// blobs under.
func computeStoreHint(
	blobStore blob_stores.BlobStoreInitialized,
	storeIdString string,
) (*capture_receipt.StoreHint, error) {
	if storeIdString == "" {
		return nil, nil
	}

	cfg := blobStore.BlobStore.GetBlobStoreConfig()
	if cfg == nil {
		return nil, nil
	}

	hashFormat := blobStore.BlobStore.GetDefaultHashType()
	if hashFormat == nil {
		return nil, nil
	}

	hash, _ := hashFormat.GetHash() //repool:owned
	digester := markl_io.MakeWriter(hash, nil)

	typedCfg := &hyphence.TypedBlob[blob_store_configs.Config]{
		Type: blob_store_configs.TypeStructForConfig(cfg),
		Blob: cfg,
	}

	if _, err := blob_store_configs.Coder.EncodeTo(typedCfg, digester); err != nil {
		return nil, errors.Wrap(err)
	}

	return &capture_receipt.StoreHint{
		StoreId:       storeIdString,
		ConfigMarklId: digester.GetMarklId().String(),
	}, nil
}
