package cutting_garden_plugin_caldav

import (
	"context"
	"strings"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/plugin_blob_io"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
)

// storeResource hashes one fetched CalDAV resource's body into store and
// returns the EntryV1 describing it. rel — the resource's server-relative
// path — is returned even on error so callers can name the failure; a
// resource whose href resolves to the collection root yields rel=="" with
// a nil error and the zero entry, which callers skip rather than emit a
// pathless entry. Capture and diff share this so the EntryV1 shape
// (Root/Type/Mode — the keys the diff comparator matches on) is defined
// in exactly one place and the two paths cannot drift apart.
func storeResource(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	c *client,
	origin string,
	res resource,
) (entry capture_receipt.EntryV1, rel string, err error) {
	rel = serverPath(c.resolveHref(res.href))
	if rel == "" {
		return capture_receipt.EntryV1{}, "", nil
	}

	id, size, err := plugin_blob_io.WriteReaderBlob(
		ctx, store, strings.NewReader(res.data),
	)
	if err != nil {
		return capture_receipt.EntryV1{}, rel, err
	}

	return capture_receipt.EntryV1{
		Path:   rel,
		Root:   origin,
		Type:   capture_receipt.TypeFile,
		Mode:   resourceMode,
		Size:   size,
		BlobId: id.String(),
	}, rel, nil
}
