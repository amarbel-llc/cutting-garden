// Package health wires the `health` subcommand: enumerate every
// URI-scheme plugin registered in the binary and report, for each, the
// schemes it claims, its receipt type-tag, and which capabilities it
// implements — capture, restore (direct or via the capture protocol),
// diff, the RFC 0002 capture protocol (with its kind), and RootLister
// traversal (with its node types) — and each RootProvider's roots.
//
// Positional surface:
//
//	health [-format text|json] [-config <path>]
//
// Exit 0 on success, 64 on a bad -format value or trailing arguments.

// Reporting the home-manager `serve` module status is a future addition
// (no status signal exists today).
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"code.linenisgreat.com/cutting-garden/internal/command"
	"code.linenisgreat.com/cutting-garden/internal/command_components"
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/interfaces"
)

// Format flag values.
const (
	formatText = "text"
	formatJSON = "json"
)

// Health is the value registered for the `health` subcommand. Format
// selects the rendering (text table or json); output is the writer the
// report goes to (os.Stdout in New, a buffer in tests).
type Health struct {
	Format string
	// ConfigPath, the -config flag, loads this file instead of
	// $XDG_CONFIG_HOME/cutting-garden/config.toml.
	ConfigPath string
	output     io.Writer
}

var (
	_ command.Cmd                       = (*Health)(nil)
	_ interfaces.CommandComponentWriter = (*Health)(nil)
)

// New constructs a Health with default flag values; output routes to
// os.Stdout.
func New() *Health {
	return &Health{Format: formatText, output: os.Stdout}
}

// newWithOutput is the test-only constructor that routes the report to
// the supplied writer.
func newWithOutput(output io.Writer) *Health {
	return &Health{Format: formatText, output: output}
}

func (*Health) GetDescription() command.Description {
	return command.Description{
		Short: "report registered plugins and their capabilities",
		Long: "Enumerates every URI-scheme plugin registered in the binary " +
			"and reports, for each, the schemes it claims, its receipt " +
			"type-tag, and which capabilities it implements: capture, " +
			"restore (direct or via the capture protocol), diff, the " +
			"RFC 0002 capture protocol (with its kind), RootLister " +
			"traversal (with its node types), and facets (RFC 0012: the " +
			"declared dimensions, and whether the plugin counts them itself " +
			"or the framework folds them). It also lists each RootProvider " +
			"plugin's roots (RFC 0007): the accounts the config declares and " +
			"the intrinsic ones, the entry points `list` and `mcp` start " +
			"from. Takes no positional arguments.",
	}
}

func (cmd *Health) SetFlagDefinitions(flagSet interfaces.CLIFlagDefinitions) {
	flagSet.StringVar(
		&cmd.Format,
		"format",
		formatText,
		"output format: text (aligned table) or json (one object per plugin)",
	)
	flagSet.StringVar(
		&cmd.ConfigPath,
		"config",
		"",
		"config file to load instead of $XDG_CONFIG_HOME/cutting-garden/config.toml "+
			"(it must exist)",
	)
}

func (cmd *Health) Run(req command.Request) {
	ctx := req.Context.(errors.Context)

	if err := validateFormat(cmd.Format); err != nil {
		errors.ContextCancelWithBadRequestf(ctx, "%s", err.Error())
		return
	}
	if req.RemainingArgCount() > 0 {
		errors.ContextCancelWithBadRequestf(ctx,
			"health takes no positional arguments; trailing: %v",
			req.PeekArgs())
		return
	}

	// The config is loaded once, before probing: the load injects every
	// plugin's section (so Roots sees its accounts) and registers any
	// config-declared wire plugin, which then gets its own row.
	if _, err := command_components.LoadAndInjectConfigAt(cmd.ConfigPath, os.Stderr); err != nil {
		errors.ContextCancelWithError(ctx, err)
		return
	}

	rows := collectRows(ctx)

	var err error
	if cmd.Format == formatJSON {
		err = writeJSON(cmd.output, rows)
	} else {
		err = writeText(cmd.output, rows)
	}
	if err != nil {
		errors.ContextCancelWithError(ctx, err)
	}
}

// pluginRow is one plugin's introspected capabilities.
type pluginRow struct {
	Plugin    string   `json:"plugin"`
	Schemes   []string `json:"schemes"`
	TypeTag   string   `json:"type_tag"`
	Capture   bool     `json:"capture"`
	Restore   string   `json:"restore"` // "yes" | "protocol" | "no"
	Diff      bool     `json:"diff"`
	Protocol  string   `json:"protocol_kind,omitempty"`
	Traversal []string `json:"traversal_types,omitempty"`
	// Facets are the declared facet dimension keys (RFC 0012), each once;
	// FacetCounting says who computes a summary: "counter" (the plugin's
	// one-shot FacetCounter) or "fold" (the framework walk).
	Facets        []string `json:"facets,omitempty"`
	FacetCounting string   `json:"facet_counting,omitempty"`
	// Roots are a RootProvider's top-level roots (RFC 0007); RootsError is
	// why they could not be enumerated (a wire plugin that failed to spawn,
	// an unreadable account), the row's other columns still reported.
	Roots      []string `json:"roots,omitempty"`
	RootsError string   `json:"roots_error,omitempty"`
}

// collectRows enumerates the registered plugins and probes each.
func collectRows(ctx context.Context) []pluginRow {
	plugins := cutting_garden_plugins.RegisteredPlugins()
	rows := make([]pluginRow, 0, len(plugins))
	for _, p := range plugins {
		row := probe(p)
		row.Roots, row.RootsError = probeRoots(ctx, p)
		rows = append(rows, row)
	}
	return rows
}

// probeRoots asks a RootProvider for its roots, as `list` and `mcp` do
// with no URI; a failure is reported, not fatal (cutting-garden#165).
func probeRoots(ctx context.Context, p cutting_garden_plugins.Plugin) (roots []string, failure string) {
	provider, ok := p.(cutting_garden_plugins.RootProvider)
	if !ok {
		return nil, ""
	}
	urls, err := provider.Roots(ctx)
	if err != nil {
		return nil, err.Error()
	}
	for _, u := range urls {
		roots = append(roots, u.String())
	}
	return roots, ""
}

// probe builds a plugin's row by type-asserting it against each optional
// capability interface — the same probing the orchestrator uses to pick
// a dispatch path.
func probe(p cutting_garden_plugins.Plugin) pluginRow {
	row := pluginRow{
		Plugin:  displayName(p),
		Schemes: p.Schemes(),
		TypeTag: p.TypeTag(),
	}

	_, row.Capture = p.(cutting_garden_plugins.CapturePlugin)
	_, row.Diff = p.(cutting_garden_plugins.DiffPlugin)

	switch {
	case isType[cutting_garden_plugins.RestorePlugin](p):
		row.Restore = "yes"
	case isType[cutting_garden_plugins.ProtocolRestorePlugin](p):
		row.Restore = "protocol"
	default:
		row.Restore = "no"
	}

	if pk, ok := p.(cutting_garden_plugins.ProtocolRestorePlugin); ok {
		row.Protocol = pk.ProtocolKind()
	} else if pk, ok := p.(cutting_garden_plugins.ProtocolDiffPlugin); ok {
		row.Protocol = pk.ProtocolKind()
	}

	if rl, ok := p.(cutting_garden_plugins.RootLister); ok {
		for _, nt := range rl.Types() {
			row.Traversal = append(row.Traversal, nt.Tag)
		}
	}

	row.Facets, row.FacetCounting = probeFacets(p)

	return row
}

// DisplayName is the name a plugin is reported under: its first non-empty
// scheme.
func DisplayName(p cutting_garden_plugins.Plugin) string { return displayName(p) }

// Capabilities is a plugin's compact capability list, in a fixed order:
// capture, restore (direct or through the capture protocol), diff,
// traversal (RootLister), facets (declared dimensions), write (NodeMutator)
// and create (ContainerCreator). `version` prints it per linked plugin.
func Capabilities(p cutting_garden_plugins.Plugin) []string {
	row := probe(p)
	var out []string
	add := func(has bool, name string) {
		if has {
			out = append(out, name)
		}
	}
	add(row.Capture, "capture")
	add(row.Restore != "no", "restore")
	add(row.Diff, "diff")
	add(row.Traversal != nil, "traversal")
	add(row.FacetCounting != "", "facets")
	add(isType[cutting_garden_plugins.NodeMutator](p), "write")
	add(isType[cutting_garden_plugins.ContainerCreator](p), "create")
	return out
}

// probeFacets reports p's declared facet dimensions in declaration order,
// each once, and how its summaries are computed.
func probeFacets(p cutting_garden_plugins.Plugin) (dimensions []string, counting string) {
	if describer, ok := p.(cutting_garden_plugins.FacetDescriber); ok {
		seen := make(map[string]bool)
		for _, typeFacets := range describer.DescribeFacets() {
			for _, dimension := range typeFacets.Dimensions {
				if !seen[dimension.Key] {
					seen[dimension.Key] = true
					dimensions = append(dimensions, dimension.Key)
				}
			}
		}
	}

	switch {
	case isType[cutting_garden_plugins.FacetCounter](p):
		counting = "counter"
	case dimensions != nil:
		counting = "fold"
	}
	return dimensions, counting
}

// facetsLabel is the table's short form of a row's facets: the dimension
// count and who counts them (the json output lists the keys).
func facetsLabel(r pluginRow) string {
	if r.FacetCounting == "" {
		return "-"
	}
	return fmt.Sprintf("%d (%s)", len(r.Facets), r.FacetCounting)
}

// isType reports whether p satisfies interface T.
func isType[T any](p cutting_garden_plugins.Plugin) bool {
	_, ok := p.(T)
	return ok
}

// displayName is the plugin's first non-empty scheme — the name a user
// types — falling back to a marker for the schemeless default plugin.
func displayName(p cutting_garden_plugins.Plugin) string {
	for _, s := range p.Schemes() {
		if s != "" {
			return s
		}
	}
	return "(schemeless)"
}

// writeText renders the rows as an aligned table.
func writeText(w io.Writer, rows []pluginRow) error {
	var buf strings.Builder
	tw := tabwriter.NewWriter(&buf, 0, 2, 2, ' ', 0)

	// Writes to a strings.Builder cannot fail, so the row writes are
	// error-free; only the final flush to w is fallible.
	fmt.Fprintln(tw, "PLUGIN\tSCHEMES\tCAPTURE\tRESTORE\tDIFF\tPROTOCOL\tFACETS\tTRAVERSAL")
	for _, r := range rows {
		fmt.Fprintf(
			tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Plugin,
			schemesLabel(r.Schemes),
			yesNo(r.Capture),
			r.Restore,
			yesNo(r.Diff),
			orDash(r.Protocol),
			facetsLabel(r),
			orDash(strings.Join(r.Traversal, ",")),
		)
	}
	if err := tw.Flush(); err != nil {
		return errors.Wrap(err)
	}

	writeRootsTable(&buf, rows)

	if _, err := io.WriteString(w, buf.String()); err != nil {
		return errors.Wrap(err)
	}
	return nil
}

// writeRootsTable appends, after a blank line, one `PLUGIN ROOTS` line per
// root (or per roots failure), omitted when no plugin has any.
func writeRootsTable(buf *strings.Builder, rows []pluginRow) {
	var lines []string
	for _, r := range rows {
		for _, root := range r.Roots {
			lines = append(lines, r.Plugin+"\t"+root)
		}
		if r.RootsError != "" {
			lines = append(lines, r.Plugin+"\t(unavailable: "+r.RootsError+")")
		}
	}
	if len(lines) == 0 {
		return
	}
	buf.WriteString("\n")
	tw := tabwriter.NewWriter(buf, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "PLUGIN\tROOTS")
	for _, line := range lines {
		fmt.Fprintln(tw, line)
	}
	_ = tw.Flush() // a strings.Builder write cannot fail
}

// writeJSON re-emits the rows as NDJSON — one plugin object per line —
// for piping into jq.
func writeJSON(w io.Writer, rows []pluginRow) error {
	enc := json.NewEncoder(w)
	for i := range rows {
		if err := enc.Encode(rows[i]); err != nil {
			return errors.Wrap(err)
		}
	}
	return nil
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// schemesLabel renders a plugin's schemes for the table, showing the
// empty schemeless-default claim as "(default)" rather than a bare
// leading comma. The json output keeps the raw scheme strings.
func schemesLabel(schemes []string) string {
	out := make([]string, len(schemes))
	for i, s := range schemes {
		if s == "" {
			out[i] = "(default)"
			continue
		}
		out[i] = s
	}
	return strings.Join(out, ",")
}

// validateFormat enforces the -format value constraint. Mirrors
// failures.validateFormat.
func validateFormat(value string) error {
	switch value {
	case formatText, formatJSON:
		return nil
	}
	return errors.ErrorWithStackf(
		"invalid -format value %q; expected text or json", value,
	)
}
