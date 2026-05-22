package cutting_garden_plugin_ytdlp

import (
	"context"
	"io"
	"os"

	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/madder/go/pkgs/domain_interfaces"
	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/errors"
)

// writeFileBlob streams srcPath into store and returns the
// content-addressed markl id plus the byte count. Mirrors the file
// plugin's helper of the same name; duplicated because the file
// plugin's copy is package-private and the two plugins have no
// natural shared package today.
func writeFileBlob(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	srcPath string,
) (id domain_interfaces.MarklId, size int64, err error) {
	src, err := os.Open(srcPath)
	if err != nil {
		err = errors.Wrap(err)
		return
	}
	defer errors.DeferredCloser(&err, src)

	wc, err := store.MakeBlobWriter(nil)
	if err != nil {
		err = errors.Wrap(err)
		return
	}
	defer errors.DeferredCloser(&err, wc)

	if size, err = io.Copy(wc, newCtxReader(ctx, src)); err != nil {
		err = errors.Wrap(err)
		return
	}

	id = wc.GetMarklId()
	return
}

// ctxReader wraps an io.Reader so io.Copy aborts when ctx is
// cancelled. Twin of the file plugin's ctxReader; tracked for
// upstreaming at amarbel-llc/purse-first#90.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func newCtxReader(ctx context.Context, r io.Reader) ctxReader {
	return ctxReader{ctx: ctx, r: r}
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
