package capture_receipt

import (
	"code.linenisgreat.com/madder/go/pkgs/blob_store_configs"
	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
	"code.linenisgreat.com/madder/go/pkgs/markl_io"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// ComputeStoreHint builds the RFC 0001 store-hint metadata for a
// receipt. Empty storeIdString returns (nil, nil), the MAY-omit path
// §Producer Rules §Receipt Metadata: Store Hint permits when the
// caller can't resolve a real id. A non-nil error is a soft failure
// — callers SHOULD surface it (sink notice on the capture side,
// FDR §Store-Hint Resolution §Hash-family fallback on the restore
// side) and treat the hint as absent.
//
// The hint's ConfigMarklId is computed in the store's default hash
// family so consumers can validate it under the same hash the store
// publishes blobs under.
//
// Phase 3 step 5 moved this here from internal/capture/store_hint.go
// so cutting-garden's restore cmd can run drift checks against the
// same compute path that capture wrote the hint with.
func ComputeStoreHint(
	blobStore blob_stores.BlobStoreInitialized,
	storeIdString string,
) (*StoreHint, error) {
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

	typedCfg := &hyphenceBlob[blob_store_configs.Config]{
		Type: blob_store_configs.TypeStructForConfig(cfg),
		Blob: cfg,
	}

	if _, err := blob_store_configs.Coder.EncodeTo(typedCfg, digester); err != nil {
		return nil, errors.Wrap(err)
	}

	return &StoreHint{
		StoreId:       storeIdString,
		ConfigMarklId: digester.GetMarklId().String(),
	}, nil
}
