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
	"fmt"
	"os"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/capture_sink"
	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/blob_store_env"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/madder/go/pkgs/env_dir"
	"github.com/amarbel-llc/madder/go/pkgs/env_local"
	"github.com/amarbel-llc/madder/go/pkgs/env_ui"
	"github.com/amarbel-llc/madder/go/pkgs/madder_env"
	"github.com/amarbel-llc/madder/go/pkgs/output_format"
	"github.com/amarbel-llc/purse-first/libs/dewey/0/interfaces"
	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/errors"
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
	envBlobStore := makeBlobStoreEnv(ctx)
	cgEnvDir := makeCgEnvDir(ctx)
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
			sink.Notice(group.switchNotice)
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

		for _, root := range group.roots {
			if root.shadowNotice != "" {
				sink.Notice(root.shadowNotice)
			}
			result := root.plugin.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
				Source:    root.sourceURL,
				RawArg:    root.path,
				BlobStore: blobStore,
				Sink:      sink,
			})
			entries = append(entries, result.Entries...)
			failCount += result.FailCount
		}

		// Collapse Root to "." for single-root groups per RFC 0003
		// §Root Encoding. Multi-root groups keep distinct Root values.
		if len(group.roots) == 1 {
			for i := range entries {
				entries[i].Root = "."
			}
		}

		if len(entries) == 0 {
			sink.Notice(fmt.Sprintf(
				"notice: no entries captured for store=%s; receipt skipped",
				quoteEmpty(storeName),
			))
			continue
		}

		receiptID, err := writeReceipt(blobStore, entries)
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

	appendCaptureLog(cgEnvDir, sink, captureLogEntries)

	if failCount > 0 {
		errors.ContextCancelWithBadRequestf(ctx,
			"capture failed entries: %d", failCount)
	}
}

// makeBlobStoreEnv is the local reimplementation of madder's
// command_components.EnvBlobStore.MakeEnvBlobStore mixin: build a
// dewey-context-backed env_local from env_dir + env_ui, then hand it
// to pkgs/blob_store_env.MakeBlobStoreEnv. The xdgScope is hardcoded
// to "madder" — cutting-garden is a sibling of madder that operates
// on madder's stores. The audit-log wiring (SetBlobWriteObserver) on
// the blob-write path is intentionally omitted (madder's inventory
// log is a different observability mechanism from cg's captures.log).
func makeBlobStoreEnv(ctx errors.Context) blob_store_env.BlobStoreEnv {
	dir := env_dir.MakeDefault(
		ctx,
		env_dir.Config{
			EnvVarNames: madder_env.DefaultEnvVarNames,
		},
		"madder",
	)
	ui := env_ui.MakeDefault(ctx)
	return blob_store_env.MakeBlobStoreEnv(env_local.Make(ui, dir))
}

// makeCgEnvDir builds the cutting-garden-scoped env_dir for cg's own
// per-utility state (captures.log etc.). Distinct from
// makeBlobStoreEnv's madder-scoped env_dir — the two address disjoint
// XDG paths by construction. Local reimplementation of madder's
// command_components.MakeEnvDirForScope.
func makeCgEnvDir(ctx errors.Context) env_dir.Env {
	return env_dir.MakeDefault(
		ctx,
		env_dir.Config{
			EnvVarNames: madder_env.DefaultEnvVarNames,
		},
		"cutting-garden",
	)
}

// writeReceipt encodes entries via capture_receipt and writes the
// resulting blob into blobStore. Returns the blob's content-addressed
// markl id as a string. Mirrors madder's writeReceiptBlob shape, minus
// the store-hint metadata (step 8).
func writeReceipt(
	blobStore blob_stores.BlobStoreInitialized,
	entries []capture_receipt.EntryV1,
) (id string, err error) {
	wc, err := blobStore.MakeBlobWriter(nil)
	if err != nil {
		err = errors.Wrap(err)
		return
	}
	defer errors.DeferredCloser(&err, wc)

	if _, err = capture_receipt.WriteV1(wc, entries); err != nil {
		err = errors.Wrap(err)
		return
	}

	id = wc.GetMarklId().String()
	return
}
