package capture_wire

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"

	"code.linenisgreat.com/cutting-garden/internal/capture_plugin"
	"code.linenisgreat.com/cutting-garden/internal/capture_serve"
	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

const stderrTailBytes = 4096

// writerArgv builds the RFC 0002 `writer.cmd` the v1 capture-batch
// subprocess pipes each node blob into: this very cutting-garden
// binary's hidden `__write-blob` sink, bound to the destination store.
// Relocated from plugins/web verbatim — it was already entirely
// generic (it resolves ITS OWN executable, not the capture plugin's).
// An empty storeName selects the default store.
func writerArgv(storeName string) ([]string, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, errors.Wrapf(err, "capture_wire: resolve own executable for writer.cmd")
	}
	argv := []string{self, "__write-blob"}
	if storeName != "" {
		argv = append(argv, "--store", storeName)
	}
	return argv, nil
}

// resolvedCommand splits Spec.Command into the executable and its
// fixed base args, resolving the executable via $PATH when it is a
// bare name (exec.LookPath, matching plugins/web's explicit PATH
// check so an absent binary degrades to "v2 unavailable" rather than
// an opaque exec error).
func (p *Plugin) resolvedCommand() (bin string, baseArgs []string, err error) {
	if len(p.spec.Command) == 0 {
		return "", nil, errors.ErrorWithStackf(
			"plugin %q: empty command", p.spec.Name,
		)
	}
	bin, err = exec.LookPath(p.spec.Command[0])
	if err != nil {
		return "", nil, err
	}
	return bin, p.spec.Command[1:], nil
}

// captureServeV2 attempts the capture over the RFC 0008 persistent
// session (this plugin's "capture-serve" subcommand): node blobs
// stream back through FD-passed pipes straight into store — no
// per-blob `__write-blob` spawn. usedV2 is false when v2 is
// unavailable (bring-up failure or unsupported-version refusal, per
// capture_serve.IsFallbackSignal) and the caller runs the v1 path
// instead; err is non-nil only for REAL failures after a successful
// handshake, which must not silently retry on v1. Relocated from
// plugins/web's captureServeV2, parameterized by Spec.Command instead
// of a hardcoded chrest binary.
func (p *Plugin) captureServeV2(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	target, format string,
) (receiptID string, usedV2 bool, err error) {
	bin, baseArgs, lookErr := p.resolvedCommand()
	if lookErr != nil {
		// No binary at all: let the v1 path produce its canonical
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
		bin, append(append([]string{}, baseArgs...), subcommandServe)...,
	)
	if err != nil {
		if capture_serve.IsFallbackSignal(err) {
			return "", false, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", false, errors.Wrapf(ctxErr,
				"plugin %q: %s canceled", p.spec.Name, subcommandServe)
		}
		return "", false, errors.Wrapf(err, "plugin %q: %s", p.spec.Name, subcommandServe)
	}

	if len(result.Errors) > 0 {
		return "", false, errors.ErrorWithStackf(
			"plugin %q: batch error (%s): %s",
			p.spec.Name, result.Errors[0].Kind, result.Errors[0].Message,
		)
	}
	if len(result.Captures) != 1 {
		return "", false, errors.ErrorWithStackf(
			"plugin %q: returned %d capture results, want 1",
			p.spec.Name, len(result.Captures),
		)
	}
	c := result.Captures[0]
	if c.Error != nil {
		return "", false, errors.ErrorWithStackf(
			"plugin %q: capture failed (%s): %s",
			p.spec.Name, c.Error.Kind, c.Error.Message,
		)
	}
	if c.Receipt == nil || c.Receipt.ID == "" {
		return "", false, errors.ErrorWithStackf(
			"plugin %q: returned no receipt for capture %q", p.spec.Name, c.Name,
		)
	}
	return c.Receipt.ID, true, nil
}

// runCaptureBatch execs the plugin's "capture-batch" subcommand, pipes
// the marshaled capture-plugin/v1 input to its stdin, and decodes the
// batch output from its stdout — the RFC 0008 §Migration v1 fallback.
// Relocated from plugins/web's runCaptureBatch, parameterized by
// Spec.Command. ctx cancellation propagates to the child.
func (p *Plugin) runCaptureBatch(ctx context.Context, input batchInput) (batchOutput, error) {
	bin, baseArgs, lookErr := p.resolvedCommand()
	if lookErr != nil {
		return batchOutput{}, errors.ErrorWithStackf(
			"plugin %q: %s not found on PATH (%v); enter the devshell or install it",
			p.spec.Name, p.spec.Command[0], lookErr,
		)
	}

	raw, err := json.Marshal(input)
	if err != nil {
		return batchOutput{}, errors.Wrap(err)
	}

	args := append(append([]string{}, baseArgs...), subcommandBatch)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = bytes.NewReader(raw)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	stderrTail := newTailWriter(stderrTailBytes)
	cmd.Stderr = stderrTail

	if runErr := cmd.Run(); runErr != nil {
		// A canceled context (SIGINT/SIGTERM) kills the child, surfacing
		// as a generic exit error. Report the cancellation rather than
		// mislabeling an intentional interrupt as a plugin crash.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return batchOutput{}, errors.Wrapf(ctxErr,
				"plugin %q: %s canceled", p.spec.Name, subcommandBatch)
		}
		return batchOutput{}, errors.ErrorWithStackf(
			"plugin %q: %s failed (%v)\nstderr-tail: %s",
			p.spec.Name, subcommandBatch, runErr, stderrTail.String(),
		)
	}

	var out batchOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return batchOutput{}, errors.Wrapf(err,
			"plugin %q: decode batch output (%s)",
			p.spec.Name, truncate(stdout.String(), 512))
	}
	if out.Schema != batchSchema {
		return batchOutput{}, errors.ErrorWithStackf(
			"plugin %q: batch output schema %q, want %q",
			p.spec.Name, out.Schema, batchSchema,
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

// tailWriter keeps only the last cap bytes written through it — enough
// to surface a plugin failure's tail without buffering an unbounded
// stream.
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
