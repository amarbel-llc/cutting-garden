package restore

import (
	"net/url"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/blob_store_env"
	"github.com/amarbel-llc/madder/go/pkgs/blob_store_id"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/madder/go/pkgs/ids"
	"github.com/amarbel-llc/madder/go/pkgs/markl"
	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/errors"
)

// resolveRestorePlugin parses destStr as a URL and looks up the
// restore plugin registered for its scheme. Schemeless dests resolve
// to the file plugin's `""` registration.
func resolveRestorePlugin(
	destStr string,
) (*url.URL, cutting_garden_plugins.RestorePlugin, error) {
	u, err := url.Parse(destStr)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "parse dest %q", destStr)
	}
	plugin, err := cutting_garden_plugins.ResolveRestore(u.Scheme)
	if err != nil {
		return nil, nil, err
	}
	return u, plugin, nil
}

// readReceiptBlob fetches and parses the receipt blob.
//
// With storeOverride non-empty: resolve that store, read directly.
// With storeOverride empty: walk GetBlobStoresSorted in deterministic
// order, probing HasBlob until a store carrying the receipt is found.
// The deterministic order ensures two stores holding receipts with
// colliding ids resolve the same way every time.
//
// Phase 3 step 3 uses this shape for the receipt blob ITSELF. Step 5
// adds the FDR §Store-Hint Resolution decision tree for selecting the
// materialization store (the per-entry blob source).
func readReceiptBlob(
	envBlobStore blob_store_env.BlobStoreEnv,
	receiptID *markl.Id,
	storeOverride string,
) (capture_receipt.Blob, ids.TypeStruct, error) {
	if storeOverride != "" {
		store, err := resolveStoreByID(envBlobStore, storeOverride)
		if err != nil {
			return nil, ids.TypeStruct{}, err
		}
		blob, tt, err := capture_receipt.Read(store, receiptID)
		if err != nil {
			return nil, tt, errors.Wrapf(err, "read receipt %s", receiptID)
		}
		return blob, tt, nil
	}

	for _, store := range envBlobStore.GetBlobStoresSorted() {
		if !store.HasBlob(receiptID) {
			continue
		}
		blob, tt, err := capture_receipt.Read(store, receiptID)
		if err != nil {
			return nil, tt, errors.Wrapf(err, "read receipt %s", receiptID)
		}
		return blob, tt, nil
	}

	return nil, ids.TypeStruct{}, errors.ErrorWithStackf(
		"receipt %s not found in any configured store", receiptID)
}

// resolveStoreByID parses idStr as a blob-store-id and looks up the
// corresponding configured store. Returns an error if idStr is
// malformed or the store is not configured locally.
//
// Takes the materializationEnv interface (defined in store_resolve.go)
// rather than blob_store_env.BlobStoreEnv directly so tests can pass
// fakes — the concrete type satisfies the interface structurally.
func resolveStoreByID(
	env materializationEnv,
	idStr string,
) (blob_stores.BlobStoreInitialized, error) {
	var id blob_store_id.Id
	if err := id.Set(idStr); err != nil {
		return blob_stores.BlobStoreInitialized{}, errors.Wrapf(
			err, "parse -store value %q", idStr)
	}
	stores := env.GetBlobStores()
	store, ok := stores[id.String()]
	if !ok {
		return blob_stores.BlobStoreInitialized{}, errors.ErrorWithStackf(
			"-store %q is not a configured blob store", idStr)
	}
	return store, nil
}
