package cutting_garden_plugin_web

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"

	"code.linenisgreat.com/cutting-garden/pkgs/capture_plugin"
	"code.linenisgreat.com/cutting-garden/pkgs/capture_serve"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

const stderrTailBytes = 4096

// writerArgv builds the RFC 0002 `writer.cmd` the chrest subprocess pipes
// each node blob into: this very cutting-garden binary's hidden
// `__write-blob` sink, bound to the destination store. Running ourselves
// (rather than an external `madder write`) guarantees blobs land in
// exactly the store the capture targets, re-resolved from the inherited
// environment. An empty storeName selects the default store.
func writerArgv(storeName string) ([]string, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, errors.Wrapf(err, "web plugin: resolve own executable for writer.cmd")
	}
	argv := []string{self, "__write-blob"}
	if storeName != "" {
		argv = append(argv, "--store", storeName)
	}
	return argv, nil
}

// captureServeV2 attempts the capture over the RFC 0008 persistent
// session (`chrest capture-serve`): node blobs stream back through
// FD-passed pipes straight into store — no per-blob `__write-blob`
// spawn. usedV2 is false when v2 is unavailable (bring-up failure or
// unsupported-version refusal, per capture_serve.IsFallbackSignal) and
// the caller runs the v1 path instead; err is non-nil only for REAL
// failures after a successful handshake, which must not silently retry
// on v1.
func captureServeV2(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	target, format string,
) (receiptID string, usedV2 bool, err error) {
	bin, err := exec.LookPath(capturerBinary)
	if err != nil {
		// No chrest at all: let the v1 path produce its canonical
		// not-on-PATH diagnostic.
		return "", false, nil
	}

	normalize := true
	result, err := capture_serve.Run(
		ctx,
		capture_plugin.NewBlobStoreWriter(store),
		capture_serve.BatchParams{
			Target: target,
			Defaults: &capture_serve.BatchDefaults{
				Normalize: &normalize,
				Plugin:    map[string]any{"browser": defaultBrowser},
			},
			Captures: []capture_serve.CaptureSpec{
				{Name: captureName, Format: format},
			},
		},
		bin, serveSubcommand,
	)
	if err != nil {
		if capture_serve.IsFallbackSignal(err) {
			return "", false, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", false, errors.Wrapf(ctxErr,
				"web plugin: chrest capture-serve canceled")
		}
		return "", false, errors.Wrapf(err, "web plugin: chrest capture-serve")
	}

	if len(result.Errors) > 0 {
		return "", false, errors.ErrorWithStackf(
			"web plugin: chrest batch error (%s): %s",
			result.Errors[0].Kind, result.Errors[0].Message,
		)
	}
	if len(result.Captures) != 1 {
		return "", false, errors.ErrorWithStackf(
			"web plugin: chrest returned %d capture results, want 1",
			len(result.Captures),
		)
	}
	c := result.Captures[0]
	if c.Error != nil {
		return "", false, errors.ErrorWithStackf(
			"web plugin: capture failed (%s): %s", c.Error.Kind, c.Error.Message,
		)
	}
	if c.Receipt == nil || c.Receipt.ID == "" {
		return "", false, errors.ErrorWithStackf(
			"web plugin: chrest returned no receipt for capture %q", c.Name,
		)
	}
	return c.Receipt.ID, true, nil
}

// runCaptureBatch execs `chrest capture-batch`, pipes the marshaled
// capture-plugin/v1 input to its stdin, and decodes the batch output from
// its stdout. chrest owns the merkle-tree assembly and writer fan-out; we
// only validate the protocol envelope. ctx cancellation propagates to the
// child (and, transitively, to its browser and writer subprocesses).
func runCaptureBatch(ctx context.Context, input batchInput) (batchOutput, error) {
	bin, err := exec.LookPath(capturerBinary)
	if err != nil {
		return batchOutput{}, errors.ErrorWithStackf(
			"web plugin: %s not found on PATH (%v); enter the devshell or install chrest",
			capturerBinary, err,
		)
	}

	raw, err := json.Marshal(input)
	if err != nil {
		return batchOutput{}, errors.Wrap(err)
	}

	cmd := exec.CommandContext(ctx, bin, "capture-batch")
	cmd.Stdin = bytes.NewReader(raw)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	stderrTail := newTailWriter(stderrTailBytes)
	cmd.Stderr = stderrTail

	if runErr := cmd.Run(); runErr != nil {
		// A canceled context (SIGINT/SIGTERM) kills chrest, surfacing as a
		// generic exit error. Report the cancellation rather than mislabeling
		// an intentional interrupt as a chrest crash.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return batchOutput{}, errors.Wrapf(ctxErr,
				"web plugin: chrest capture-batch canceled")
		}
		return batchOutput{}, errors.ErrorWithStackf(
			"web plugin: chrest capture-batch failed (%v)\nstderr-tail: %s",
			runErr, stderrTail.String(),
		)
	}

	var out batchOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return batchOutput{}, errors.Wrapf(err,
			"web plugin: decode chrest batch output (%s)", truncate(stdout.String(), 512))
	}
	if out.Schema != batchSchema {
		return batchOutput{}, errors.ErrorWithStackf(
			"web plugin: chrest batch output schema %q, want %q", out.Schema, batchSchema,
		)
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// tailWriter keeps only the last cap bytes written through it — enough to
// surface a chrest failure's tail without buffering an unbounded stream.
type tailWriter struct {
	buf []byte
	cap int
}

func newTailWriter(cap int) *tailWriter { return &tailWriter{cap: cap} }

func (w *tailWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.cap {
		w.buf = w.buf[len(w.buf)-w.cap:]
	}
	return len(p), nil
}

func (w *tailWriter) String() string { return string(w.buf) }
