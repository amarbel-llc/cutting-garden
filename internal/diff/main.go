// Package diff wires the `diff` subcommand: walk a directory and
// compare its contents against a receipt's entries. File content
// blob-ids are recomputed in the receipt's source-store hash family
// via pkgs/blob_stores.NewDiscardBlobStore — no bytes persisted.
//
// Positional surface:
//
//	diff [-store STORE_ID] [-verify-blobs-exist] [-color auto|always|never] RECEIPT_ID DIR
//
// Semantics are normative per FDR 0006 (docs/features/0006-diff.md
// upstream). Cross-command glue (receipt fetch, store-hint
// resolution, type-tag guard, env wiring) lives in
// internal/command_components; this package holds only diff-specific
// dispatch + the per-entry comparator (steps 4+).
//
// Phase 4 status: step 2 ships the cmd skeleton only — arg + flag
// parsing, color-value validation, registration. Receipt fetch lands
// in step 3, comparison in step 4, -verify-blobs-exist in step 5,
// color output in step 6, bats coverage in step 7.
package diff

import (
	"io"
	"os"

	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/purse-first/libs/dewey/0/interfaces"
	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/errors"
)

// Color flag values per FDR §Flags.
const (
	colorAuto   = "auto"
	colorAlways = "always"
	colorNever  = "never"
)

// Diff is the value registered for the `diff` subcommand.
//
// Store mirrors Restore.Store: when non-empty, overrides the
// receipt's store-hint resolution per FDR 0001 §Store-Hint Resolution
// branch 1.
//
// VerifyBlobsExist toggles the receipt-vs-store check on top of the
// default tree-vs-receipt comparison (FDR §Receipt-vs-store probe).
// Off by default because it adds one HasBlob round-trip per file
// entry, which is meaningful on remote (e.g. SFTP) stores.
//
// Color toggles ANSI SGR coloring of per-line markers per FDR §Flags.
// Validated in Run; auto means "ANSI when stdout is a TTY and
// NO_COLOR is unset" (the default lipgloss renderer behavior).
//
// diagnostics is the writer FDR §Store-Hint Resolution diagnostic
// lines route to; matches Restore.diagnostics's shape.
type Diff struct {
	Store            string
	VerifyBlobsExist bool
	Color            string
	diagnostics      io.Writer
}

var (
	_ command.Cmd                       = (*Diff)(nil)
	_ interfaces.CommandComponentWriter = (*Diff)(nil)
)

// New constructs a Diff with default flag values (Color defaults to
// "auto" per FDR §Flags); diagnostics route to os.Stderr.
func New() *Diff {
	return &Diff{
		Color:       colorAuto,
		diagnostics: os.Stderr,
	}
}

// newWithDiagnostics is the test-only constructor that routes
// diagnostic output to the supplied writer.
func newWithDiagnostics(diagnostics io.Writer) *Diff {
	return &Diff{
		Color:       colorAuto,
		diagnostics: diagnostics,
	}
}

func (*Diff) GetDescription() command.Description {
	return command.Description{
		Short: "compare a directory tree against a capture receipt",
	}
}

func (cmd *Diff) SetFlagDefinitions(
	flagSet interfaces.CLIFlagDefinitions,
) {
	flagSet.StringVar(
		&cmd.Store,
		"store",
		"",
		"explicit blob-store-id to resolve the receipt against "+
			"(overrides the receipt's store-hint resolution)",
	)
	flagSet.BoolVar(
		&cmd.VerifyBlobsExist,
		"verify-blobs-exist",
		false,
		"probe the resolved source store for every receipt file "+
			"entry's blob and emit a B line on miss. Off by default "+
			"because it adds one HasBlob round-trip per file entry",
	)
	flagSet.StringVar(
		&cmd.Color,
		"color",
		colorAuto,
		"ANSI SGR coloring of diff markers (A/D/M/T/B): "+
			"auto (on when stdout is a TTY and NO_COLOR is unset), "+
			"always, or never",
	)
}

func (cmd *Diff) Run(req command.Request) {
	ctx := req.Context.(errors.Context)

	if err := validateColor(cmd.Color); err != nil {
		errors.ContextCancelWithBadRequestf(ctx, "%s", err.Error())
		return
	}

	receiptIDStr := req.PopArg("receipt-id")
	dir := req.PopArg("dir")

	if req.RemainingArgCount() > 0 {
		errors.ContextCancelWithBadRequestf(ctx,
			"too many positional arguments; diff takes exactly two "+
				"(<receipt-id> <dir>), trailing: %v", req.PeekArgs())
		return
	}

	// Step 3 wires receipt fetch + dir validation here. Step 4 adds
	// the walk + comparison. Step 5 adds -verify-blobs-exist. Step 6
	// adds color rendering. The args are consumed so the framework
	// records them in the request's audit trail.
	_ = receiptIDStr
	_ = dir

	errors.ContextCancelWithBadRequestf(ctx,
		"diff: not yet implemented (Phase 4 step 2 skeleton; "+
			"receipt fetch lands in step 3, comparison in step 4)")
}

// validateColor enforces the FDR §Flags constraint on -color values.
// Returns nil for the three allowed values, an error otherwise.
func validateColor(value string) error {
	switch value {
	case colorAuto, colorAlways, colorNever:
		return nil
	}
	return errors.ErrorWithStackf(
		"invalid -color value %q; expected auto, always, or never",
		value,
	)
}
