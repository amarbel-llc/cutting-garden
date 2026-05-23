package cutting_garden_plugin_ytdlp

import (
	"bytes"
	"context"
	"io"
	"os/exec"

	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/errors"
)

// stderrTailBytes caps how much yt-dlp stderr is buffered for the
// failure diagnostic. Enough to surface a typical error message
// (geo-block, gone-video, network) without retaining megabytes of
// progress noise when stderr is verbose.
const stderrTailBytes = 4096

// runYtdlp shells out to yt-dlp with args, honoring ctx for
// cancellation. yt-dlp itself decides what to write under outDir
// (paths are passed via -o templates in args); stdout is discarded
// because all artifacts land on disk. The last stderrTailBytes of
// stderr are wrapped into the returned error on non-zero exit.
//
// The binary is resolved through exec.LookPath, which honors the
// caller's PATH. nix-built cutting-garden wraps the binaries with
// makeWrapper so yt-dlp is on PATH at install time; devshells get it
// the same way.
func runYtdlp(ctx context.Context, outDir string, args []string) error {
	binPath, err := exec.LookPath("yt-dlp")
	if err != nil {
		return errors.ErrorWithStackf(
			"ytdlp plugin: yt-dlp not found on PATH (%v)\n"+
				"hint: enter the devshell or run a nix-built binary",
			err,
		)
	}

	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Dir = outDir

	var stderrTail bytes.Buffer
	cmd.Stderr = newTailWriter(&stderrTail, stderrTailBytes)
	cmd.Stdout = io.Discard

	if runErr := cmd.Run(); runErr != nil {
		return errors.ErrorWithStackf(
			"ytdlp plugin: yt-dlp failed (%v)\nstderr-tail: %s",
			runErr, stderrTail.String(),
		)
	}

	return nil
}

// tailWriter keeps only the last cap bytes written through it.
// Implements io.Writer so it can be assigned to cmd.Stderr without
// retaining yt-dlp's full progress stream.
type tailWriter struct {
	buf *bytes.Buffer
	cap int
}

func newTailWriter(buf *bytes.Buffer, cap int) *tailWriter {
	return &tailWriter{buf: buf, cap: cap}
}

func (w *tailWriter) Write(p []byte) (int, error) {
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
