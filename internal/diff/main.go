// Package diff wires the `diff` subcommand: walk a directory and
// compare its contents against a receipt's entries. File content
// blob-ids are recomputed in the receipt's source-store hash family
// via pkgs/blob_stores.NewDiscardBlobStore — no bytes persisted.
//
// Positional surface:
//
//	diff [-store STORE_ID] [-verify-blobs-exist] [-color auto|always|never] RECEIPT_ID DIR
//
// Semantics are normative per FDR 0002 (docs/features/0002-diff.md).
// Cross-command glue (receipt fetch, store-hint resolution, type-tag
// guard, env wiring) lives in internal/command_components; this
// package holds only diff-specific dispatch + the per-entry
// comparator.
package diff

import (
	"fmt"
	"io"
	"os"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/cutting-garden/internal/command_components"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/madder/go/pkgs/markl"
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

	if err := cmd.runDiff(ctx, receiptIDStr, dir); err != nil {
		errors.ContextCancelWithError(ctx, err)
	}
}

// runDiff implements the cmd in five phases: resolve the dir plugin
// and validate the directory exists, fetch and parse the receipt
// blob (including type-tag and store-hint resolution), walk <dir>
// against the receipt's entries through a discard blob store, and
// (optionally) probe the source store for missing blobs.
//
// Phase 4 step 3 ships the first half: validate, fetch, resolve.
// Steps 4-5 wire the walk + comparison + probe.
func (cmd *Diff) runDiff(
	ctx errors.Context,
	receiptIDStr, dirStr string,
) error {
	dirURL, plugin, err := command_components.ResolveDiffPlugin(dirStr)
	if err != nil {
		return err
	}

	if err := plugin.ValidateDiffDir(dirURL, dirStr); err != nil {
		return err
	}

	var receiptID markl.Id
	if err := receiptID.Set(receiptIDStr); err != nil {
		return errors.Wrapf(err, "parse receipt-id %q", receiptIDStr)
	}

	envBlobStore := command_components.MakeBlobStoreEnv(ctx)

	blob, typeTag, err := command_components.ReadReceiptBlob(
		envBlobStore, &receiptID, cmd.Store,
	)
	if err != nil {
		return err
	}

	if err := command_components.CheckReceiptTypeTag(
		&receiptID, typeTag, plugin, dirURL, "diff",
	); err != nil {
		return err
	}

	v1, ok := blob.(*capture_receipt.V1)
	if !ok {
		return errors.ErrorWithStackf(
			"receipt %s: unexpected blob shape %T (expected *V1)",
			&receiptID, blob)
	}

	sourceStore, err := command_components.ResolveMaterializationStore(
		envBlobStore, v1.Hint, cmd.Store, cmd.diagnostics,
	)
	if err != nil {
		return err
	}

	// Recompute file content blob-ids in the source store's hash
	// family WITHOUT persisting (FDR §Behavior phase 5). The
	// discard store's MakeBlobWriter digests every byte but writes
	// it nowhere.
	discardStore := blob_stores.NewDiscardBlobStore(
		sourceStore.GetDefaultHashType(),
	)

	diskEntries, scanErr := plugin.ScanForDiff(
		cutting_garden_plugins.DiffScanRequest{
			Context:        ctx,
			Dir:            dirURL,
			RawDir:         dirStr,
			BlobStore:      discardStore,
			ReceiptEntries: v1.Entries,
		})
	if scanErr != nil {
		return scanErr
	}

	var missingBlobs map[string]string
	if cmd.VerifyBlobsExist {
		missingBlobs, err = probeMissingBlobs(sourceStore, v1.Entries)
		if err != nil {
			return err
		}
	}

	differences := compareEntries(v1.Entries, diskEntries, missingBlobs)

	renderer, err := newDiffRenderer(cmd.Color, os.Stdout)
	if err != nil {
		return err
	}
	for _, line := range differences {
		fmt.Fprintln(os.Stdout, renderDiffLine(renderer, line))
	}

	if len(differences) > 0 {
		fmt.Fprintf(cmd.diagnostics, "diff: %d %s\n",
			len(differences), pluralize("difference", "differences", len(differences)))
		return command.Mismatchf(
			"tree differs from receipt: %d %s",
			len(differences),
			pluralize("entry", "entries", len(differences)))
	}

	return nil
}

// pluralize returns singular when n == 1, plural otherwise. Used in
// the "diff: N differences" tally and the "tree differs from receipt:
// N entries" error.
func pluralize(singular, plural string, n int) string {
	if n == 1 {
		return singular
	}
	return plural
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
