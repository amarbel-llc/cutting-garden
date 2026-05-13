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
// receipt. Empty storeIdString returns (nil, nil), the MAY-omit path
// §Producer Rules §Receipt Metadata: Store Hint permits when the
// caller can't resolve a real id. A non-nil error is a soft failure
// — callers SHOULD surface it (sink notice) and write the receipt
// without the hint.
//
// The hint's ConfigMarklId is computed in the store's default hash
// family so consumers can validate it under the same hash the store
// publishes blobs under.
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

	hash, repoolHash := hashFormat.GetHash()
	defer repoolHash()

	digester, repoolDigester := markl_io.MakeWriterWithRepool(hash, nil)
	defer repoolDigester()

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
