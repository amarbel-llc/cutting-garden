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

	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
	"code.linenisgreat.com/madder/go/pkgs/domain_interfaces"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
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
	return WriteFileBlobProgress(ctx, store, srcPath, nil)
}

// progressStride is the minimum byte distance between consecutive
// onBytes callbacks from WriteFileBlobProgress. 1 MiB is coarse enough
// not to flood a TUI consumer (a few dozen ticks per second even on a
// fast local disk) and fine enough that a slow remote link (SFTP at
// single-digit MB/s) still moves the bar every second or so.
const progressStride = 1 << 20

// WriteFileBlobProgress is WriteFileBlob with a nil-safe byte-progress
// callback: onBytes(written) is invoked with the cumulative bytes
// copied for THIS file, at least once per progressStride bytes and once
// at completion (so even a sub-stride file gets exactly one call with
// its total). onBytes == nil behaves exactly like WriteFileBlob. The
// callback is observability only — it never influences the blob bytes
// or the returned id/size.
func WriteFileBlobProgress(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	srcPath string,
	onBytes func(int64),
) (id domain_interfaces.MarklId, size int64, err error) {
	src, err := os.Open(srcPath)
	if err != nil {
		err = errors.Wrap(err)
		return
	}
	defer errors.DeferredCloser(&err, src)

	if onBytes == nil {
		return WriteReaderBlob(ctx, store, src)
	}

	counter := &countingReader{r: src, onBytes: onBytes}
	if id, size, err = WriteReaderBlob(ctx, store, counter); err != nil {
		return
	}
	counter.flush()
	return
}

// countingReader wraps an io.Reader, tracking cumulative bytes read and
// invoking onBytes whenever at least progressStride bytes accumulated
// since the last call. flush emits one final call with the total —
// callers invoke it after a successful copy so the last sample always
// equals the file size.
type countingReader struct {
	r          io.Reader
	onBytes    func(int64)
	total      int64
	lastReport int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.total += int64(n)
	if c.total-c.lastReport >= progressStride {
		c.lastReport = c.total
		c.onBytes(c.total)
	}
	return n, err
}

func (c *countingReader) flush() {
	c.lastReport = c.total
	c.onBytes(c.total)
}

// WriteReaderBlob streams r into store and returns the content-addressed
// markl id plus the byte count. It is the reader-based sibling of
// WriteFileBlob, used by callers that already hold an open stream (e.g.
// the git plugin piping `git cat-file` output, or an in-memory
// strings.Reader) and want to avoid a temp-file round-trip. ctx is
// observed for cancellation on every Read.
func WriteReaderBlob(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	r io.Reader,
) (id domain_interfaces.MarklId, size int64, err error) {
	wc, err := store.MakeBlobWriter(nil)
	if err != nil {
		err = errors.Wrap(err)
		return
	}
	defer errors.DeferredCloser(&err, wc)

	if size, err = io.Copy(wc, NewCtxReader(ctx, r)); err != nil {
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
