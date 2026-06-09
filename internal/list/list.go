// Package list wires the `list` subcommand: resolve the RootLister
// plugin for a URI and print the immediate child nodes that node's
// traversal exposes — the read-only consumer of the plugin traversal
// primitive (FDR 0014).
//
// Positional surface:
//
//	list [-format text|json] URI
//
// One level per invocation: `list caldav://host/dav/me/` lists the
// calendar collections; `list caldav://host/dav/me/personal/` lists that
// calendar's VTODO/VEVENT objects. Read-only — no blob store is touched.
// Exit 0 on success, 2 on a resolution or traversal error, 64 on a bad
// -format value or wrong argument count.
package list

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strings"
	"text/tabwriter"

	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/cutting-garden/internal/command_components"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"
)

// Format flag values.
const (
	formatText = "text"
	formatJSON = "json"
)

// List is the value registered for the `list` subcommand. Format selects
// the rendering; output is the writer the listing goes to (os.Stdout in
// New, a buffer in tests).
type List struct {
	Format string
	output io.Writer
}

var (
	_ command.Cmd                       = (*List)(nil)
	_ interfaces.CommandComponentWriter = (*List)(nil)
)

// New constructs a List with default flag values; output routes to
// os.Stdout.
func New() *List {
	return &List{Format: formatText, output: os.Stdout}
}

// newWithOutput is the test-only constructor that routes the listing to
// the supplied writer.
func newWithOutput(output io.Writer) *List {
	return &List{Format: formatText, output: output}
}

func (*List) GetDescription() command.Description {
	return command.Description{
		Short: "list the child nodes of a traversable plugin URI",
		Long: "Resolves the plugin for URI and prints the immediate child " +
			"nodes its RootLister traversal exposes \\(em for a CalDAV " +
			"endpoint the calendar collections, for a calendar its " +
			"VTODO/VEVENT objects. One level per invocation: descend a " +
			"container by running list again on its URI. Read-only; no " +
			"blob store is touched.",
	}
}

func (cmd *List) SetFlagDefinitions(flagSet interfaces.CLIFlagDefinitions) {
	flagSet.StringVar(
		&cmd.Format,
		"format",
		formatText,
		"output format: text (aligned table) or json (one object per node)",
	)
}

func (cmd *List) Run(req command.Request) {
	ctx := req.Context.(errors.Context)

	if err := validateFormat(cmd.Format); err != nil {
		errors.ContextCancelWithBadRequestf(ctx, "%s", err.Error())
		return
	}

	// The URI is optional: PeekArgs rather than PopArg, which would poison
	// the context with a "missing argument" usage error on the no-arg path.
	args := req.PeekArgs()
	switch {
	case len(args) == 0:
		// No URI: list every configured and intrinsic root across all
		// plugins (RFC 0007) — the entry points to descend into.
		if err := cmd.runRoots(ctx); err != nil {
			errors.ContextCancelWithError(ctx, err)
		}
	case len(args) > 1:
		errors.ContextCancelWithBadRequestf(ctx,
			"too many positional arguments; list takes at most one (<uri>), "+
				"trailing: %v", args[1:])
	default:
		if err := cmd.runList(ctx, args[0]); err != nil {
			errors.ContextCancelWithError(ctx, err)
		}
	}
}

// runRoots loads the config, injects it into the plugins, and renders the
// aggregated top-level roots — each a URI the user can then pass back to
// `list` to descend one level.
func (cmd *List) runRoots(ctx errors.Context) error {
	if err := command_components.LoadAndInjectConfig(os.Stderr); err != nil {
		return err
	}
	roots, err := command_components.AggregateRoots(ctx)
	if err != nil {
		return err
	}

	nodes := make([]cutting_garden_plugins.Node, 0, len(roots))
	for _, root := range roots {
		nodes = append(nodes, cutting_garden_plugins.Node{
			URI:  root,
			Name: rootLabel(root),
		})
	}

	if cmd.Format == formatJSON {
		return writeJSON(cmd.output, nodes)
	}
	return writeText(cmd.output, nodes)
}

// rootLabel derives a short display name for a root URI: the last path
// segment, else the host, else the full URI.
func rootLabel(u *url.URL) string {
	if trimmed := strings.TrimRight(u.Path, "/"); trimmed != "" {
		return path.Base(trimmed)
	}
	if u.Host != "" {
		return u.Host
	}
	return u.String()
}

// runList resolves the RootLister for uriStr, enumerates the node's
// immediate children, and renders them. errors.Context satisfies
// context.Context, so it threads straight into ListRoots for cancelation.
func (cmd *List) runList(ctx errors.Context, uriStr string) error {
	u, lister, err := command_components.ResolveRootListerPlugin(uriStr)
	if err != nil {
		return err
	}

	nodes, err := lister.ListRoots(ctx, u)
	if err != nil {
		return errors.Wrapf(err, "list %s", uriStr)
	}

	if cmd.Format == formatJSON {
		return writeJSON(cmd.output, nodes)
	}
	return writeText(cmd.output, nodes)
}

// writeText renders the nodes as an aligned URI / NAME / TYPE table.
func writeText(w io.Writer, nodes []cutting_garden_plugins.Node) error {
	var buf strings.Builder
	tw := tabwriter.NewWriter(&buf, 0, 2, 2, ' ', 0)

	// Writes to a strings.Builder cannot fail; only the final flush to w
	// is fallible.
	fmt.Fprintln(tw, "URI\tNAME\tTYPE")
	for _, n := range nodes {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", n.URIString(), n.Name, n.Type)
	}
	if err := tw.Flush(); err != nil {
		return errors.Wrap(err)
	}

	if _, err := io.WriteString(w, buf.String()); err != nil {
		return errors.Wrap(err)
	}
	return nil
}

// nodeView is the json projection of a Node: the URI is rendered as its
// string form rather than url.URL's struct shape.
type nodeView struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// writeJSON re-emits the nodes as NDJSON — one object per node — for
// piping into jq.
func writeJSON(w io.Writer, nodes []cutting_garden_plugins.Node) error {
	enc := json.NewEncoder(w)
	for _, n := range nodes {
		if err := enc.Encode(nodeView{
			URI:  n.URIString(),
			Name: n.Name,
			Type: n.Type,
		}); err != nil {
			return errors.Wrap(err)
		}
	}
	return nil
}

// validateFormat enforces the -format value constraint. Mirrors
// failures.validateFormat / health.validateFormat.
func validateFormat(value string) error {
	switch value {
	case formatText, formatJSON:
		return nil
	}
	return errors.ErrorWithStackf(
		"invalid -format value %q; expected text or json", value,
	)
}
