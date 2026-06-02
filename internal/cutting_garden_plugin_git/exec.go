package cutting_garden_plugin_git

import (
	"bytes"
	"context"
	"io"
	"os/exec"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// stderrTailBytes caps how much git stderr is buffered for the failure
// diagnostic. Enough to surface a typical error (auth refused, unknown
// branch, network) without retaining megabytes of clone progress noise.
const stderrTailBytes = 4096

// lookGit resolves the git binary on PATH once per call. nix-built
// cutting-garden wraps the binaries with makeWrapper so git is on PATH
// at install time; devshells get it the same way.
func lookGit() (string, error) {
	binPath, err := exec.LookPath("git")
	if err != nil {
		return "", errors.ErrorWithStackf(
			"git plugin: git not found on PATH (%v)\n"+
				"hint: enter the devshell or run a nix-built binary",
			err,
		)
	}
	return binPath, nil
}

// runGit shells out to git with args, honoring ctx for cancellation.
// dir, when non-empty, becomes the child's working directory (used to
// run commands inside the bare clone). stdout is discarded — the
// callers that need output use gitOutput instead. The last
// stderrTailBytes of stderr are wrapped into the returned error on
// non-zero exit.
func runGit(ctx context.Context, dir string, args ...string) error {
	binPath, err := lookGit()
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Dir = dir

	var stderrTail bytes.Buffer
	cmd.Stderr = newTailWriter(&stderrTail, stderrTailBytes)
	cmd.Stdout = io.Discard

	if runErr := cmd.Run(); runErr != nil {
		return errors.ErrorWithStackf(
			"git plugin: git %s failed (%v)\nstderr-tail: %s",
			argSummary(args), runErr, stderrTail.String(),
		)
	}

	return nil
}

// gitOutput is runGit's read-the-output sibling: it returns the child's
// stdout on success (used by `ls-remote`). stderr is tailed into the
// error on non-zero exit exactly as runGit does.
func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	binPath, err := lookGit()
	if err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Dir = dir

	var (
		stdout     bytes.Buffer
		stderrTail bytes.Buffer
	)
	cmd.Stdout = &stdout
	cmd.Stderr = newTailWriter(&stderrTail, stderrTailBytes)

	if runErr := cmd.Run(); runErr != nil {
		return "", errors.ErrorWithStackf(
			"git plugin: git %s failed (%v)\nstderr-tail: %s",
			argSummary(args), runErr, stderrTail.String(),
		)
	}

	return stdout.String(), nil
}

// argSummary returns the leading git subcommand for diagnostics without
// echoing a (potentially long) remote URL or local tempdir path.
func argSummary(args []string) string {
	if len(args) == 0 {
		return "(no args)"
	}
	return args[0]
}

// tailWriter keeps only the last cap bytes written through it.
// Implements io.Writer so it can be assigned to cmd.Stderr without
// retaining git's full progress stream.
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
