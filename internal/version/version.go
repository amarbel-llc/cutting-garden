// Package version wires the `version` subcommand: print the build
// version and commit SHA burnt into the binary at link time.
//
// Positional surface:
//
//	version
//
// Output is the self-identification line of eng-versioning(7) §"version
// subcommand output", then (the hybrid form) a blank line and a table of
// the linked plugins; the first line stays the whole identity, so
// `cg version | head -n1` is the scriptable form:
//
//	cutting-garden 0.1.0+abc1234
//
//	PLUGIN  SCHEMES  CAPABILITIES
//	caldav  caldav   capture,restore,diff,traversal,facets,write,create
//
// Dev builds (bare `go build`, `go run`, `go test`) show
// "cutting-garden dev+unknown"; release/nix builds show the version.env
// value and the flake rev. Exit 0 on success, 64 on trailing arguments.
package version

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"text/tabwriter"

	"code.linenisgreat.com/cutting-garden/internal/buildinfo"
	"code.linenisgreat.com/cutting-garden/internal/command"
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/health"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
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
			"the flake rev. After a blank line follows a table of the " +
			"plugins the binary links: each one's schemes and capabilities " +
			"(capture, restore, diff, traversal, facets, write, create). The " +
			"first line alone is the identity, for scripts. Takes no " +
			"positional arguments.",
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

	var buf strings.Builder
	fmt.Fprintf(&buf, "%s %s\n", progName, buildinfo.String())
	writePluginTable(&buf, cutting_garden_plugins.RegisteredPlugins())
	if _, err := io.WriteString(cmd.output, buf.String()); err != nil {
		errors.ContextCancelWithError(ctx, err)
	}
}

// writePluginTable appends, after a blank line, the hybrid form of
// eng-versioning(7): one row per linked plugin with its schemes and
// capabilities. A binary linking no plugin prints the self-line alone.
func writePluginTable(buf *strings.Builder, plugins []cutting_garden_plugins.Plugin) {
	if len(plugins) == 0 {
		return
	}
	buf.WriteString("\n")
	tw := tabwriter.NewWriter(buf, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "PLUGIN\tSCHEMES\tCAPABILITIES")
	for _, p := range plugins {
		schemes := slices.DeleteFunc(slices.Clone(p.Schemes()), func(s string) bool { return s == "" })
		if len(schemes) == 0 {
			schemes = []string{"(default)"}
		}
		capabilities := strings.Join(health.Capabilities(p), ",")
		if capabilities == "" {
			capabilities = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", health.DisplayName(p), strings.Join(schemes, ","), capabilities)
	}
	_ = tw.Flush() // a strings.Builder write cannot fail
}
