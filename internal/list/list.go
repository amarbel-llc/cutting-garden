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
	"sort"
	"strings"
	"text/tabwriter"

	"code.linenisgreat.com/cutting-garden/internal/command"
	"code.linenisgreat.com/cutting-garden/internal/command_components"
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
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
	// Facets, when set, prints the node's hoisted facet summary (via the
	// plugin's FacetCounter) instead of its child listing (RFC 0012, FDR 0021).
	Facets bool
	// Filter is an optional comma-separated set of dimension=value predicates,
	// AND-composed, that narrows a --facets summary.
	Filter string
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
	flagSet.BoolVar(
		&cmd.Facets,
		"facets",
		false,
		"print the node's hoisted facet summary instead of its child listing",
	)
	flagSet.StringVar(
		&cmd.Filter,
		"filter",
		"",
		"comma-separated dimension=value predicates (AND-composed) narrowing --facets",
	)
}

func (cmd *List) Run(req command.Request) {
	ctx := req.Context.(errors.Context)

	if err := validateFormat(cmd.Format); err != nil {
		errors.ContextCancelWithBadRequestf(ctx, "%s", err.Error())
		return
	}

	// Config load precedes EVERY path, not just the no-arg root
	// aggregation: the direct-URI paths resolve through the scheme
	// registry, and a [[traversal_plugins]] wire plugin exists there
	// only after registration (RFC 0013 §Host integration). Without
	// this, `list fj://…` in a fresh process failed with "unknown
	// scheme" while the no-arg listing worked — found by fj-cg's live
	// conformance run (#140).
	if err := command_components.LoadAndInjectConfig(os.Stderr); err != nil {
		errors.ContextCancelWithError(ctx, err)
		return
	}

	// The URI is optional: PeekArgs rather than PopArg, which would poison
	// the context with a "missing argument" usage error on the no-arg path.
	args := req.PeekArgs()
	switch {
	case len(args) == 0:
		if cmd.Facets {
			// A summary is computed over a specific node's subtree; there is no
			// cross-plugin aggregate facet view (FDR 0021).
			errors.ContextCancelWithBadRequestf(ctx,
				"list --facets requires a <uri>")
			return
		}
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
		run := cmd.runList
		if cmd.Facets {
			run = cmd.runFacets
		}
		if err := run(ctx, args[0]); err != nil {
			errors.ContextCancelWithError(ctx, err)
		}
	}
}

// runRoots renders the aggregated top-level roots — each a URI the user
// can then pass back to `list` to descend one level. Config is already
// loaded and injected by Run.
func (cmd *List) runRoots(ctx errors.Context) error {
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

// runFacets resolves the FacetCounter for uriStr, computes the node's
// hoisted facet summary (narrowed by --filter), and renders it. The tracer
// consumes only the one-shot FacetCounter path (RFC 0012 §4.1); a plugin
// that does not implement it reports that facets are unavailable.
func (cmd *List) runFacets(ctx errors.Context, uriStr string) error {
	u, lister, err := command_components.ResolveRootListerPlugin(uriStr)
	if err != nil {
		return err
	}

	counter, ok := lister.(cutting_garden_plugins.FacetCounter)
	if !ok {
		return errors.ErrorWithStackf(
			"list --facets %s: plugin does not support facets", uriStr,
		)
	}

	filter, err := parseFacetFilter(cmd.Filter)
	if err != nil {
		return err
	}

	result, ok, err := counter.FacetCounts(ctx, u, filter)
	if err != nil {
		return errors.Wrapf(err, "list --facets %s", uriStr)
	}
	if !ok {
		return errors.ErrorWithStackf(
			"list --facets %s: no facet summary available at this node", uriStr,
		)
	}

	if cmd.Format == formatJSON {
		return writeFacetsJSON(cmd.output, result)
	}
	return writeFacetsText(cmd.output, result)
}

// parseFacetFilter parses "dim=val,dim2=val2" into an AND-composed
// FacetFilter. The empty string is no filter.
func parseFacetFilter(raw string) (cutting_garden_plugins.FacetFilter, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var filter cutting_garden_plugins.FacetFilter
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		dim, val, found := strings.Cut(part, "=")
		dim, val = strings.TrimSpace(dim), strings.TrimSpace(val)
		if !found || dim == "" || val == "" {
			return nil, errors.ErrorWithStackf(
				"invalid -filter predicate %q; expected dimension=value", part,
			)
		}
		filter = append(filter, cutting_garden_plugins.FacetPredicate{
			Dimension: dim,
			Value:     val,
		})
	}
	return filter, nil
}

// writeFacetsText renders the summary as one aligned row per dimension,
// values ordered by descending count then key, with a trailing marker when
// the summary is partial.
func writeFacetsText(w io.Writer, result cutting_garden_plugins.FacetResult) error {
	dims := make([]string, 0, len(result.Summary))
	for dim := range result.Summary {
		dims = append(dims, dim)
	}
	sort.Strings(dims)

	var buf strings.Builder
	tw := tabwriter.NewWriter(&buf, 0, 2, 2, ' ', 0)
	for _, dim := range dims {
		fmt.Fprintf(tw, "%s\t%s\n", dim, formatHistogram(result.Summary[dim]))
	}
	if err := tw.Flush(); err != nil {
		return errors.Wrap(err)
	}
	if !result.Complete {
		buf.WriteString("(partial — summary does not cover the whole subtree)\n")
	}

	if _, err := io.WriteString(w, buf.String()); err != nil {
		return errors.Wrap(err)
	}
	return nil
}

// formatHistogram renders one dimension's "key count  key count" line,
// ordered by descending count then key for a stable display.
func formatHistogram(hist cutting_garden_plugins.FacetHistogram) string {
	type bucket struct {
		key   string
		count int64
	}
	buckets := make([]bucket, 0, len(hist))
	for key, count := range hist {
		buckets = append(buckets, bucket{key, count})
	}
	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].count != buckets[j].count {
			return buckets[i].count > buckets[j].count
		}
		return buckets[i].key < buckets[j].key
	})

	parts := make([]string, 0, len(buckets))
	for _, b := range buckets {
		parts = append(parts, fmt.Sprintf("%s %d", b.key, b.count))
	}
	return strings.Join(parts, "  ")
}

// facetView is the json projection of a facet summary: the per-dimension
// histograms plus whether the summary is complete.
type facetView struct {
	Facets   cutting_garden_plugins.FacetSummary `json:"facets"`
	Complete bool                                `json:"complete"`
}

// writeFacetsJSON emits the summary as a single JSON object for jq.
func writeFacetsJSON(w io.Writer, result cutting_garden_plugins.FacetResult) error {
	enc := json.NewEncoder(w)
	if err := enc.Encode(facetView{
		Facets:   result.Summary,
		Complete: result.Complete,
	}); err != nil {
		return errors.Wrap(err)
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
