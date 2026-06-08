// Package health wires the `health` subcommand: enumerate every
// URI-scheme plugin registered in the binary and report, for each, the
// schemes it claims, its receipt type-tag, and which capabilities it
// implements — capture, restore (direct or via the capture protocol),
// diff, the RFC 0002 capture protocol (with its kind), and RootLister
// traversal (with its node types).
//
// Positional surface:
//
//	health [-format text|json]
//
// Exit 0 on success, 64 on a bad -format value or trailing arguments.
// Reporting the home-manager `serve` module status is a future addition
// (no status signal exists today).
package health

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"
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
	output io.Writer
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
			"RFC 0002 capture protocol (with its kind), and RootLister " +
			"traversal (with its node types). Takes no positional arguments.",
	}
}

func (cmd *Health) SetFlagDefinitions(flagSet interfaces.CLIFlagDefinitions) {
	flagSet.StringVar(
		&cmd.Format,
		"format",
		formatText,
		"output format: text (aligned table) or json (one object per plugin)",
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

	rows := collectRows()

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
}

// collectRows enumerates the registered plugins and probes each.
func collectRows() []pluginRow {
	plugins := cutting_garden_plugins.RegisteredPlugins()
	rows := make([]pluginRow, 0, len(plugins))
	for _, p := range plugins {
		rows = append(rows, probe(p))
	}
	return rows
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

	return row
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
	fmt.Fprintln(tw, "PLUGIN\tSCHEMES\tCAPTURE\tRESTORE\tDIFF\tPROTOCOL\tTRAVERSAL")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Plugin,
			strings.Join(r.Schemes, ","),
			yesNo(r.Capture),
			r.Restore,
			yesNo(r.Diff),
			orDash(r.Protocol),
			orDash(strings.Join(r.Traversal, ",")),
		)
	}
	if err := tw.Flush(); err != nil {
		return errors.Wrap(err)
	}

	if _, err := io.WriteString(w, buf.String()); err != nil {
		return errors.Wrap(err)
	}
	return nil
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
