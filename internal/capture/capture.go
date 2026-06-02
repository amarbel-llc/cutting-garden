// Package capture wires the `capture` subcommand: walk one or more
// directories and write every regular file as a blob, then emit one
// receipt blob per store group.
//
// Positional surface (after step 6):
//
//	capture [STORE_ID | DIR]...
//
// Args are classified left-to-right. A DIR arg appends a root to the
// current group; a STORE_ID arg flushes the current group and starts a
// new one targeting that store. Zero args captures `.` into the default
// store; a single STORE_ID arg captures `.` into that store. Roots
// belonging to the same group share a destination store and fold into a
// single receipt.
//
// The `--format=auto|tap|json` flag selects the per-record event sink
// (NDJSON or TAP). Audit log (step 7), store-hint metadata (step 8),
// and bats coverage (step 9) are still pending.
package capture

import (
	"os"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/capture_sink"
	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/cutting-garden/internal/command_components"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/madder/go/pkgs/output_format"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"
)

// Capture is the value registered for the `capture` subcommand.
//
// Format is bound to the `--format` CLI flag by SetFlagDefinitions.
// A pointer receiver on Run is required so the parsed flag value
// reaches the dispatch site.
type Capture struct {
	Format output_format.Format
}

var (
	_ command.Cmd                       = (*Capture)(nil)
	_ interfaces.CommandComponentWriter = (*Capture)(nil)
)

// New constructs a Capture with its flag fields initialized to
// defaults — matches the madder capture cmd's `Format:
// output_format.Default` shape.
func New() *Capture {
	return &Capture{Format: output_format.Default}
}

func (*Capture) GetDescription() command.Description {
	return command.Description{
		Short: "capture one or more directory trees into madder's blob stores",
	}
}

func (cmd *Capture) SetFlagDefinitions(
	flagSet interfaces.CLIFlagDefinitions,
) {
	flagSet.Var(&cmd.Format, "format", output_format.FlagDescription)
}

func (cmd *Capture) Run(req command.Request) {
	ctx := req.Context.(errors.Context)

	args := req.PopArgs()
	envBlobStore := command_components.MakeBlobStoreEnv(ctx)
	shadowCandidates := blobStoreIds(envBlobStore.GetBlobStores())

	groups, classifyFails, planErr := planCapture(args, shadowCandidates)

	var sink capture_sink.Sink
	switch cmd.Format.Resolve(os.Stdout) {
	case output_format.FormatTAP:
		sink = capture_sink.NewTAP(os.Stdout)
	default:
		sink = capture_sink.NewNDJSON(os.Stdout, os.Stderr)
	}
	defer sink.Finalize()

	failCount := 0
	var captureLogEntries []captureLogEntry

	for _, cf := range classifyFails {
		sink.Failure(cf.arg, cf.err)
		failCount++
	}

	if planErr != nil {
		sink.Failure("(arguments)", planErr)
		errors.ContextCancelWithBadRequestf(ctx, "%s", planErr.Error())
		return
	}

	for _, group := range groups {
		if group.switchNotice != "" {
			sink.Notice("%s", group.switchNotice)
		}

		var blobStore blob_stores.BlobStoreInitialized
		var storeName string
		if group.storeID.IsEmpty() {
			blobStore = envBlobStore.GetDefaultBlobStore()
		} else {
			blobStore = envBlobStore.GetBlobStore(group.storeID)
			storeName = group.storeID.String()
		}

		sink.SetStore(storeName)

		var entries []capture_receipt.EntryV1
		entryRoots := 0
		protocolReceipts := 0

		for _, root := range group.roots {
			if root.shadowNotice != "" {
				sink.Notice("%s", root.shadowNotice)
			}

			// RFC 0002 plugins emit their own self-contained receipt
			// merkle tree per root rather than folding EntryV1 records
			// into the shared store-group receipt below.
			if pp, ok := root.plugin.(cutting_garden_plugins.ProtocolCapturePlugin); ok {
				res, perr := pp.CaptureProtocol(cutting_garden_plugins.ProtocolCaptureRequest{
					Context:   ctx,
					Source:    root.sourceURL,
					RawArg:    root.path,
					BlobStore: blobStore,
				})
				if perr != nil {
					sink.Failure(root.path, perr)
					failCount++
					continue
				}
				sink.StoreGroupReceipt(res.ReceiptDigest, res.ObjectCount)
				protocolReceipts++
				captureLogEntries = append(captureLogEntries, captureLogEntry{
					Ts:        captureLogTimestamp(),
					ReceiptID: res.ReceiptDigest,
					StoreID:   storeName,
					Roots:     []string{root.path},
				})
				continue
			}

			result := root.plugin.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
				Context:   ctx,
				Source:    root.sourceURL,
				RawArg:    root.path,
				BlobStore: blobStore,
				Sink:      sink,
			})
			entries = append(entries, result.Entries...)
			entryRoots++
			failCount += result.FailCount
		}

		// Collapse Root to "." when the EntryV1 entries came from a
		// single root per RFC 0001 §Root Encoding. Multi-root groups
		// keep distinct Root values.
		if entryRoots == 1 {
			for i := range entries {
				entries[i].Root = "."
			}
		}

		if len(entries) == 0 {
			// Protocol roots already emitted their own receipts; only
			// warn about a skipped store-group receipt when there was
			// EntryV1 work that produced nothing.
			if protocolReceipts == 0 {
				sink.Notice(
					"notice: no entries captured for store=%s; receipt skipped",
					quoteEmpty(storeName),
				)
			}
			continue
		}

		// Empty storeName (default store) → resolve to its actual id
		// per RFC 0001 §Store-Hint Resolution.
		effectiveStoreId := storeName
		if effectiveStoreId == "" {
			effectiveStoreId = envBlobStore.GetDefaultBlobStoreId()
		}

		hint, hintErr := capture_receipt.ComputeStoreHint(blobStore, effectiveStoreId)
		if hintErr != nil {
			sink.Notice(
				"notice: omitting store-hint for store=%s: %v",
				quoteEmpty(storeName), hintErr,
			)
		}

		receiptID, err := writeReceipt(blobStore, entries, hint)
		if err != nil {
			sink.Failure("(receipt)", err)
			failCount++
			continue
		}
		sink.StoreGroupReceipt(receiptID, len(entries))

		captureLogEntries = append(captureLogEntries, captureLogEntry{
			Ts:        captureLogTimestamp(),
			ReceiptID: receiptID,
			StoreID:   storeName,
			Roots:     rootPaths(group.roots),
		})
	}

	if len(captureLogEntries) > 0 {
		appendCaptureLog(command_components.MakeCgEnvDir(ctx), sink, captureLogEntries)
	}

	if failCount > 0 {
		errors.ContextCancelWithBadRequestf(ctx,
			"capture failed entries: %d", failCount)
	}
}

// writeReceipt encodes entries via capture_receipt and writes the
// resulting blob into blobStore. Returns the blob's content-addressed
// markl id as a string. When hint is non-nil, the receipt's hyphence
// metadata block carries an RFC 0001 store-hint line; pass nil for
// hint to omit. Mirrors madder's writeReceiptBlob shape.
func writeReceipt(
	blobStore blob_stores.BlobStoreInitialized,
	entries []capture_receipt.EntryV1,
	hint *capture_receipt.StoreHint,
) (id string, err error) {
	wc, err := blobStore.MakeBlobWriter(nil)
	if err != nil {
		err = errors.Wrap(err)
		return
	}
	defer errors.DeferredCloser(&err, wc)

	if _, err = capture_receipt.WriteV1WithHint(wc, entries, hint); err != nil {
		err = errors.Wrap(err)
		return
	}

	id = wc.GetMarklId().String()
	return
}
