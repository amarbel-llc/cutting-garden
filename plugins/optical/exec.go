package cutting_garden_plugin_optical

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os/exec"
	"sync"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// stderrTailBytes caps how much of the ripping tool's stderr is
// buffered for the failure diagnostic. Enough to surface a typical
// error (no medium present, I/O error, permission denied) without
// retaining the megabytes of carriage-return progress noise cdparanoia
// and ddrescue stream while running.
const stderrTailBytes = 4096

// scanBufInitial / maxScanLineBytes bound the line scanners. ddrescue
// and cdparanoia status lines are short, but a stray long line
// shouldn't make the scanner error out — raise the ceiling above the
// default 64 KiB.
const (
	scanBufInitial   = 64 * 1024
	maxScanLineBytes = 1024 * 1024
)

// runExternal shells out to binName (resolved via exec.LookPath) with
// args, honoring ctx for cancellation. cmd.Dir is outDir, so the tool's
// relative output filenames land there. Every line of stdout AND stderr
// is forwarded to onLog (nil-safe); the last stderrTailBytes of stderr
// are wrapped into the returned error on non-zero exit.
//
// Both pipes are drained to EOF in their goroutines before cmd.Wait, as
// the StdoutPipe/StderrPipe contract requires.
//
// The binary is resolved through exec.LookPath, which honors the
// caller's PATH. nix-built cutting-garden wraps the binaries with
// makeWrapper so cdparanoia/ddrescue are on PATH at install time;
// devshells get them the same way.
func runExternal(
	ctx context.Context,
	outDir string,
	binName string,
	args []string,
	onLog func(string),
) error {
	binPath, err := exec.LookPath(binName)
	if err != nil {
		return errors.ErrorWithStackf(
			"optical plugin: %s not found on PATH (%v)\n"+
				"hint: enter the devshell or run a nix-built binary",
			binName, err,
		)
	}

	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Dir = outDir

	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		return errors.Wrap(err)
	}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		return errors.Wrap(err)
	}

	if startErr := cmd.Start(); startErr != nil {
		return errors.Wrap(startErr)
	}

	// stderrTail is written only by the stderr goroutine, so the
	// goroutine join (wg.Wait) before reading it is the happens-before
	// barrier — no additional lock needed.
	var stderrTail bytes.Buffer
	tail := newTailWriter(&stderrTail, stderrTailBytes)

	var wg sync.WaitGroup
	wg.Add(2)

	// stdout: ddrescue streams its live status block here. Log only.
	go func() {
		defer wg.Done()
		scanLines(outPipe, nil, onLog)
	}()

	// stderr: cdparanoia progress and both tools' diagnostics. Every
	// line is a log line AND tees into the bounded failure tail so a
	// non-zero exit surfaces it.
	go func() {
		defer wg.Done()
		scanLines(errPipe, tail, onLog)
	}()

	// Both pipes must reach EOF before Wait per the pipe contract.
	wg.Wait()

	if waitErr := cmd.Wait(); waitErr != nil {
		return errors.ErrorWithStackf(
			"optical plugin: %s failed (%v)\nstderr-tail: %s",
			binName, waitErr, stderrTail.String(),
		)
	}

	return nil
}

// scanLines reads r line-by-line, forwarding each line to onLog
// (nil-safe). When tail is non-nil (the stderr stream), every line is
// also appended to the bounded failure tail (with a trailing newline so
// multi-line diagnostics stay legible), and a scanner error is noted
// there too. A scanner error is advisory: cmd.Wait decides overall
// success. The tail is written only by the single stderr goroutine, so
// the wg.Wait join in runExternal is the happens-before barrier
// guarding it.
func scanLines(r io.Reader, tail *tailWriter, onLog func(string)) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, scanBufInitial), maxScanLineBytes)
	for scanner.Scan() {
		line := scanner.Text()
		if tail != nil {
			tail.Write([]byte(line))
			tail.Write([]byte{'\n'})
		}
		if onLog != nil {
			onLog(line)
		}
	}
	if scanErr := scanner.Err(); scanErr != nil && tail != nil {
		tail.Write([]byte("optical plugin: stderr scan error: "))
		tail.Write([]byte(scanErr.Error()))
		tail.Write([]byte{'\n'})
	}
}

// tailWriter keeps only the last cap bytes written through it.
// Implements io.Writer so it can buffer a ripping tool's stderr stream
// without retaining the full progress noise for the failure diagnostic.
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
