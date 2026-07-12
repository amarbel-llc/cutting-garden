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
	"net/url"
	"os"

	"code.linenisgreat.com/cutting-garden/internal/capture_plugin"
	"code.linenisgreat.com/cutting-garden/internal/capture_receipt"
	"code.linenisgreat.com/cutting-garden/internal/command"
	"code.linenisgreat.com/cutting-garden/internal/command_components"
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/piggy/go/pkgs/markl"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"
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
	var receiptID markl.Id
	if err := receiptID.Set(receiptIDStr); err != nil {
		return errors.Wrapf(err, "parse receipt-id %q", receiptIDStr)
	}

	envBlobStore := command_components.MakeBlobStoreEnv(ctx)

	store, err := command_components.LocateReceiptStore(
		envBlobStore, &receiptID, cmd.Store,
	)
	if err != nil {
		return err
	}

	typeStr, err := command_components.PeekReceiptType(store, &receiptID)
	if err != nil {
		return err
	}

	// RFC 0002 protocol receipts route by capture kind — the receipt,
	// not the destination, decides how the capture is rebuilt. fs-v1
	// receipts fall through to the EntryV1 path below.
	if kind, ok := capture_plugin.KindFromReceiptType(typeStr); ok {
		pp, err := cutting_garden_plugins.ResolveProtocolRestore(kind)
		if err != nil {
			return err
		}
		destURL, err := url.Parse(destStr)
		if err != nil {
			return errors.Wrapf(err, "parse dest %q", destStr)
		}
		return pp.RestoreProtocol(cutting_garden_plugins.ProtocolRestoreRequest{
			Context:       ctx,
			BlobStore:     store,
			ReceiptDigest: receiptIDStr,
			Dest:          destURL,
			RawDest:       destStr,
		})
	}

	destURL, plugin, err := command_components.ResolveRestorePlugin(destStr)
	if err != nil {
		return err
	}

	if err := plugin.ValidateDest(destURL, destStr); err != nil {
		return err
	}

	blob, typeTag, err := capture_receipt.Read(store, &receiptID)
	if err != nil {
		return errors.Wrapf(err, "read receipt %s", &receiptID)
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
			&receiptID, blob,
		)
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
