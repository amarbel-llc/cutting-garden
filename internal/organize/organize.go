// Package organize wires the `organize` subcommand: generate a faceted,
// editable document from a plugin's nodes, then apply the edits back as
// substrate writes — dodder's organize upstreamed and generalized across
// plugins (FDR 0023, RFC 0015).
//
// Invocation shapes:
//
//	organize <uri> <group-by> [--query <trellis>]           interactive (TTY) / generate (pipe)
//	  (<group-by> may be positional as shown, or the --group-by <facet> flag)
//	organize --apply <path> [--commit]                      apply an edited document
//	organize --commit-directly < doc                        apply from stdin, committing
//
// A bare `organize <uri>` on a terminal generates the document into a temp file,
// opens it in $EDITOR, and applies the result on save (dodder's interactive
// default); with stdout piped/redirected it just prints the document (the
// MCP/scripting path). Generate selects the anchor's nodes (a trellis query, or
// the enriched child listing), groups them by the --group-by facet dimension,
// pins the pre-edit assignment as a content-addressed organize-base-v1 blob, and
// emits the document with a `- _base=@<digest>` line. You move object lines
// between headings; apply three-way-merges your edits against the pinned base and
// the re-queried live state and writes each move through the plugin's NodeMutator.
// Both paths render the change set as a per-object word-diff first (cutting-garden
// #224); apply is dry-run by default (shows the diff and touches nothing) and
// --commit performs the writes (the interim lever pending the `%:dry-run`
// directive, hyphence#14), an interactive commit confirming after the diff.
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

	"code.linenisgreat.com/cutting-garden/internal/command"
	"code.linenisgreat.com/cutting-garden/internal/command_components"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/interfaces"
	"github.com/mattn/go-isatty"
)

// Organize is the value registered for the `organize` subcommand.
type Organize struct {
	// Query is an optional trellis query (RFC 0014) selecting the nodes to
	// organize; empty means the anchor's enriched child listing.
	Query string
	// GroupBy is the facet dimension the document groups by (required to
	// generate).
	GroupBy string
	// Apply, when set, switches to apply mode: the path of an edited document
	// to merge and write, or "-" for stdin. The anchor, group-by, and query are
	// recovered from the document's directives, so no <uri> is re-passed.
	Apply string
	// CommitDirectly reads an edited document from stdin and applies it,
	// committing the writes (dodder's commit-directly mode) — the scripted
	// re-apply of a previously-generated dry-run document.
	CommitDirectly bool
	// Commit performs the writes; the default is a dry-run that prints the
	// intended moves and touches nothing. It is the interim dry-run/commit lever
	// pending the `%:dry-run` directive-in-doc (hyphence#14).
	Commit bool
	// IncludeTerminal drops organize's default exclusion of terminal/done
	// objects (cutting-garden#214): sugar for omitting the `_terminal=no` clause
	// the generated query otherwise carries.
	IncludeTerminal bool
	output          io.Writer
}

var (
	_ command.Cmd                       = (*Organize)(nil)
	_ interfaces.CommandComponentWriter = (*Organize)(nil)
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
			"by a facet dimension, pinning the pre-edit state as a content-" +
			"addressed base; you move object lines between headings and apply " +
			"the result, which three-way-merges the edits against the base and " +
			"the re-queried live state and writes each move through the " +
			"plugin. Apply is a dry-run until \\-commit. See RFC 0015, FDR 0023.",
	}
}

func (cmd *Organize) SetFlagDefinitions(flagSet interfaces.CLIFlagDefinitions) {
	flagSet.StringVar(
		&cmd.Query,
		"query",
		"",
		"trellis query selecting the nodes to organize (RFC 0014; optional)",
	)
	flagSet.StringVar(
		&cmd.GroupBy,
		"group-by",
		"",
		"facet dimension to group the nodes by (required to generate)",
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
		"write the moves through to the substrate (default: dry-run)",
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
	if err := command_components.LoadAndInjectConfig(os.Stderr); err != nil {
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
		if err := cmd.runCommitDirectly(ctx); err != nil {
			errors.ContextCancelWithError(ctx, err)
		}
		return
	}
	if cmd.Apply != "" {
		if err := cmd.runApply(ctx, cmd.Apply); err != nil {
			errors.ContextCancelWithError(ctx, err)
		}
		return
	}

	args := req.PeekArgs()
	switch {
	case len(args) == 0:
		errors.ContextCancelWithBadRequestf(ctx,
			"organize requires a <uri> to generate (or --apply <path> to apply "+
				"an edited document)")
		return
	case len(args) > 2:
		errors.ContextCancelWithBadRequestf(ctx,
			"too many positional arguments; organize takes at most two "+
				"(<uri> [group-by]), trailing: %v", args[2:])
		return
	}

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

	if err := cmd.runGenerateOrInteractive(ctx, args[0]); err != nil {
		errors.ContextCancelWithError(ctx, err)
	}
}

// runGenerateOrInteractive chooses the default behavior for a bare `organize
// <uri>` invocation: launch the interactive $EDITOR round-trip when stdout is a
// terminal, else print the document to stdout (the pipe/redirect and
// MCP/scripting path).
func (cmd *Organize) runGenerateOrInteractive(ctx errors.Context, uriStr string) error {
	if stdoutIsTerminal() {
		return cmd.runInteractive(ctx, uriStr)
	}
	return cmd.runGenerate(ctx, uriStr)
}

// runInteractive generates the document into a temp file, opens it in the user's
// editor, and applies the result on save. An unchanged buffer is a no-op; a
// dry-run keeps the buffer and prints its path so it can be re-applied with
// -commit-directly; a committed apply removes it.
func (cmd *Organize) runInteractive(ctx errors.Context, uriStr string) error {
	rendered, err := cmd.buildAndStore(ctx, uriStr)
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
	// so the diff renders in color.
	committed, err := cmd.applyDocument(ctx, string(editedBytes), cmd.Commit, true, true)
	if err != nil {
		// Keep the edited buffer so the user can resolve conflicts and re-apply.
		fmt.Fprintf(cmd.output, "organize: edited document left at %s\n", tmpPath)
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
