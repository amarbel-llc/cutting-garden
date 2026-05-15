// Package restore wires the `restore` subcommand: parse a receipt
// blob, validate destination preconditions and per-entry path
// sanitization, then materialize the captured tree onto disk.
//
// Positional surface:
//
//	restore [-store STORE_ID] RECEIPT_ID DEST
//
// Semantics are normative per FDR 0001 (docs/features/0001-restore.md
// upstream) and RFC 0003 §Consumer Rules.
//
// Phase 3 status: steps 3-5 wire the happy path through plugin.Restore
// with full FDR §Store-Hint Resolution (drift checks, fallback notices,
// -store override) plus the cross-scheme type-tag guard. Rich
// sanitization diagnostic shapes lock down in step 6.
package restore

import (
	"io"
	"net/url"
	"os"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/cgenv"
	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/ids"
	"github.com/amarbel-llc/madder/go/pkgs/markl"
	"github.com/amarbel-llc/purse-first/libs/dewey/0/interfaces"
	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/errors"
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
	destURL, plugin, err := resolveRestorePlugin(destStr)
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

	envBlobStore := cgenv.MakeBlobStoreEnv(ctx)

	blob, typeTag, err := readReceiptBlob(envBlobStore, &receiptID, cmd.Store)
	if err != nil {
		return err
	}

	if err := checkReceiptTypeTag(&receiptID, typeTag, plugin, destURL); err != nil {
		return err
	}

	v1, ok := blob.(*capture_receipt.V1)
	if !ok {
		return errors.ErrorWithStackf(
			"receipt %s: unexpected blob shape %T (expected *V1)",
			&receiptID, blob)
	}

	materializationStore, err := resolveMaterializationStore(
		envBlobStore, v1.Hint, cmd.Store, cmd.diagnostics,
	)
	if err != nil {
		return err
	}

	return plugin.Restore(cutting_garden_plugins.RestoreRequest{
		Entries:   v1.Entries,
		BlobStore: materializationStore,
		Dest:      destURL,
		RawDest:   destStr,
	})
}

// checkReceiptTypeTag refuses a receipt whose wire-format type-tag
// does not match the dest plugin's TypeTag(). The file plugin
// accepts only `cutting_garden-capture_receipt-fs-v1`; an s3 or
// sftp plugin would accept its own segment.
//
// Cross-scheme restore (e.g. fs receipt → s3 dest) is a real future
// case (mirror a captured tree without local materialization), but
// the v1 strict guard is the safe default until the policy lands.
// Decision tracked at cutting-garden#18 — when it resolves, this
// function becomes the dispatch point for whatever policy is chosen
// (-allow-cross-scheme flag, per-plugin AcceptsReceiptTag, or
// relax-entirely).
func checkReceiptTypeTag(
	receiptID *markl.Id,
	typeTag ids.TypeStruct,
	plugin cutting_garden_plugins.RestorePlugin,
	destURL *url.URL,
) error {
	if typeTag.StringSansOp() == plugin.TypeTag() {
		return nil
	}
	return errors.ErrorWithStackf(
		"receipt %s: type-tag %q cannot be restored to scheme %q "+
			"(plugin tag %q); cross-scheme restore is not supported "+
			"(cutting-garden#18)",
		receiptID, typeTag.StringSansOp(), destURL.Scheme, plugin.TypeTag())
}
