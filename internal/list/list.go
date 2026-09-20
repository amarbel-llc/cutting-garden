// Package list wires the `list` subcommand: resolve the RootLister
// plugin for a URI and print the immediate child nodes that node's
// traversal exposes — the read-only consumer of the plugin traversal
// primitive (FDR 0014).
//
// Positional surface:
//
//	list [-format text|json|espalier] [-facets [-filter PRED]] [-query TRELLIS] [URI]
//
// One level per invocation: `list caldav://host/dav/me/` lists the
// calendar collections; `list caldav://host/dav/me/personal/` lists that
// calendar's VTODO/VEVENT objects. With no URI it lists every plugin's
// configured roots. `-format text` renders a mesa table (styled on a TTY,
// TAB-separated on a pipe — purse-first RFC 0003), with a TAGS column when
// the plugin declares a tag dimension; `-format espalier` renders one
// organize object line per node through the shared trellis literal writer
// (native tags design G8/G13). `-facets` prints the node's hoisted facet
// summary instead of its children (narrowed by `-filter`); `-query` filters
// the listing by a trellis query evaluated against the URI's subtree
// (RFC 0014, FDR 0022, cutting-garden#164). Both `-facets` and `-query`
// require a URI. Read-only — no blob store is touched. Exit 0 on success,
// 2 on a resolution or traversal error, 64 on a bad -format value, a bad
// query, or a wrong argument count.
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
	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/cutting-garden/internal/trellis_eval"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/interfaces"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/mesa"
)

// Format flag values.
const (
	formatText     = "text"
	formatJSON     = "json"
	formatEspalier = "espalier"
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
	// Query, when set, is a trellis query (RFC 0014) evaluated against the
	// <uri>'s subtree: the listing becomes the query's matched nodes rather
	// than the raw child enumeration (FDR 0022, cutting-garden#164). Requires
	// a <uri>.
	Query  string
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
		"output format: text (table; TAB-separated on a pipe), json (one "+
			"object per node), or espalier (organize object lines)",
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
	flagSet.StringVar(
		&cmd.Query,
		"query",
		"",
		"trellis query filtering the listed nodes (RFC 0014; requires a <uri>)",
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
	if _, err := command_components.LoadAndInjectConfig(os.Stderr); err != nil {
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
		if cmd.Query != "" {
			// A query is anchored at an explicit <uri> in slice-1; the
			// no-URI default-anchor (roots-as-nodes) walk is deferred
			// (FDR 0022, cutting-garden#164).
			errors.ContextCancelWithBadRequestf(ctx,
				"list --query requires a <uri>")
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
	roots, err := command_components.AggregateRoots(ctx, os.Stderr)
	if err != nil {
		return err
	}
	// cutting-garden#120: a friendlier root label (e.g. a calendar-scoped
	// caldav account's DAV displayname) than the default URL-derived one,
	// when a plugin's RootLabeler supplies one.
	labels := command_components.AggregateRootLabels(ctx, os.Stderr)

	nodes := make([]cutting_garden_plugins.Node, 0, len(roots))
	for _, root := range roots {
		nodes = append(nodes, cutting_garden_plugins.Node{
			URI:  root,
			Name: rootDisplayLabel(root, labels),
		})
	}

	// Roots span plugins and are plain address entries — no per-node tag
	// presentation (or atom presentation) applies to any format.
	switch cmd.Format {
	case formatJSON:
		return writeJSON(cmd.output, nodes, nil)
	case formatEspalier:
		// No anchor: a root's box id is its full URI, the trailer its label.
		return writeEspalier(cmd.output, nodes, "", nil, nil)
	}
	return writeText(cmd.output, nodes, nil)
}

// rootLabel derives a short display name for a root URI: the last path
// segment, else the host, else the full URI. The framework-side fallback
// when no plugin supplies a friendlier label (see rootDisplayLabel,
// cutting-garden#120).
func rootLabel(u *url.URL) string {
	if trimmed := strings.TrimRight(u.Path, "/"); trimmed != "" {
		return path.Base(trimmed)
	}
	if u.Host != "" {
		return u.Host
	}
	return u.String()
}

// rootDisplayLabel resolves a root's display name for the aggregated
// roots listing: the RootLabeler-supplied friendly label
// (cutting-garden#120), keyed by the root URL's String() form, when
// present, else the framework's default rootLabel() derivation.
func rootDisplayLabel(u *url.URL, labels map[string]string) string {
	if label, ok := labels[u.String()]; ok && label != "" {
		return label
	}
	return rootLabel(u)
}

// runList resolves the RootLister for uriStr, enumerates the node's
// immediate children, and renders them. errors.Context satisfies
// context.Context, so it threads straight into ListRoots for cancelation.
func (cmd *List) runList(ctx errors.Context, uriStr string) error {
	u, lister, err := command_components.ResolveRootListerPlugin(uriStr)
	if err != nil {
		return err
	}

	// The [tags] interpreter override serves every path since native tags
	// slice 4 — a --query's bare-tag term resolution (RFC 0019 §4, #231
	// slice 3) AND each format's tag presentation (the JSON `tags` array,
	// the text table's TAGS column, the espalier boxes' tag atoms) — so the
	// config VALUE is re-read unconditionally. A missing config yields "".
	cfg, cerr := command_components.LoadDefaultConfig(nil)
	if cerr != nil {
		return errors.Wrapf(cerr, "list %s", uriStr)
	}
	tagsOverride := cfg.Tags.Interpreter

	// The presented tag set (design G12/G8): the designated FieldTag field's
	// values, ordered by the resolved interpreter's SortKey. nil (with no
	// error) when the plugin declares no tag dimension — the JSON view then
	// omits the key, the text table its TAGS column, and the espalier box its
	// tag atoms, and the non-espalier fetch below stays the cheap
	// metadata-only ListRoots.
	presentTags, err := command_components.NodeTagsPresenter(lister, tagsOverride)
	if err != nil {
		return errors.Wrapf(err, "list %s", uriStr)
	}

	var nodes []cutting_garden_plugins.Node
	if cmd.Query != "" {
		q, perr := trellis.Parse(cmd.Query)
		if perr != nil {
			return errors.BadRequestf("list %s --query: %s", uriStr, perr)
		}
		if nodes, err = trellis_eval.Evaluate(
			ctx, q, u, lister, trellis_eval.WithTagsInterpreter(tagsOverride),
		); err != nil {
			return errors.Wrapf(err, "list %s --query", uriStr)
		}
	} else if presentTags != nil || cmd.Format == formatEspalier {
		// Tags, box atoms, and the description trailer all present off
		// Node.Fields, which caldav's metadata-only ListRoots deliberately
		// leaves empty (cutting-garden#212) — prefer the plugin's enriched
		// listing exactly as organize's selection does. Espalier opts in even
		// without a tag dimension: its trailer/atoms want the enriched fields.
		if nodes, err = command_components.ListEnrichedChildren(ctx, lister, u); err != nil {
			return errors.Wrapf(err, "list %s", uriStr)
		}
	} else if nodes, err = lister.ListRoots(ctx, u); err != nil {
		return errors.Wrapf(err, "list %s", uriStr)
	}

	switch cmd.Format {
	case formatJSON:
		return writeJSON(cmd.output, nodes, presentTags)
	case formatEspalier:
		return writeEspalier(
			cmd.output, nodes, uriStr, presentTags,
			command_components.BoxAtomPresenter(lister),
		)
	}
	return writeText(cmd.output, nodes, presentTags)
}

// writeText renders the nodes as a mesa table (purse-first FDR 0015 /
// RFC 0003): styled when w is a terminal, plain TAB-separated on a pipe —
// the machine-friendly form the bats vectors assert. Columns are URI (the
// Flex column — it absorbs the terminal width), NAME, TYPE, and — only when
// the plugin declares a tag dimension (presentTags != nil, design G8) —
// TAGS, the presented tag set space-joined in SortKey order. An empty
// listing renders nothing (mesa's zero-row plain form).
func writeText(
	w io.Writer,
	nodes []cutting_garden_plugins.Node,
	presentTags func(cutting_garden_plugins.Node) []string,
) error {
	table := mesa.New().
		Col("URI", mesa.Flex).
		Col("NAME", mesa.Pin).
		Col("TYPE", mesa.Pin)
	if presentTags != nil {
		table.Col("TAGS", mesa.Pin)
	}
	for _, n := range nodes {
		cells := []mesa.Cell{
			mesa.Text(n.URIString()),
			mesa.Text(n.Name),
			mesa.Text(n.Type),
		}
		if presentTags != nil {
			cells = append(cells, mesa.Text(strings.Join(presentTags(n), " ")))
		}
		table.Row(cells...)
	}
	if err := table.Render(w); err != nil {
		return errors.Wrap(err)
	}
	return nil
}

// writeEspalier renders one organize object line per node — `- [<id>
// <tag>… <k>=<v>…] <desc>` — through the SAME projection organize's
// document builder uses (native tags design G8/G13): the box id is
// anchor-relative against the listed URI (command_components.RelativeID),
// the interior is spelled by trellis.WriteLiteral (tags leading, SortKey
// order, QuoteIfNeeded), and the trailer is the node's description field
// (command_components.NodeDescription). Lines sort by box id, mirroring
// organize's in-section ordering, so `list -format espalier <uri>` prints
// exactly the object lines `organize` would emit for the same node set —
// RFC 0014's isometry, pinned end to end by the list_espalier bats lane.
// A `!type` term is inlined only when the set spans several node types,
// organize's spelling-1/spelling-2 rule (there is no envelope here to
// distribute a single type, and a single-type listing must match
// spelling 2's bare boxes). A node of a plugin without the tag/atom
// capabilities renders id + trailer only: `- [<id>] <name>`.
func writeEspalier(
	w io.Writer,
	nodes []cutting_garden_plugins.Node,
	anchor string,
	presentTags func(cutting_garden_plugins.Node) []string,
	presentAtoms func(cutting_garden_plugins.Node) []cutting_garden_plugins.BoxAtom,
) error {
	type line struct{ id, rendered string }
	inlineType := multipleDistinctTypes(nodes)
	lines := make([]line, 0, len(nodes))
	for _, n := range nodes {
		lit := trellis.Literal{
			ID: command_components.RelativeID(n.URIString(), anchor),
		}
		if inlineType {
			lit.Type = n.Type
		}
		if presentTags != nil {
			lit.Tags = presentTags(n)
		}
		if presentAtoms != nil {
			for _, a := range presentAtoms(n) {
				lit.Atoms = append(lit.Atoms, trellis.Atom{Name: a.Name, Value: a.Value})
			}
		}

		var b strings.Builder
		b.WriteString("- [")
		trellis.WriteLiteral(&b, lit)
		b.WriteByte(']')
		if desc := command_components.NodeDescription(n); desc != "" {
			b.WriteByte(' ')
			b.WriteString(desc)
		}
		lines = append(lines, line{id: lit.ID, rendered: b.String()})
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].id < lines[j].id })

	var buf strings.Builder
	for _, ln := range lines {
		buf.WriteString(ln.rendered)
		buf.WriteByte('\n')
	}
	if _, err := io.WriteString(w, buf.String()); err != nil {
		return errors.Wrap(err)
	}
	return nil
}

// multipleDistinctTypes reports whether nodes span more than one node type —
// organize's spelling selector (buildDocument's distinctTypes rule): one type
// keeps boxes bare, several inline each box's `!type`.
func multipleDistinctTypes(nodes []cutting_garden_plugins.Node) bool {
	first := ""
	for _, n := range nodes {
		if n.Type == "" {
			continue
		}
		if first == "" {
			first = n.Type
			continue
		}
		if n.Type != first {
			return true
		}
	}
	return false
}

// nodeView is the json projection of a Node: the URI is rendered as its
// string form rather than url.URL's struct shape. Tags is the node's
// presented tag set (design G12, native tags slice 2) — the designated
// FieldTag field's values in the resolved interpreter's SortKey order —
// omitted entirely for an untagged node or a plugin with no tag dimension.
type nodeView struct {
	URI  string   `json:"uri"`
	Name string   `json:"name"`
	Type string   `json:"type"`
	Tags []string `json:"tags,omitempty"`
}

// writeJSON re-emits the nodes as NDJSON — one object per node — for
// piping into jq. presentTags (command_components.NodeTagsPresenter) fills
// each view's tag set; nil renders no tags (the roots listing, a plugin
// with no tag dimension).
func writeJSON(
	w io.Writer,
	nodes []cutting_garden_plugins.Node,
	presentTags func(cutting_garden_plugins.Node) []string,
) error {
	enc := json.NewEncoder(w)
	for _, n := range nodes {
		view := nodeView{
			URI:  n.URIString(),
			Name: n.Name,
			Type: n.Type,
		}
		if presentTags != nil {
			view.Tags = presentTags(n)
		}
		if err := enc.Encode(view); err != nil {
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
	if cmd.Format == formatEspalier {
		// A facet summary has no object lines to render; espalier is a
		// node-listing format only (#251 owns the --facets successor story).
		return errors.BadRequestf(
			"list --facets renders text or json, not espalier",
		)
	}

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

	filter, err := cutting_garden_plugins.ParseFacetFilter(cmd.Filter)
	if err != nil {
		return errors.Wrap(err)
	}

	// Validate an explicit filter against the plugin's declared schema
	// BEFORE computing anything (cutting-garden#161, the same rule the mcp
	// read_facets surface applies): an undeclared dimension or an
	// out-of-domain closed-dimension value is rejected with an actionable
	// error, and a date-kind predicate is annotated for prefix matching
	// (cutting-garden#230) — without this, `list --filter date_x=2026`
	// would silently degrade to exact matching.
	if verr := cutting_garden_plugins.ValidateFilterFor(filter, lister); verr != nil {
		return errors.Wrapf(verr, "list --facets %s (filtered)", uriStr)
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
// failures.validateFormat / health.validateFormat, plus list's own espalier.
func validateFormat(value string) error {
	switch value {
	case formatText, formatJSON, formatEspalier:
		return nil
	}
	return errors.ErrorWithStackf(
		"invalid -format value %q; expected text, json, or espalier", value,
	)
}
