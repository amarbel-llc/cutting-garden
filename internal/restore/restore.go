// Package restore wires the `restore` subcommand: parse a receipt
// blob, validate destination preconditions and per-entry path
// sanitization, then materialize the captured tree onto disk.
//
// Positional surface:
//
//	restore [-store STORE_ID] RECEIPT_ID DEST
//
// Semantics are normative per FDR 0001 (docs/features/0001-restore.md)
// and RFC 0001 §Consumer Rules. Cross-command glue
// (receipt fetch, store-hint resolution, type-tag guard, env wiring)
// lives in internal/command_components; this package holds only
// restore-specific dispatch.
package restore

import (
	"io"
	"os"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/cutting-garden/internal/command_components"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/markl"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// Restore is the value registered for the `restore` subcommand.
//
// Store is bound to the `-store` flag by SetFlagDefinitions; when
// non-empty it overrides the receipt's store-hint resolution per FDR
// §Store-Hint Resolution branch 1.
//
// diagnostics is the writer FDR §Store-Hint Resolution and
// §Sanitization diagnostic lines route to. Defaults to os.Stderr in
// New(); tests use newWithDiagnostics to inject a bytes.Buffer.
type Restore struct {
	Store       string
	diagnostics io.Writer
}

var (
	_ command.Cmd                       = (*Restore)(nil)
	_ interfaces.CommandComponentWriter = (*Restore)(nil)
)

// New constructs a Restore with default flag values; diagnostics
// route to os.Stderr.
func New() *Restore {
	return &Restore{diagnostics: os.Stderr}
}

// newWithDiagnostics is the test-only constructor that routes
// diagnostic output to the supplied writer.
func newWithDiagnostics(diagnostics io.Writer) *Restore {
	return &Restore{diagnostics: diagnostics}
}

func (*Restore) GetDescription() command.Description {
	return command.Description{
		Short: "restore a captured tree from a receipt blob",
	}
}

func (cmd *Restore) SetFlagDefinitions(
	flagSet interfaces.CLIFlagDefinitions,
) {
	flagSet.StringVar(
		&cmd.Store,
		"store",
		"",
		"explicit blob-store-id to resolve the receipt and entry blobs "+
			"against (overrides the receipt's store-hint resolution)",
	)
}

func (cmd *Restore) Run(req command.Request) {
	ctx := req.Context.(errors.Context)

	receiptIDStr := req.PopArg("receipt-id")
	destStr := req.PopArg("dest")

	if req.RemainingArgCount() > 0 {
		errors.ContextCancelWithBadRequestf(ctx,
			"too many positional arguments; restore takes exactly two "+
				"(<receipt-id> <dest>), trailing: %v", req.PeekArgs())
		return
	}

	if err := cmd.runRestore(ctx, receiptIDStr, destStr); err != nil {
		errors.ContextCancelWithError(ctx, err)
	}
}

// runRestore implements the cmd in three phases: resolve the dest
// plugin and validate the destination preconditions, fetch and parse
// the receipt blob (including type-tag and store-hint resolution),
// then dispatch to plugin.Restore (which handles sanitization and
// per-type materialization).
func (cmd *Restore) runRestore(
	ctx errors.Context,
	receiptIDStr, destStr string,
) error {
	destURL, plugin, err := command_components.ResolveRestorePlugin(destStr)
	if err != nil {
		return err
	}

	if err := plugin.ValidateDest(destURL, destStr); err != nil {
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
		&receiptID, typeTag, plugin, destURL, "restore",
	); err != nil {
		return err
	}

	v1, ok := blob.(*capture_receipt.V1)
	if !ok {
		return errors.ErrorWithStackf(
			"receipt %s: unexpected blob shape %T (expected *V1)",
			&receiptID, blob)
	}

	materializationStore, err := command_components.ResolveMaterializationStore(
		envBlobStore, v1.Hint, cmd.Store, cmd.diagnostics,
	)
	if err != nil {
		return err
	}

	return plugin.Restore(cutting_garden_plugins.RestoreRequest{
		Context:   ctx,
		Entries:   v1.Entries,
		BlobStore: materializationStore,
		Dest:      destURL,
		RawDest:   destStr,
	})
}
