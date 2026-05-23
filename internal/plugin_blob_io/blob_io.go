// Package plugin_blob_io houses shared blob-streaming helpers used by
// every capture/diff plugin. Both the filesystem and yt-dlp plugins
// open a local file, stream it through a markl digester into the
// caller's blob store, and abort cleanly on context cancellation;
// keeping that loop in one place avoids the textual duplication that
// previously lived under each plugin and drifted independently.
//
// The discard-store variant lives nowhere — callers that want hash-
// only behaviour pass a discard-wrapping BlobStoreInitialized and
// reuse WriteFileBlob unchanged.
//
// Candidate for upstreaming to amarbel-llc/purse-first/libs/dewey
// (CtxReader specifically). Tracked at amarbel-llc/purse-first#90.
package plugin_blob_io

import (
	"context"
	"io"
	"os"

	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/madder/go/pkgs/domain_interfaces"
	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/errors"
)

// WriteFileBlob streams srcPath into store and returns the content-
// addressed markl id plus the byte count. ctx is observed for
// cancellation on every Read; long copies unwind promptly on
// SIGINT/SIGTERM.
func WriteFileBlob(
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

	if size, err = io.Copy(wc, NewCtxReader(ctx, src)); err != nil {
		err = errors.Wrap(err)
		return
	}

	id = wc.GetMarklId()
	return
}

// CtxReader wraps an io.Reader so io.Copy aborts when ctx is cancelled.
// Read checks ctx.Err before each underlying Read; once ctx is done,
// every subsequent Read returns ctx.Err and io.Copy unwinds with that
// error.
//
// Granularity is one buffer (io.Copy's default 32 KiB), which keeps
// interactive cancel latency well below a second on any reasonable
// I/O path while staying allocation-free.
type CtxReader struct {
	ctx context.Context
	r   io.Reader
}

func NewCtxReader(ctx context.Context, r io.Reader) CtxReader {
	return CtxReader{ctx: ctx, r: r}
}

func (c CtxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
