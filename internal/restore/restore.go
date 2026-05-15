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
// Phase 3 status: steps 3-4 wire the happy path through plugin.Restore
// using -store-or-default for materialization plus the cross-scheme
// type-tag guard. The FDR §Store-Hint Resolution decision tree (drift
// checks, fallback notices) lands in step 5; rich sanitization
// diagnostics land in step 6.
package restore

import (
	"net/url"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/cgenv"
	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/blob_store_env"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
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
type Restore struct {
	Store string
}

var (
	_ command.Cmd                       = (*Restore)(nil)
	_ interfaces.CommandComponentWriter = (*Restore)(nil)
)

// New constructs a Restore with default flag values.
func New() *Restore {
	return &Restore{}
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
// the receipt blob, then dispatch to plugin.Restore (which handles
// sanitization and per-type materialization).
//
// Step 3 uses -store-or-default for materialization; the FDR
// §Store-Hint Resolution decision tree is wired in step 5.
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

	materializationStore, err := selectMaterializationStore(envBlobStore, cmd.Store)
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

// selectMaterializationStore picks the store entry blobs are read
// against. Step 3 implements the two trivial branches:
//
//   - storeOverride non-empty → resolve that store directly.
//   - storeOverride empty     → use the active default store.
//
// Step 5 replaces this with the full FDR §Store-Hint Resolution
// decision tree (hint matches → use hinted; drift → refuse; hint
// missing → fall back with notice; no hint → fall back with notice).
func selectMaterializationStore(
	envBlobStore blob_store_env.BlobStoreEnv,
	storeOverride string,
) (blob_stores.BlobStoreInitialized, error) {
	if storeOverride != "" {
		return resolveStoreByID(envBlobStore, storeOverride)
	}
	return envBlobStore.GetDefaultBlobStore(), nil
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
