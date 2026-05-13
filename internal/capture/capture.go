// Package capture wires the `capture` subcommand: walk one directory,
// write every regular file as a blob, and emit a receipt blob
// describing the captured tree.
//
// Accepts an optional positional blob-store-id ahead of the directory
// to target a non-default store (e.g. `capture .default <dir>`).
// Default store resolves exactly the way madder resolves it.
//
// This is the Phase 2 MVP step 5 capture surface: at most one
// store-id + one directory, `--format=auto|tap|json` flag selecting
// the sink, no audit log, no shadow detection. Multi-root +
// interleaved store-switches land in step 6.
package capture

import (
	"net/url"
	"os"
	"path/filepath"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/capture_sink"
	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/blob_store_env"
	"github.com/amarbel-llc/madder/go/pkgs/blob_store_id"
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
		Short: "capture a directory tree into madder's default blob store",
	}
}

func (cmd *Capture) SetFlagDefinitions(
	flagSet interfaces.CLIFlagDefinitions,
) {
	flagSet.Var(&cmd.Format, "format", output_format.FlagDescription)
}

func (cmd *Capture) Run(req command.Request) {
	ctx := req.Context.(errors.Context)

	storeID, dir, ok := parseArgs(ctx, req)
	if !ok {
		return
	}
	// cutting-garden#4: clean the user's arg so a trailing slash doesn't
	// poison entry.Root and propagate into the receipt or sink output.
	// In step 4 single-root mode this is partially redundant (Root is
	// overwritten to "." below) but it also fixes the per-entry sink
	// records emitted during the walk.
	dir = filepath.Clean(dir)

	envBlobStore := makeBlobStoreEnv(ctx)
	var blobStore blob_stores.BlobStoreInitialized
	if storeID.IsEmpty() {
		blobStore = envBlobStore.GetDefaultBlobStore()
	} else {
		blobStore = envBlobStore.GetBlobStore(storeID)
	}

	plugin, err := cutting_garden_plugins.ResolveCapture("")
	if err != nil {
		errors.ContextCancelWithError(ctx, err)
		return
	}

	var sink capture_sink.Sink
	switch cmd.Format.Resolve(os.Stdout) {
	case output_format.FormatTAP:
		sink = capture_sink.NewTAP(os.Stdout)
	default:
		sink = capture_sink.NewNDJSON(os.Stdout, os.Stderr)
	}
	defer sink.Finalize()

	result := plugin.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
		Source:    &url.URL{Path: dir},
		RawArg:    dir,
		BlobStore: blobStore,
		Sink:      sink,
	})

	// Collapse Root to "." for the single-root MVP, matching madder's
	// RFC 0003 §Root Encoding for single-root receipts.
	entries := result.Entries
	for i := range entries {
		entries[i].Root = "."
	}

	if len(entries) > 0 {
		receiptID, err := writeReceipt(blobStore, entries)
		if err != nil {
			sink.Failure("(receipt)", err)
			errors.ContextCancelWithError(ctx, err)
			return
		}
		sink.StoreGroupReceipt(receiptID, len(entries))
	}

	if result.FailCount > 0 {
		errors.ContextCancelWithBadRequestf(ctx,
			"capture failed entries: %d", result.FailCount)
	}
}

// parseArgs reads positional args off the request: either `<DIR>` or
// `<STORE_ID> <DIR>`. Returns ok=false (with the context cancelled
// to a BadRequest) on a usage error. Step 6 will replace this with
// the full multi-root + interleaved store-switches planner.
func parseArgs(
	ctx errors.Context,
	req command.Request,
) (storeID blob_store_id.Id, dir string, ok bool) {
	args := req.PopArgs()
	switch len(args) {
	case 1:
		dir = args[0]
		ok = true
	case 2:
		if err := storeID.Set(args[0]); err != nil {
			errors.ContextCancelWithBadRequestf(ctx,
				"parse blob-store-id %q: %v", args[0], err)
			return
		}
		dir = args[1]
		ok = true
	default:
		errors.ContextCancelWithBadRequestf(ctx,
			"usage: capture [STORE_ID] DIR (got %d args)", len(args))
	}
	return
}

// makeBlobStoreEnv is the local reimplementation of madder's
// command_components.EnvBlobStore.MakeEnvBlobStore mixin: build a
// dewey-context-backed env_local from env_dir + env_ui, then hand it
// to pkgs/blob_store_env.MakeBlobStoreEnv. The xdgScope is hardcoded
// to "madder" — cutting-garden is a sibling of madder that operates
// on madder's stores. The audit-log wiring (SetBlobWriteObserver) is
// intentionally omitted; it lands in step 7.
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

// writeReceipt encodes entries via capture_receipt and writes the
// resulting blob into blobStore. Returns the blob's content-addressed
// markl id as a string. Mirrors madder's writeReceiptBlob shape.
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
