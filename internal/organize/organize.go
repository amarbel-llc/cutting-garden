// Package organize wires the `organize` subcommand: generate a faceted,
// editable document from a plugin's nodes, then apply the edits back as
// substrate writes — dodder's organize upstreamed and generalized across
// plugins (FDR 0023, RFC 0015).
//
// Invocation shapes:
//
//	organize <expression> <group-by>                        interactive (TTY) / generate (pipe)
//	  (<expression> is one trellis expression carrying its own origin, RFC 0020
//	  §4.1: `<uri>` alone, or `'<uri> -> <query>'`. <group-by> may be positional
//	  as shown, or the --group-by flag: `(tags)`, `project`, `status=`,
//	  `date_due=(month)` — native tags design G10)
//	organize --apply <path> [--commit|--dry-run]            apply an edited document
//	organize --commit-directly < doc                        apply from stdin, committing
//
// A bare `organize <uri>` on a terminal generates the document into a temp file,
// opens it in $EDITOR, and applies the result on save (dodder's interactive
// default); with stdout piped/redirected it just prints the document (the
// MCP/scripting path). Generate selects the anchor's nodes (a trellis query, or
// the enriched child listing), groups them by the --group-by dimension,
// pins the pre-edit assignment as a content-addressed organize-base-v1 blob, and
// emits the document with a `- _base=@<digest>` line. You move object lines
// between headings; apply three-way-merges your edits against the pinned base and
// the re-queried live state and writes each move through the plugin's NodeMutator.
//
// Both paths render the change set as a per-object word-diff first (cutting-garden
// #224). Apply is wet-run by default AT A TERMINAL: it writes after showing the
// diff and confirming (the #224 gate). Piped or redirected — the MCP/scripting
// path — it stays dry-run and requires an explicit --commit, so a headless
// invocation never writes silently (cutting-garden#213). --dry-run forces preview
// everywhere. (This TTY-gated default is the interim lever pending the `%:dry-run`
// directive-in-doc, hyphence#14.)
//
// This is the FDR 0023 caldav tracer bullet: the writable dimension it exercises
// end-to-end is `status` (a passthrough enum). Date reschedule-by-move, which
// needs plugin-side bucket->value completion, arrives with the FacetWriteApplier
// verb (Slice 2b). The document dialect (internal/organize/document.go) is a
// deliberately minimal subset of RFC 0015, sufficient to prove the pipeline.
package organize

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode"

	"code.linenisgreat.com/cutting-garden/internal/cgconfig"
	"code.linenisgreat.com/cutting-garden/internal/command"
	"code.linenisgreat.com/cutting-garden/internal/command_components"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/cutting-garden/internal/trellis_eval"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/interfaces"
	"github.com/mattn/go-isatty"
)

// Organize is the value registered for the `organize` subcommand.
type Organize struct {
	// Query is the trellis query (RFC 0014) selecting the nodes to organize —
	// the part of the positional expression after the origin's `->` (RFC 0020
	// §4.1); empty means the anchor's enriched child listing.
	Query string
	// originSource is the positional expression's origin as written, echoed
	// into the document's provenance note.
	originSource string
	// removedQuery receives the retired -query flag so passing it is a usage
	// error that names the replacement, not an "unknown flag".
	removedQuery string
	// GroupBy is the grouping the document is built around (required to
	// generate), in the one spelling the `_group-by` directive and the dimension
	// heading share (native tags design G10): `(tags)` for the type's whole tag
	// set, a bare name for a tag namespace (`project`, RFC 0019), `<dim>=` for a
	// field, or `<dim>=(year|month|day)` for a date field at a bucket granularity
	// (cutting-garden#230; a bare `<dim>=` on a date field resolves the
	// `[organize] date_granularity` config default, then day).
	GroupBy string
	// Apply, when set, switches to apply mode: the path of an edited document
	// to merge and write, or "-" for stdin. The anchor, group-by, and query are
	// recovered from the document's directives, so no <uri> is re-passed.
	Apply string
	// CommitDirectly reads an edited document from stdin and applies it,
	// committing the writes (dodder's commit-directly mode) — the scripted
	// re-apply of a previously-generated dry-run document.
	CommitDirectly bool
	// Commit forces the writes on the non-interactive (piped/redirected) path,
	// where organize is dry-run by default so a headless invocation never writes
	// silently. At a terminal organize writes by default (confirming after the
	// #224 diff), so -commit is redundant there. Interim lever pending the
	// `%:dry-run` directive-in-doc (hyphence#14, cutting-garden#213).
	Commit bool
	// DryRun forces a preview — show the diff and exit without writing — even at a
	// terminal, the named inverse of the wet-run-by-default rule (#213).
	DryRun bool
	// IncludeTerminal drops organize's default exclusion of terminal/done
	// objects (cutting-garden#214): sugar for omitting the `_terminal=no` clause
	// the generated query otherwise carries.
	IncludeTerminal bool
	output          io.Writer
	// confirm, when set, replaces the terminal yes/no prompt (the #224 commit
	// gate and the refusal strip gate) — the test seam; nil uses huh.
	confirm func(title string) (bool, error)
}

var (
	_ command.Cmd                       = (*Organize)(nil)
	_ interfaces.CommandComponentWriter = (*Organize)(nil)
	_ command.CommandWithSeeAlso        = (*Organize)(nil)
)

// New constructs an Organize with output routed to os.Stdout.
func New() *Organize {
	return &Organize{output: os.Stdout}
}

// newWithOutput is the test-only constructor routing output to w.
func newWithOutput(output io.Writer) *Organize {
	return &Organize{output: output}
}

func (*Organize) GetDescription() command.Description {
	return command.Description{
		Short: "reorganize a plugin's nodes by editing a faceted document",
		Long: "Generates an editable document that groups a plugin's nodes " +
			"by a field (`status=`, `date_due=(month)`), its whole tag set " +
			"(`(tags)`), or a tag namespace (`project`), pinning the pre-edit state as a content-" +
			"addressed base; you move object lines between headings and apply " +
			"the result, which three-way-merges the edits against the base and " +
			"the re-queried live state and writes each move through the " +
			"plugin. At a terminal apply writes by default after confirming; " +
			"piped it is dry-run until \\-commit. See RFC 0015, FDR 0023.",
	}
}

func (*Organize) GetSeeAlso() []string {
	return []string{
		"cutting-garden(1)",
		"cutting-garden-fmt-organize(1)",
	}
}

func (cmd *Organize) SetFlagDefinitions(flagSet interfaces.CLIFlagDefinitions) {
	flagSet.StringVar(
		&cmd.removedQuery,
		"query",
		"",
		"REMOVED (RFC 0020): write the selection as the first argument instead, "+
			"`'<uri> -> <trellis query>'`; passing this flag is a usage error",
	)
	flagSet.StringVar(
		&cmd.GroupBy,
		"group-by",
		"",
		"grouping to build the document around (required to generate): `(tags)` "+
			"for the whole tag set, a bare tag namespace (`project` → rollup; quote "+
			"it if it contains `:` or `/`), `dim=` for a field (taken on trust when "+
			"the plugin declares no facet schema), or `dim=(year|month|day)` for a "+
			"date field at that granularity",
	)
	flagSet.StringVar(
		&cmd.Apply,
		"apply",
		"",
		"apply an edited document instead of generating one (path, or - for stdin)",
	)
	flagSet.BoolVar(
		&cmd.CommitDirectly,
		"commit-directly",
		false,
		"read an edited document from stdin and apply it, committing the writes",
	)
	flagSet.BoolVar(
		&cmd.Commit,
		"commit",
		false,
		"force writing on the piped/non-terminal path (dry-run by default there); "+
			"redundant at a terminal, where organize writes by default after confirming",
	)
	flagSet.BoolVar(
		&cmd.DryRun,
		"dry-run",
		false,
		"show the diff and exit without writing, even at a terminal",
	)
	flagSet.BoolVar(
		&cmd.IncludeTerminal,
		"include-terminal",
		false,
		"include terminal/done objects (default: excluded — organize is a "+
			"triage-the-active-work surface)",
	)
}

func (cmd *Organize) Run(req command.Request) {
	ctx := req.Context.(errors.Context)

	// Config load precedes both paths: the plugin (and any wire plugin) is
	// resolvable through the scheme registry only after registration, exactly
	// as in `list` (RFC 0013 §Host integration).
	//
	// The loaded value threads down to every path that reads a config lever
	// (the `[tags]` override, the `[organize]` defaults) instead of being
	// re-read there: the load INJECTS each plugin's section as it decodes
	// (RFC 0007), so a second read would re-parse the file and reassign every
	// plugin's package state for nothing.
	cfg, err := command_components.LoadAndInjectConfig(os.Stderr)
	if err != nil {
		errors.ContextCancelWithError(ctx, err)
		return
	}

	// Apply modes recover their anchor from the document, so they take no <uri>.
	if cmd.CommitDirectly && cmd.Apply != "" {
		errors.ContextCancelWithBadRequestf(ctx,
			"organize: -commit-directly reads the document from stdin; do not also "+
				"pass -apply")
		return
	}
	if cmd.CommitDirectly {
		if err := cmd.runCommitDirectly(ctx, cfg); err != nil {
			errors.ContextCancelWithError(ctx, err)
		}
		return
	}
	if cmd.Apply != "" {
		if err := cmd.runApply(ctx, cfg, cmd.Apply); err != nil {
			errors.ContextCancelWithError(ctx, err)
		}
		return
	}

	if cmd.removedQuery != "" {
		errors.ContextCancelWithBadRequestf(ctx,
			"organize: -query was removed; put the selection in the first "+
				"argument as one trellis expression: "+
				"`cg organize '<uri> -> %s' [group-by]` (RFC 0020 §4.1)",
			cmd.removedQuery)
		return
	}

	args := req.PeekArgs()
	switch {
	case len(args) == 0:
		errors.ContextCancelWithBadRequestf(ctx,
			"organize requires a selection to generate — a <uri>, or "+
				"`'<uri> -> <trellis query>'` (or --apply <path> to apply an "+
				"edited document)")
		return
	case len(args) > 2:
		errors.ContextCancelWithBadRequestf(ctx,
			"too many positional arguments; organize takes at most two "+
				"(<expression> [group-by]), trailing: %v", args[2:])
		return
	}

	selection, err := parseSelection(
		args[0],
		func() map[string][]command_components.NamedRoot {
			return command_components.AggregateRootNames(ctx, os.Stderr)
		},
	)
	if err != nil {
		errors.ContextCancelWithError(ctx, err)
		return
	}
	cmd.Query = selection.Query
	cmd.originSource = selection.OriginSource

	// A bare second positional is the grouping dimension — sugar for -group-by, so
	// `cg organize caldav:task priority` works (cutting-garden#216, the ergonomic
	// step toward a fully trellis selection surface). Giving both the positional
	// and the flag is an error.
	if len(args) == 2 {
		if cmd.GroupBy != "" {
			errors.ContextCancelWithBadRequestf(ctx,
				"organize: group-by given twice — positional %q and -group-by %q; use one",
				args[1], cmd.GroupBy)
			return
		}
		cmd.GroupBy = args[1]
	}

	if err := cmd.runGenerateOrInteractive(ctx, cfg, selection.Origin); err != nil {
		errors.ContextCancelWithError(ctx, err)
	}
}

// parseSelection reads organize's first positional as ONE trellis expression in
// origin-in-expression form (RFC 0020 §4.1): `<uri>` alone, or
// `<uri> -> <query>`, the origin naming the anchor and the remainder the
// selection within it.
//
// An argument with no whitespace cannot hold a combinator (they require
// surrounding whitespace), so it can only be an origin. If it does not parse as
// a trellis term it is taken as a literal URI: a URL carrying a reserved rune
// (`user@host` in a CalDAV path) stays usable unquoted, as it was when this
// argument was a plain <uri>.
//
// The origin may instead be a BOUND TYPE, `!<name>` (RFC 0020 §3.4, §4.2): a
// configured root's name, resolved through rootNames. Further terms in the same
// step — or a step after `->` — select within that root, so `!task
// priority=0_must` and `!task -> priority=0_must` mean the same.
func parseSelection(
	arg string, rootNames func() map[string][]command_components.NamedRoot,
) (trellis_eval.Selection, error) {
	if selection, matched, err := parseBoundTypeSelection(arg, rootNames); matched {
		return selection, err
	}

	selection, err := trellis_eval.SplitOrigin(arg)
	if err == nil {
		return selection, nil
	}
	if !strings.ContainsFunc(arg, unicode.IsSpace) {
		return trellis_eval.Selection{Origin: arg, OriginSource: strconv.Quote(arg)}, nil
	}
	return trellis_eval.Selection{}, errors.BadRequestf(
		"organize: selection %q: %s (expected `<uri>` or `<uri> -> <trellis query>`)",
		arg, err)
}

// parseBoundTypeSelection handles a selection that opens with a type term.
// matched is false when arg does not open with one, leaving it to the URI
// path; once it does, the outcome (a selection or an error) is final.
func parseBoundTypeSelection(
	arg string, rootNames func() map[string][]command_components.NamedRoot,
) (selection trellis_eval.Selection, matched bool, err error) {
	lead, perr := trellis.SplitLeadingTerm(arg)
	if perr != nil {
		return selection, false, nil
	}
	typ, ok := lead.Term.Basic.(trellis.TypeBasicTerm)
	if !ok {
		return selection, false, nil
	}
	name := typ.Type.Name
	if lead.Term.Negate || lead.Term.Exact || typ.Sigil != nil {
		return selection, true, errors.BadRequestf(
			"organize: selection %q: the root name `!%s` cannot be negated (`^`), "+
				"exact-matched (`=`) or carry a sigil", arg, name)
	}

	roots := rootNames()[name]
	switch len(roots) {
	case 0:
		return selection, true, errors.BadRequestf(
			"organize: selection %q: `!%s` is not a configured root name (a "+
				"selection opens with a <uri> or the `name` of a configured "+
				"account; a plugin node type cannot open one)", arg, name)
	case 1:
	default:
		where := make([]string, len(roots))
		for i, r := range roots {
			where[i] = fmt.Sprintf("%s (%s)", r.URL, strings.Join(r.Schemes, ","))
		}
		return selection, true, errors.BadRequestf(
			"organize: selection %q: the root name `!%s` is ambiguous — it names %s; "+
				"select one by its <uri>", arg, name, strings.Join(where, " and "))
	}

	query := lead.Rest
	if after, bridged := strings.CutPrefix(query, "->"); bridged &&
		(after == "" || unicode.IsSpace([]rune(after)[0])) {
		query = strings.TrimSpace(after)
		if query == "" {
			return selection, true, errors.BadRequestf(
				"organize: selection %q: expected a step after `->`", arg)
		}
	}
	if query != "" {
		if _, qerr := trellis.Parse(query); qerr != nil {
			return selection, true, errors.BadRequestf(
				"organize: selection %q: %s", arg, qerr)
		}
	}
	return trellis_eval.Selection{
		Origin:       roots[0].URL,
		OriginSource: lead.TermSource,
		Query:        query,
	}, true, nil
}

// runGenerateOrInteractive chooses the default behavior for a bare `organize
// <uri>` invocation: launch the interactive $EDITOR round-trip when stdout is a
// terminal, else print the document to stdout (the pipe/redirect and
// MCP/scripting path).
func (cmd *Organize) runGenerateOrInteractive(
	ctx errors.Context, cfg *cgconfig.ConfigV0, uriStr string,
) error {
	if stdoutIsTerminal() {
		return cmd.runInteractive(ctx, cfg, uriStr)
	}
	return cmd.runGenerate(ctx, cfg, uriStr)
}

// runInteractive generates the document into a temp file, opens it in the user's
// editor, and applies the result on save. An unchanged buffer is a no-op; a
// dry-run keeps the buffer and prints its path so it can be re-applied with
// -commit-directly; a committed apply removes it.
func (cmd *Organize) runInteractive(
	ctx errors.Context, cfg *cgconfig.ConfigV0, uriStr string,
) error {
	rendered, err := cmd.buildAndStore(ctx, cfg, uriStr)
	if err != nil {
		return err
	}

	f, err := os.CreateTemp("", "cg-organize-*.txt")
	if err != nil {
		return errors.Wrapf(err, "organize: create edit buffer")
	}
	tmpPath := f.Name()
	if _, werr := io.WriteString(f, rendered); werr != nil {
		_ = f.Close()
		return errors.Wrap(werr)
	}
	if cerr := f.Close(); cerr != nil {
		return errors.Wrap(cerr)
	}

	if err := launchEditor(ctx, tmpPath); err != nil {
		fmt.Fprintf(cmd.output, "organize: editor aborted; document left at %s\n", tmpPath)
		return err
	}

	editedBytes, err := os.ReadFile(tmpPath)
	if err != nil {
		return errors.Wrapf(err, "organize: read edited buffer")
	}
	if string(editedBytes) == rendered {
		_ = os.Remove(tmpPath)
		fmt.Fprintln(cmd.output, "organize: no changes; nothing to apply")
		return nil
	}

	// The interactive path is always a terminal (runInteractive is gated on it),
	// so it is wet-run by default (writes after the confirm gate) unless -dry-run
	// forces preview (cutting-garden#213), and the diff renders in color.
	// interactive is applyMode's, not a hard-coded true: -dry-run must never
	// prompt, and the separable-refusal gate (resolveRefusals) keys on it alone.
	commit, interactive := applyMode(cmd.DryRun, cmd.Commit, true)
	committed, landed, err := cmd.applyDocument(ctx, cfg, string(editedBytes), commit, interactive, true)
	if err != nil {
		// Keep the edited buffer so the user can resolve conflicts and
		// re-apply. Objects created before the failure get their real ids
		// written into the buffer (their temp ids would otherwise name
		// them as still-to-create; the ledger guards them either way).
		if len(landed) == 0 {
			fmt.Fprintf(cmd.output, "organize: edited document left at %s\n", tmpPath)
			return err
		}
		run := applyRun{landed: landed}
		rewritten, rerr := rewriteLandedTempIDs(string(editedBytes), run.landedByKey())
		if rerr == nil {
			rerr = os.WriteFile(tmpPath, []byte(rewritten), 0o600)
		}
		if rerr != nil {
			fmt.Fprintf(cmd.output,
				"organize: edited document left at %s (its temp ids for %s were NOT "+
					"rewritten: %s; re-applying it skips them — the creation ledger)\n",
				tmpPath, run.landedList(), errorText(rerr))
			return err
		}
		fmt.Fprintf(cmd.output,
			"organize: edited document left at %s, with the created objects' temp ids "+
				"rewritten to their real ids (%s), so re-applying it is safe\n",
			tmpPath, run.landedList())
		return err
	}
	if committed {
		_ = os.Remove(tmpPath)
		return nil
	}
	fmt.Fprintf(cmd.output,
		"\norganize: dry-run — no writes. Re-apply this edit with:\n"+
			"  cg organize -commit-directly < %s\n", tmpPath)
	return nil
}

// stdoutIsTerminal reports whether stdout is an interactive terminal, gating the
// interactive-by-default behavior (a pipe/redirect or non-TTY consumer gets the
// plain generate-to-stdout path).
func stdoutIsTerminal() bool {
	fd := os.Stdout.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}
