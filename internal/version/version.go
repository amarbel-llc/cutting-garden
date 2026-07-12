// Package version wires the `version` subcommand: print the build
// version and commit SHA burnt into the binary at link time.
//
// Positional surface:
//
//	version
//
// Output is the self-identification line mandated by
// eng-versioning(7) §"version subcommand output" for a binary that pins
// no downstream components in flake.nix:
//
//	cutting-garden 0.1.0+abc1234
//
// Dev builds (bare `go build`, `go run`, `go test`) show
// "cutting-garden dev+unknown"; release/nix builds show the version.env
// value and the flake rev. Exit 0 on success, 64 on trailing arguments.
package version

import (
	"fmt"
	"io"
	"os"

	"code.linenisgreat.com/cutting-garden/internal/buildinfo"
	"code.linenisgreat.com/cutting-garden/internal/command"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// progName is the self-identification name printed ahead of the build
// identity. The canonical binary name, even when invoked via the `cg`
// alias — matching how the manpage and usage banner name the utility.
const progName = "cutting-garden"

// Version is the value registered for the `version` subcommand. output is
// the writer the line goes to (os.Stdout in New, a buffer in tests).
type Version struct {
	output io.Writer
}

var _ command.Cmd = (*Version)(nil)

// New constructs a Version with output routed to os.Stdout.
func New() *Version {
	return &Version{output: os.Stdout}
}

// newWithOutput is the test-only constructor that routes output to the
// supplied writer.
func newWithOutput(output io.Writer) *Version {
	return &Version{output: output}
}

func (*Version) GetDescription() command.Description {
	return command.Description{
		Short: "print the build version and commit",
		Long: "Prints the version and commit SHA burnt in via -ldflags at " +
			"build time, as a single self-identification line " +
			"\"cutting-garden <version>+<commit>\". Dev builds show " +
			"\"dev+unknown\"; release builds show the version.env value and " +
			"the flake rev. Takes no positional arguments.",
	}
}

func (cmd *Version) Run(req command.Request) {
	ctx := req.Context.(errors.Context)

	if req.RemainingArgCount() > 0 {
		errors.ContextCancelWithBadRequestf(ctx,
			"version takes no positional arguments; trailing: %v",
			req.PeekArgs())
		return
	}

	if _, err := fmt.Fprintf(
		cmd.output, "%s %s\n", progName, buildinfo.String(),
	); err != nil {
		errors.ContextCancelWithError(ctx, err)
	}
}
