package organize

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// resolveEditorCommand returns the editor command line to launch, honoring
// $VISUAL, then $EDITOR, then falling back to vi. The value may itself carry
// arguments (e.g. "code -w", "emacsclient -nw"); the caller splits on spaces.
func resolveEditorCommand() string {
	if v := os.Getenv("VISUAL"); v != "" {
		return v
	}
	if v := os.Getenv("EDITOR"); v != "" {
		return v
	}
	return "vi"
}

// launchEditor opens path in the user's editor (resolveEditorCommand),
// inheriting the terminal, and blocks until it exits. A non-zero exit is
// returned as an error so the caller can treat it as an abort — leaving the
// buffer on disk rather than applying a half-edit.
func launchEditor(ctx context.Context, path string) error {
	fields := strings.Fields(resolveEditorCommand())
	// resolveEditorCommand always returns at least "vi", so fields is non-empty.
	bin, err := exec.LookPath(fields[0])
	if err != nil {
		return errors.Wrapf(err, "organize: locate editor %q", fields[0])
	}

	c := exec.CommandContext(ctx, bin, append(fields[1:], path)...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		return errors.Wrapf(err, "organize: editor exited abnormally")
	}
	return nil
}
