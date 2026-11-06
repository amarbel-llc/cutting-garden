// Package tailwriter is the bounded stderr buffer the exec-a-downloader
// plugins (ytdlp, optical, googlephotos) share: it keeps the last N bytes a
// tool wrote, enough to put its error message in a failure diagnostic without
// retaining megabytes of progress noise (cutting-garden#96).
//
// It lives under plugins/internal/ rather than internal/ because a plugin
// consumes the pkgs/ SDK only (internal/sdklayering), and this helper is not
// SDK surface.
package tailwriter

import "bytes"

// Writer keeps only the last cap bytes written through it. It is an
// io.Writer, so it can be assigned to cmd.Stderr or fed line by line, and it
// is not safe for concurrent use: the callers write it from one goroutine and
// read it after that goroutine is joined.
type Writer struct {
	buf *bytes.Buffer
	cap int
}

// New returns a Writer that keeps the last cap bytes in buf.
func New(buf *bytes.Buffer, cap int) *Writer {
	return &Writer{buf: buf, cap: cap}
}

func (w *Writer) Write(p []byte) (int, error) {
	if len(p) >= w.cap {
		w.buf.Reset()
		w.buf.Write(p[len(p)-w.cap:])
		return len(p), nil
	}
	if w.buf.Len()+len(p) > w.cap {
		drop := w.buf.Len() + len(p) - w.cap
		bs := w.buf.Bytes()
		w.buf.Reset()
		w.buf.Write(bs[drop:])
	}
	w.buf.Write(p)
	return len(p), nil
}
