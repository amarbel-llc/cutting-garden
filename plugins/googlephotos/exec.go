package cutting_garden_plugin_googlephotos

import (
	"bytes"
	"context"
	"io"
	"os/exec"

	"code.linenisgreat.com/cutting-garden/plugins/internal/tailwriter"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// stderrTailBytes caps how much gallery-dl stderr is buffered for the
// failure diagnostic. Enough to surface a typical error message
// (private album, expired share link, network) without retaining
// megabytes of progress noise when stderr is verbose.
const stderrTailBytes = 4096

// runGalleryDL shells out to gallery-dl with args, honoring ctx for
// cancellation. gallery-dl itself decides what to write under outDir
// (the location is passed via `--directory` in args); stdout is
// discarded because all artifacts land on disk. The last
// stderrTailBytes of stderr are wrapped into the returned error on
// non-zero exit.
//
// The binary is resolved through exec.LookPath, which honors the
// caller's PATH. nix-built cutting-garden wraps the binaries with
// makeWrapper so gallery-dl is on PATH at install time; devshells get
// it the same way.
func runGalleryDL(ctx context.Context, outDir string, args []string) error {
	binPath, err := exec.LookPath("gallery-dl")
	if err != nil {
		return errors.ErrorWithStackf(
			"google-photos plugin: gallery-dl not found on PATH (%v)\n"+
				"hint: enter the devshell or run a nix-built binary",
			err,
		)
	}

	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Dir = outDir

	var stderrTail bytes.Buffer
	cmd.Stderr = tailwriter.New(&stderrTail, stderrTailBytes)
	cmd.Stdout = io.Discard

	if runErr := cmd.Run(); runErr != nil {
		return errors.ErrorWithStackf(
			"google-photos plugin: gallery-dl failed (%v)\nstderr-tail: %s",
			runErr, stderrTail.String(),
		)
	}

	return nil
}
