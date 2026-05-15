package restore

import (
	"fmt"
	"io"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/madder/go/pkgs/blob_store_id"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/errors"
)

// materializationEnv is the narrow surface
// resolveMaterializationStore (and resolveStoreByID) consume from
// blob_store_env.BlobStoreEnv. Declared here so unit tests can supply
// a fake: the concrete BlobStoreEnv panics in GetDefaultBlobStore
// when no stores are initialized, which blocks byte-exact diagnostic
// assertions for the no-store-needed FDR branches. The concrete
// BlobStoreEnv satisfies this interface structurally — type-alias
// chains inherit every method.
type materializationEnv interface {
	GetDefaultBlobStore() blob_stores.BlobStoreInitialized
	GetBlobStores() map[string]blob_stores.BlobStoreInitialized
}

// resolveMaterializationStore implements FDR §Store-Hint Resolution.
// Returns the store entry blobs are read against, plus any diagnostic
// writes to `diagnostics`. Five branches:
//
//  1. `-store` flag wins. No diagnostic.
//  2. Hint present, store configured, config-hash matches → use the
//     hinted store, no diagnostic.
//  3. Hint present, store configured, config-hash differs → refuse
//     with a `warning:` + payload, return an error naming the
//     -store override.
//  4. Hint present, store NOT configured locally → fall back to the
//     active default with two `notice:` lines.
//  5. No hint → fall back to the active default with two `notice:`
//     lines.
//
// FDR §Limitations §Hash-family mismatch carves out a sub-branch of
// (2/3): if ComputeStoreHint returns a compute error or nil, the
// caller widens (3) into (4) — fall back with a hash-mismatch notice
// rather than refusing on a comparison that cannot meaningfully be
// performed.
//
// FDR §Store-hint resolution does NOT specify behavior for a
// malformed hint store-id. Madder treats it as branch-4-style
// fallback; this implementation matches.
func resolveMaterializationStore(
	env materializationEnv,
	hint *capture_receipt.StoreHint,
	storeOverride string,
	diagnostics io.Writer,
) (blob_stores.BlobStoreInitialized, error) {
	if storeOverride != "" {
		return resolveStoreByID(env, storeOverride)
	}

	if hint == nil {
		fmt.Fprintln(diagnostics, "notice: receipt carries no store hint")
		fmt.Fprintln(diagnostics, "notice: falling back to active store")
		return env.GetDefaultBlobStore(), nil
	}

	var hintID blob_store_id.Id
	if err := hintID.Set(hint.StoreId); err != nil {
		fmt.Fprintf(diagnostics,
			"notice: receipt store-hint id %q is malformed: %v\n",
			hint.StoreId, err)
		fmt.Fprintln(diagnostics, "notice: falling back to active store")
		return env.GetDefaultBlobStore(), nil
	}

	stores := env.GetBlobStores()
	hintedStore, ok := stores[hintID.String()]
	if !ok {
		fmt.Fprintf(diagnostics,
			"notice: receipt names store %q which is not configured locally\n",
			hint.StoreId)
		fmt.Fprintln(diagnostics, "notice: falling back to active store")
		return env.GetDefaultBlobStore(), nil
	}

	localHint, err := capture_receipt.ComputeStoreHint(hintedStore, hintID.String())
	if err != nil || localHint == nil {
		fmt.Fprintf(diagnostics,
			"notice: cannot compute local config-markl-id for store %q: %v\n",
			hint.StoreId, err)
		fmt.Fprintln(diagnostics, "notice: falling back to active store")
		return env.GetDefaultBlobStore(), nil
	}

	if localHint.ConfigMarklId == hint.ConfigMarklId {
		return hintedStore, nil
	}

	fmt.Fprintf(diagnostics,
		"warning: store %s has been re-configured since this receipt was written\n"+
			"  receipt config-hash: %s\n"+
			"  current config-hash: %s\n",
		hint.StoreId, hint.ConfigMarklId, localHint.ConfigMarklId)
	return blob_stores.BlobStoreInitialized{}, errors.ErrorWithStackf(
		"pass -store <id> to override and use the current store\n"+
			"hint: re-running with -store %s uses the current configuration",
		hint.StoreId)
}
