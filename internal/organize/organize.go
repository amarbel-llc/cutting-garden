// Package organize wires the `organize` subcommand: generate a faceted,
// editable document from a plugin's nodes, then apply the edits back as
// substrate writes — dodder's organize upstreamed and generalized across
// plugins (FDR 0023, RFC 0015).
//
// Two modes on one command:
//
//	organize <uri> --group-by <facet> [--query <trellis>]   generate a document
//	organize --apply <path> [--commit]                      apply an edited one
//
// Generate selects the anchor's nodes (a trellis query, or the enriched child
// listing), groups them by the --group-by facet dimension, pins the pre-edit
// assignment as a content-addressed organize-base-v1 blob, and prints the
// document with a `- _base=@<digest>` line. You move object lines between
// headings and feed the result back with --apply, which three-way-merges your
// edits against the pinned base and the re-queried live state and writes each
// bucket move through the plugin's NodeMutator. Apply is dry-run by default
// (prints the intended writes); --commit performs them.
//
// This is the FDR 0023 caldav tracer bullet: the writable dimension it exercises
// end-to-end is `status` (a passthrough enum). Date reschedule-by-move, which
// needs plugin-side bucket->value completion, arrives with the FacetWriteApplier
// verb (Slice 2b). The document dialect (internal/organize/document.go) is a
// deliberately minimal subset of RFC 0015, sufficient to prove the pipeline.
package organize

import (
	"io"
	"os"

	"code.linenisgreat.com/cutting-garden/internal/command"
	"code.linenisgreat.com/cutting-garden/internal/command_components"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/interfaces"
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
	// Commit performs the writes; the default is a dry-run that prints the
	// intended moves and touches nothing.
	Commit bool
	output io.Writer
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
		&cmd.Commit,
		"commit",
		false,
		"write the moves through to the substrate (default: dry-run)",
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

	// Apply mode recovers its anchor from the document, so it takes no <uri>.
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
	case len(args) > 1:
		errors.ContextCancelWithBadRequestf(ctx,
			"too many positional arguments; organize takes at most one (<uri>), "+
				"trailing: %v", args[1:])
	default:
		if err := cmd.runGenerate(ctx, args[0]); err != nil {
			errors.ContextCancelWithError(ctx, err)
		}
	}
}
