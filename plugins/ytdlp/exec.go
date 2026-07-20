package cutting_garden_plugin_ytdlp

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// progressSentinel tags the structured progress lines yt-dlp emits via
// --progress-template. yt-dlp writes --progress-template output (and its
// [youtube]/[download] status) to STDOUT, not stderr. Lines beginning
// with this prefix (plus a tab) are parsed by parseProgressLine;
// everything else on either stream is treated as a log line. The token
// is deliberately short and unlikely to appear in yt-dlp's own
// diagnostics.
const progressSentinel = "CGP"

// progressTemplate is the --progress-template value paired with
// progressSentinel. Field order matches parseProgressLine's expectations
// exactly — keep the two in lockstep. The template emits, tab-separated:
//
//	CGP <downloaded_bytes> <total_bytes> <total_bytes_estimate> <info.id>
//
// yt-dlp fills unknown numeric fields with "NA"; parseProgressLine
// tolerates that (and empty/"none") by treating them as zero, preferring
// total_bytes and falling back to total_bytes_estimate.
const progressTemplate = "download:" + progressSentinel +
	"\t%(progress.downloaded_bytes)s" +
	"\t%(progress.total_bytes)s" +
	"\t%(progress.total_bytes_estimate)s" +
	"\t%(info.id)s"

// progressLinePrefix is the byte prefix that marks a parseable progress
// line (sentinel followed by the field-separator tab).
const progressLinePrefix = progressSentinel + "\t"

// stderrTailBytes caps how much yt-dlp stderr is buffered for the
// failure diagnostic. Enough to surface a typical error message
// (geo-block, gone-video, network) without retaining megabytes of
// progress noise when stderr is verbose.
const stderrTailBytes = 4096

// scanBufInitial is the initial bufio.Scanner buffer; maxScanLineBytes
// caps line growth. yt-dlp progress and diagnostic lines are short, but
// a stray long line (e.g. a wall of warnings) shouldn't make the scanner
// error out — raise the ceiling well above the default 64 KiB. Both
// stream scanners use these limits.
const (
	scanBufInitial   = 64 * 1024
	maxScanLineBytes = 1024 * 1024
)

// progressSample is one decoded progress observation: bytes downloaded
// so far, the best-known total (total_bytes, else total_bytes_estimate,
// else 0), and the video id the bytes belong to.
type progressSample struct {
	Downloaded int64
	Total      int64
	ID         string
}

// parseProgressLine decodes one stdout line emitted under
// progressTemplate. ok is false for any line that is not a sentinel
// progress line (so callers route it to onLog instead). For sentinel
// lines, malformed numeric fields degrade to zero rather than erroring —
// progress is advisory, never a reason to fail a capture.
func parseProgressLine(line string) (sample progressSample, ok bool) {
	if !strings.HasPrefix(line, progressLinePrefix) {
		return progressSample{}, false
	}

	// Fields after the sentinel:
	//   [0]=downloaded_bytes [1]=total_bytes
	//   [2]=total_bytes_estimate [3]=info.id
	fields := strings.Split(line, "\t")
	const wantFields = 5 // sentinel + 4 values
	if len(fields) < wantFields {
		// A truncated sentinel line is not usable; surface it as a log
		// line so it isn't silently swallowed.
		return progressSample{}, false
	}

	downloaded := parseProgressInt(fields[1])
	total := parseProgressInt(fields[2])
	if total == 0 {
		total = parseProgressInt(fields[3])
	}

	return progressSample{
		Downloaded: downloaded,
		Total:      total,
		ID:         fields[4],
	}, true
}

// parseProgressInt parses a yt-dlp numeric progress field. yt-dlp writes
// "NA" (and occasionally "none") for unknown values; those and any
// unparseable input collapse to 0 so a missing total never crashes a
// capture.
func parseProgressInt(s string) int64 {
	switch s {
	case "", "NA", "none":
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// routeLine classifies one output line and dispatches it. A sentinel
// progress line (see progressTemplate) is decoded and handed to
// onProgress; any other line is handed to onLog. Both callbacks are
// nil-safe — a nil callback simply skips delivery. Kept pure (no I/O,
// no command state) so the stdout/stderr routing can be unit-tested
// without spawning yt-dlp.
func routeLine(
	line string,
	onProgress func(progressSample),
	onLog func(string),
) {
	if sample, ok := parseProgressLine(line); ok {
		if onProgress != nil {
			onProgress(sample)
		}
		return
	}
	if onLog != nil {
		onLog(line)
	}
}

// runYtdlp shells out to yt-dlp with args, honoring ctx for
// cancellation. yt-dlp itself decides what to write under outDir
// (paths are passed via -o templates in args); all artifacts land on
// disk. The last stderrTailBytes of stderr are wrapped into the
// returned error on non-zero exit.
//
// Both streams are scanned concurrently. yt-dlp writes
// --progress-template output AND its [youtube]/[download] status to
// STDOUT, so the stdout scanner routes sentinel progress lines (see
// progressTemplate) to onProgress and everything else to onLog. The
// stderr scanner (real warnings/errors, suppressed by --no-warnings but
// still the channel for fatal diagnostics) feeds onLog AND tees into the
// bounded failure tail surfaced on non-zero exit. Both callbacks are
// nil-safe.
//
// Both pipes are drained to EOF in their goroutines before cmd.Wait, as
// the StdoutPipe/StderrPipe contract requires (no Wait before drain).
//
// The binary is resolved through exec.LookPath, which honors the
// caller's PATH. nix-built cutting-garden wraps the binaries with
// makeWrapper so yt-dlp is on PATH at install time; devshells get it
// the same way.
func runYtdlp(
	ctx context.Context,
	outDir string,
	args []string,
	onProgress func(progressSample),
	onLog func(string),
) error {
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
	// goroutine join (wg.Wait) before reading it serves as the
	// happens-before barrier — no additional lock needed.
	var stderrTail bytes.Buffer
	tail := newTailWriter(&stderrTail, stderrTailBytes)

	var wg sync.WaitGroup
	wg.Add(2)

	// stdout: progress + status. Sentinel lines feed onProgress; the
	// rest are log lines. No tail — stdout never contributes to the
	// failure diagnostic.
	go func() {
		defer wg.Done()
		scanStream(outPipe, nil, onProgress, onLog)
	}()

	// stderr: diagnostics. Every line is a log line AND tees into the
	// bounded failure tail so a non-zero exit surfaces it.
	go func() {
		defer wg.Done()
		scanStream(errPipe, tail, onProgress, onLog)
	}()

	// Both pipes must reach EOF before Wait per the pipe contract.
	wg.Wait()

	if waitErr := cmd.Wait(); waitErr != nil {
		return errors.ErrorWithStackf(
			"ytdlp plugin: yt-dlp failed (%v)\nstderr-tail: %s",
			waitErr, stderrTail.String(),
		)
	}

	return nil
}

// scanStream reads r line-by-line and routes each line via routeLine.
// When tail is non-nil (the stderr stream), every routed line is also
// appended to the bounded failure tail (with a trailing newline so
// multi-line diagnostics stay legible), and a scanner error is noted
// there too. A scanner error is advisory: it never fails the capture —
// cmd.Wait decides overall success. The tail is written only by the
// single stderr goroutine, so the wg.Wait join in runYtdlp is the
// happens-before barrier guarding it; no lock is needed.
func scanStream(
	r io.Reader,
	tail *tailWriter,
	onProgress func(progressSample),
	onLog func(string),
) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, scanBufInitial), maxScanLineBytes)
	for scanner.Scan() {
		line := scanner.Text()
		if tail != nil {
			tail.Write([]byte(line))
			tail.Write([]byte{'\n'})
		}
		routeLine(line, onProgress, onLog)
	}
	if scanErr := scanner.Err(); scanErr != nil && tail != nil {
		tail.Write([]byte("ytdlp plugin: stderr scan error: "))
		tail.Write([]byte(scanErr.Error()))
		tail.Write([]byte{'\n'})
	}
}

// tailWriter keeps only the last cap bytes written through it.
// Implements io.Writer so it can buffer yt-dlp's stderr stream without
// retaining the full progress noise for the failure diagnostic.
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
