package cutting_garden_plugin_web

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"

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
