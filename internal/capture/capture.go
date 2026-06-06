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
	"io"
	"os"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/capture_sink"
	"github.com/amarbel-llc/cutting-garden/internal/capture_viewport"
	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/cutting-garden/internal/command_components"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/blob_store_env"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/madder/go/pkgs/output_format"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"
)

// Progress flag values: the live viewport runs when active.
const (
	progressAuto   = "auto"
	progressAlways = "always"
	progressNever  = "never"
)

// Capture is the value registered for the `capture` subcommand.
//
// Format is bound to the `--format` CLI flag by SetFlagDefinitions.
// A pointer receiver on Run is required so the parsed flag value
// reaches the dispatch site.
//
// Progress is bound to `--progress` (auto|always|never). When active
// (see progressActive), the live viewport runs on stderr and the
// structured per-record sink is suppressed in favor of the TUI; when
// inactive the path is byte-identical to a viewport-less capture.
type Capture struct {
	Format   output_format.Format
	Progress string
}

var (
	_ command.Cmd                       = (*Capture)(nil)
	_ interfaces.CommandComponentWriter = (*Capture)(nil)
)

// New constructs a Capture with its flag fields initialized to
// defaults — matches the madder capture cmd's `Format:
// output_format.Default` shape.
func New() *Capture {
	return &Capture{Format: output_format.Default, Progress: progressAuto}
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
	flagSet.StringVar(
		&cmd.Progress,
		"progress",
		progressAuto,
		"live progress viewport: auto (on when stderr is a TTY and "+
			"NO_COLOR is unset), always, or never. When active, -format "+
			"is ignored and the per-record stream is suppressed in favor "+
			"of the TUI on stderr.",
	)
}

// validateProgress enforces the allowed -progress values. Mirrors
// diff.validateColor; returns nil for the three accepted values, an
// error otherwise.
func validateProgress(value string) error {
	switch value {
	case progressAuto, progressAlways, progressNever:
		return nil
	}
	return errors.ErrorWithStackf(
		"invalid -progress value %q; expected auto, always, or never",
		value,
	)
}

// setupReporting builds the observability surface for a Run. It returns the
// plugin-facing Reporter (nil when inactive — the caller wraps with
// ReporterOrNop), the structured sink, and a finish(err) to call after the
// capture loop.
//
// INACTIVE (progress off): reproduces a viewport-less capture exactly — the
// sink is the resolved TAP/NDJSON writer on os.Stdout/os.Stderr and finish is
// a no-op. This is the rollback path; its sink construction MUST match the
// pre-viewport code.
//
// ACTIVE (progress on): the viewport renders on stderr, the structured sink
// is routed to io.Discard so per-record output does not race the TUI, and
// finish sends BatchDone and blocks until the program's render loop exits so
// the final frame flushes and the terminal restores before the caller sets
// the exit code.
//
// finish is idempotent (sync.Once): Run defers a finish call so that dewey's
// ctx-cancel panic-unwind (ContextContinueOrPanic — SIGINT, bad config in env
// construction, mid-loop cancels) still tears the viewport down and restores
// the terminal; when one of the inline finish(planErr)/finish(batchErr) calls
// already ran, the deferred call is a no-op.
func (cmd *Capture) setupReporting(label string) (
	reporter cutting_garden_plugins.Reporter,
	sink capture_sink.Sink,
	finish func(error),
) {
	if !progressActive(cmd.Progress, os.Stderr) {
		switch cmd.Format.Resolve(os.Stdout) {
		case output_format.FormatTAP:
			sink = capture_sink.NewTAP(os.Stdout)
		default:
			sink = capture_sink.NewNDJSON(os.Stdout, os.Stderr)
		}
		return nil, sink, func(error) {}
	}

	m := capture_viewport.New(capture_viewport.WithTitle(label))
	p := tea.NewProgram(
		m,
		tea.WithOutput(os.Stderr),
		tea.WithInput(nil),
		// CRITICAL: leave SIGINT/SIGTERM to errors.Context's signal
		// handling; do NOT let bubbletea install its own handler and race
		// dewey's unwind.
		tea.WithoutSignalHandler(),
	)
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_, _ = p.Run()
	}()
	var finishOnce sync.Once
	return capture_viewport.NewReporter(p),
		capture_sink.NewNDJSON(io.Discard, io.Discard),
		func(err error) {
			finishOnce.Do(func() {
				p.Send(capture_viewport.BatchDone{Err: err})
				<-runDone
			})
		}
}

// capturedReceipt records a store-group receipt the loop emitted, so the
// receipt id(s) can be reprinted to stdout after the viewport tears down
// (the live sink that would have shown them is suppressed under -progress).
type capturedReceipt struct {
	store     string
	receiptID string
	count     int
}

func (cmd *Capture) Run(req command.Request) {
	ctx := req.Context.(errors.Context)

	if err := validateProgress(cmd.Progress); err != nil {
		errors.ContextCancelWithBadRequestf(ctx, "%s", err.Error())
		return
	}

	args := req.PopArgs()

	// The reporting surface is built BEFORE the blob-store env: blob-store
	// chatter follows the env's err sink (madder#228), and the sink target
	// — the viewport Reporter — must exist when the env captures it. With
	// the viewport inactive the env construction is byte-identical to the
	// pre-viewport path (default stderr sink).
	rep, sink, finish := cmd.setupReporting(captureLabel(args))
	reporter := cutting_garden_plugins.ReporterOrNop(rep)
	viewportActive := rep != nil
	defer sink.Finalize()

	// Teardown guarantee: everything below — env construction included —
	// can ctx-cancel, and dewey cancels panic-unwind through these defers
	// (ContextContinueOrPanic is the catcher upstream). finish is
	// once-guarded, so this deferred call is a no-op on the normal paths
	// where an inline finish(planErr)/finish(batchErr) already ran; on
	// unwind it shuts the renderer down and restores the terminal before
	// the fatal error prints. The generic errCaptureAborted is deliberate:
	// the real error still reaches the user via the error machinery after
	// teardown — restoring the terminal is the priority here, not message
	// fidelity in the final frame.
	defer finish(errCaptureAborted)

	var envBlobStore blob_store_env.BlobStoreEnv
	if viewportActive {
		envBlobStore = command_components.MakeBlobStoreEnvWithErr(ctx,
			&reporterLineWriter{
				log: func(s string) { reporter.Log("%s", s) },
			})
	} else {
		envBlobStore = command_components.MakeBlobStoreEnv(ctx)
	}
	cgEnvDir := command_components.MakeCgEnvDir(ctx)
	shadowCandidates := blobStoreIds(envBlobStore.GetBlobStores())

	groups, classifyFails, planErr := planCapture(args, shadowCandidates)

	failCount := 0
	var captureLogEntries []captureLogEntry
	var receipts []capturedReceipt

	for _, cf := range classifyFails {
		sink.Failure(cf.arg, cf.err)
		reporter.Log("failure: %s: %v", cf.arg, cf.err)
		failCount++
	}

	if planErr != nil {
		sink.Failure("(arguments)", planErr)
		reporter.Log("failure: %s: %v", "(arguments)", planErr)
		finish(planErr)
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
					Context:            ctx,
					Source:             root.sourceURL,
					RawArg:             root.path,
					BlobStore:          blobStore,
					PriorReceiptDigest: findPriorReceipt(cgEnvDir, storeName, root.path),
					Reporter:           reporter,
				})
				if perr != nil {
					sink.Failure(root.path, perr)
					reporter.Log("failure: %s: %v", root.path, perr)
					failCount++
					continue
				}
				sink.StoreGroupReceipt(res.ReceiptDigest, res.ObjectCount)
				receipts = append(receipts, capturedReceipt{
					store: storeName, receiptID: res.ReceiptDigest, count: res.ObjectCount,
				})
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
				Reporter:  reporter,
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
			reporter.Log("failure: %s: %v", "(receipt)", err)
			failCount++
			continue
		}
		sink.StoreGroupReceipt(receiptID, len(entries))
		receipts = append(receipts, capturedReceipt{
			store: storeName, receiptID: receiptID, count: len(entries),
		})

		captureLogEntries = append(captureLogEntries, captureLogEntry{
			Ts:        captureLogTimestamp(),
			ReceiptID: receiptID,
			StoreID:   storeName,
			Roots:     rootPaths(group.roots),
		})
	}

	if len(captureLogEntries) > 0 {
		appendCaptureLog(cgEnvDir, sink, captureLogEntries)
	}

	// Tear down the viewport BEFORE setting the exit code so its final frame
	// flushes and the terminal restores first. The BatchDone error mirrors
	// the failure-vs-success the exit code will reflect. In inactive mode
	// finish is a no-op, so this ordering is identical to the pre-viewport
	// path.
	var batchErr error
	if failCount > 0 {
		batchErr = errCaptureFailed(failCount)
	}
	finish(batchErr)

	// The live sink that would have shown receipt id(s) was suppressed under
	// the viewport; reprint them on stdout so they survive (and stay
	// greppable). Inactive mode already emitted them via the real sink — do
	// NOT double-print.
	if viewportActive {
		for _, r := range receipts {
			fmt.Fprintf(os.Stdout, "receipt store=%s id=%s count=%d\n",
				quoteEmpty(r.store), r.receiptID, r.count)
		}
	}

	if failCount > 0 {
		errors.ContextCancelWithBadRequestf(ctx,
			"capture failed entries: %d", failCount)
	}
}

// errCaptureFailed is the BatchDone error shown in the viewport's final
// frame; it mirrors the message the exit-code path uses. Kept as a plain
// error because the viewport only renders its Error() string and the exit
// code is set independently via ContextCancelWithBadRequestf below.
func errCaptureFailed(n int) error {
	return fmt.Errorf("capture failed entries: %d", n)
}

// errCaptureAborted is the BatchDone error the deferred teardown guard in
// Run sends when a panic-unwind (ctx cancel, SIGINT) reaches it before any
// inline finish call. It only ever shows in the viewport's final frame; the
// real cause still prints via dewey's error machinery after teardown.
var errCaptureAborted = fmt.Errorf("capture aborted")

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
