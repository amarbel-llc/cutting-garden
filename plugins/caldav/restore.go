package caldav

import (
	"context"
	"io"

	"github.com/amarbel-llc/cutting-garden/pkgs/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/cutting-garden/pkgs/plugin_blob_io"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/piggy/go/pkgs/markl"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// Restore materializes a receipt's CalDAV resources onto the destination
// endpoint: each file entry's captured body is PUT back at its
// server-absolute path on the destination host (entry.Path resolved
// against the destination origin). Non-file entries (which a pure CalDAV
// receipt never contains, but a mixed receipt might) are skipped. The
// restore is unconditional — it creates or overwrites — and aborts on
// the first failure so a partial restore surfaces loudly.
func (Plugin) Restore(req cutting_garden_plugins.RestoreRequest) error {
	base, username, password, err := connectionFromArg(req.Dest)
	if err != nil {
		return err
	}
	origin, ok := originOf(base)
	if !ok {
		return errors.ErrorWithStackf(
			"caldav plugin: destination %q has no host", base,
		)
	}
	c := newClient(base, username, password)

	for i := range req.Entries {
		if err := req.Context.Err(); err != nil {
			return errors.Wrap(err)
		}

		e := req.Entries[i]
		if e.Type != capture_receipt.TypeFile {
			// dir/symlink/other have no CalDAV representation; a pure
			// caldav receipt has none of these.
			continue
		}

		body, err := readBlob(req.Context, req.BlobStore, e.BlobId)
		if err != nil {
			return errors.Wrapf(err, "read blob for %s", e.Path)
		}

		target := origin + "/" + e.Path
		if err := c.putResource(req.Context, target, body); err != nil {
			return err
		}
	}

	return nil
}

// readBlob reads a content-addressed blob fully into a string, honoring
// ctx cancellation on the copy.
func readBlob(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	blobID string,
) (string, error) {
	var id markl.Id
	if err := id.Set(blobID); err != nil {
		return "", errors.Wrapf(err, "parse blob_id %q", blobID)
	}

	reader, err := store.MakeBlobReader(&id)
	if err != nil {
		return "", errors.Wrapf(err, "open blob %s", &id)
	}
	defer func() { _ = reader.Close() }()

	data, err := io.ReadAll(plugin_blob_io.NewCtxReader(ctx, reader))
	if err != nil {
		return "", errors.Wrapf(err, "read blob %s", &id)
	}
	return string(data), nil
}
