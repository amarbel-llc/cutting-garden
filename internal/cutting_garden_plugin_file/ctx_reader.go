package cutting_garden_plugin_file

import (
	"context"
	"io"
)

// ctxReader wraps an io.Reader so io.Copy aborts when ctx is cancelled.
// Read checks ctx.Err before each underlying Read; once ctx is done,
// every subsequent Read returns ctx.Err and io.Copy unwinds with that
// error.
//
// Granularity is one buffer (io.Copy's default 32 KiB), which keeps
// interactive cancel latency well below a second on any reasonable
// I/O path while staying allocation-free.
//
// Candidate for upstreaming to amarbel-llc/purse-first/libs/dewey
// (charlie/ohio or bravo/errors). Tracked at
// amarbel-llc/purse-first#90.
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
