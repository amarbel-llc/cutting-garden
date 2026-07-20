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
// The `-format auto|tap|json|tap-legacy|json-legacy` flag selects the
// event renderer on the pipe path (the unified TAP-14 / tap-ndjson
// renderers, or the pre-unification legacy wire during the Stage B
// dual-format window).
package capture

import (
	"fmt"
	"io"
	"os"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"

	"code.linenisgreat.com/cutting-garden/internal/buildinfo"
	"code.linenisgreat.com/cutting-garden/internal/capture_events"
	"code.linenisgreat.com/cutting-garden/internal/capture_failures"
	"code.linenisgreat.com/cutting-garden/internal/capture_log"
	"code.linenisgreat.com/cutting-garden/internal/capture_receipt"
	"code.linenisgreat.com/cutting-garden/internal/capture_render_legacy"
	"code.linenisgreat.com/cutting-garden/internal/capture_render_ndjson"
	"code.linenisgreat.com/cutting-garden/internal/capture_render_tap"
	"code.linenisgreat.com/cutting-garden/internal/capture_sink"
	"code.linenisgreat.com/cutting-garden/internal/capture_viewport"
	"code.linenisgreat.com/cutting-garden/internal/command"
	"code.linenisgreat.com/cutting-garden/internal/command_components"
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/madder/go/pkgs/blob_store_env"
	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"
)

// Progress flag values: the live viewport runs when active.
const (
	progressAuto   = "auto"
	progressAlways = "always"
	progressNever  = "never"
)

// Format flag values. tap/json are the unified Stage B renderers
// (phases as TAP test points / tap-ndjson records); the *-legacy values
// reproduce the exact pre-unification wire during the dual-format
// window and are deprecated on arrival (removal per the design doc's
// promotion criteria — docs/plans/2026-06-06-unified-capture-events-
// tap-design.md §Rollback).
const (
	formatAuto       = "auto"
	formatTAP        = "tap"
	formatJSON       = "json"
	formatTAPLegacy  = "tap-legacy"
	formatJSONLegacy = "json-legacy"
)

// Capture is the value registered for the `capture` subcommand.
//
// Format is bound to the `-format` CLI flag by SetFlagDefinitions
// (auto|tap|json|tap-legacy|json-legacy — see the format constants).
// A pointer receiver on Run is required so the parsed flag value
// reaches the dispatch site.
//
// Progress is bound to `--progress` (auto|always|never). When active
// (see progressActive), the live viewport runs on stderr and the
// structured per-record sink is suppressed in favor of the TUI; when
// inactive the path is byte-identical to a viewport-less capture.
//
// Pack is bound to `--pack`. When set, every blob store written to
// during the run that supports packfiles (madder inventory-archive
// stores — those implementing blob_stores.PackableArchive) is packed
// once after all receipts are durable, consolidating loose blobs into
// archive files. Stores that do not support packfiles are skipped.
type Capture struct {
	Format   string
	Progress string
	Pack     bool
}

var (
	_ command.Cmd                       = (*Capture)(nil)
	_ interfaces.CommandComponentWriter = (*Capture)(nil)
)

// New constructs a Capture with its flag fields initialized to
// defaults.
func New() *Capture {
	return &Capture{Format: formatAuto, Progress: progressAuto}
}

func (*Capture) GetDescription() command.Description {
	return command.Description{
		Short: "capture one or more directory trees into madder's blob stores",
		Long: "Walks each source path (or hands URL-shaped args to the " +
			"matching plugin), writes every regular file as a " +
			"content-addressed blob, and emits one receipt blob per " +
			"store group describing the captured entries.\n" +
			".PP\n" +
			"Partial results are deliberately durable. Per-entry failures " +
			"and interruption (SIGINT/SIGTERM/SIGHUP) do not discard " +
			"completed work: every entry captured before the abort is " +
			"still folded into the store-group receipt, the receipt blob " +
			"is written, and captures.log records it. The run itself " +
			"reports the failure \\(em failing phase records and a " +
			"bailout on the event stream, and a nonzero exit \\(em but " +
			"the receipt blob carries no interrupted marker: it is an " +
			"ordinary receipt describing exactly the entries that were " +
			"captured. Blobs are content-addressed, so re-running the " +
			"same capture after an interruption re-reads the sources but " +
			"stores nothing twice.",
	}
}

func (cmd *Capture) SetFlagDefinitions(
	flagSet interfaces.CLIFlagDefinitions,
) {
	flagSet.StringVar(
		&cmd.Format,
		"format",
		formatAuto,
		"event stream format on the pipe path: auto (tap on a stdout TTY, "+
			"json when piped), tap (TAP-14 text — phases as test points with "+
			"entry subtests), json (tap-ndjson records), or tap-legacy/"+
			"json-legacy (the exact pre-unification wire; DEPRECATED — kept "+
			"only for the dual-format transition window and removed once all "+
			"consumers migrate, per the Stage B design doc's promotion "+
			"criteria). Ignored while the -progress viewport is active.",
	)
	flagSet.StringVar(
		&cmd.Progress,
		"progress",
		progressAuto,
		"live progress viewport: auto (on when stderr is a TTY and "+
			"NO_COLOR is unset), always, or never. When active, -format "+
			"is ignored and the per-record stream is suppressed in favor "+
			"of the TUI on stderr.",
	)
	flagSet.BoolVar(
		&cmd.Pack,
		"pack",
		false,
		"after every receipt is written, run madder's pack operation on "+
			"each store written to that supports packfiles (inventory-"+
			"archive stores), consolidating loose blobs into archive "+
			"files. Stores that do not support packfiles are skipped. "+
			"Packing runs only once receipts are durable and never alters "+
			"the run's exit code: a pack failure degrades to a notice.",
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

// validateFormat enforces the allowed -format values. Mirrors
// validateProgress; Run surfaces a failure as EX_USAGE.
func validateFormat(value string) error {
	switch value {
	case formatAuto, formatTAP, formatJSON, formatTAPLegacy, formatJSONLegacy:
		return nil
	}
	return errors.ErrorWithStackf(
		"invalid -format value %q; expected auto, tap, json, tap-legacy, or json-legacy",
		value,
	)
}

// resolveFormat collapses formatAuto into tap or json based on whether
// stdout is a terminal, replicating the TTY semantics of the madder
// output_format.Resolve this flag replaced (go-isatty on stdout,
// Cygwin/msys pipes counting as terminals). Non-auto values pass
// through unchanged. The *os.File parameter exists for fd injection in
// tests, like progressActive.
func resolveFormat(value string, stdout *os.File) string {
	if value != formatAuto {
		return value
	}
	fd := stdout.Fd()
	if isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd) {
		return formatTAP
	}
	return formatJSON
}

// pipeline is the per-run observability wiring setupPipeline selects.
// Exactly one renderer consumes the unified Stream per run (viewport,
// unified TAP/ndjson renderer, or the legacy bridge); the helper
// methods below route the orchestrator's own events so Run's body never
// branches on the active path.
type pipeline struct {
	// stream is the unified event Stream plugins and the orchestrator
	// emit on: the viewport reporter (TTY), a unified renderer
	// (tap/json pipe paths), or the legacy bridge (*-legacy pipe
	// paths, Entry/Failure forwarded 1:1 to legacySink).
	stream cutting_garden_plugins.Reporter

	// legacySink receives the orchestrator's pre-unification direct
	// calls (SetStore/Notice/StoreGroupReceipt/Failure/Finalize) in
	// their exact historical order. Nil on the unified tap/json paths
	// — there, notices route via stream.Log, receipts travel in the
	// receipt phase's verdict diagnostic, and the trailing
	// plan/summary comes from stream.Finalize. On the viewport path it
	// is the io.Discard NDJSON sink, preserving the pre-Stage-B
	// suppression exactly.
	legacySink capture_sink.Sink

	// finish fires the stream's Finalize exactly once with the batch
	// error (sync.Once via makeFinish) and, on the viewport path,
	// blocks until the render loop exits. Run defers a guard call for
	// dewey's ctx-cancel panic-unwind.
	finish func(error)

	viewportActive bool
}

// notice routes an informational message: the legacy sinks have a
// dedicated Notice channel (TAP comment / NDJSON stderr line); the
// unified renderers carry it as a Log event (TAP comment; dropped by
// ndjson, whose schema has no comment record).
func (p *pipeline) notice(format string, args ...any) {
	if p.legacySink != nil {
		p.legacySink.Notice(format, args...)
		return
	}
	p.stream.Log(format, args...)
}

// setStore is legacy-only: the legacy sinks stamp subsequent records
// with the store id. The unified formats carry the store in the
// receipt phase diagnostic instead.
func (p *pipeline) setStore(store string) {
	if p.legacySink != nil {
		p.legacySink.SetStore(store)
	}
}

// failure reports an orchestrator-level failure (argument classify,
// receipt write, protocol root error): the legacy wire gets its
// Failure record, and every path gets the Log echo (viewport tail
// line / TAP comment) — the exact pre-Stage-B pairing. On the unified
// formats the run-level verdict additionally reaches the wire via
// finish(batchErr) → Finalize (bailout + summary).
func (p *pipeline) failure(source string, err error) {
	if p.legacySink != nil {
		p.legacySink.Failure(source, err)
	}
	p.stream.Log("failure: %s: %v", source, err)
}

// failurePhase reports a per-argument failure (classify refusal,
// protocol-root error, receipt write) as its own failing phase
// bracketing the legacy/log emission: PhaseStart(source) + the
// pre-Stage-B failure pairing + PhaseEnd{OK:false, {"error"}}. The
// phase is what makes the failure machine-readable on the unified
// json wire (ndjson drops Log); the bridge drops phases, so the
// legacy wire is unchanged, and the viewport persists a ✗ line. The
// planErr bail deliberately stays on plain failure — its detail
// reaches the wire via finish(planErr)'s bailout instead.
func (p *pipeline) failurePhase(source string, err error) {
	p.stream.PhaseStart(source)
	p.failure(source, err)
	p.stream.PhaseEnd(capture_events.Verdict{
		OK:         false,
		Diagnostic: map[string]any{"error": err.Error()},
	})
}

// receipt reports one store-group receipt: the legacy wire gets its
// StoreGroupReceipt record; every path gets the receipt phase, whose
// verdict diagnostic carries store/receipt_id/count machine-readably
// for the unified formats.
func (p *pipeline) receipt(storeName, receiptID string, count int) {
	if p.legacySink != nil {
		p.legacySink.StoreGroupReceipt(receiptID, count)
	}
	reportReceipt(p.stream, quoteEmpty(storeName), receiptID, count)
}

// failures reports one store-group failure receipt (or its local
// spill): the legacy wire gets the line as a Notice (the
// pre-unification sinks have no failure-receipt record type); every
// path gets a failures phase whose verdict diagnostic carries
// store/<key>/count machine-readably for the unified formats. key is
// "id" when the receipt landed in the blob store, "spill" when the
// store write failed and the bytes spilled locally (value is then the
// spill path).
func (p *pipeline) failures(storeName, key, value string, count int) {
	storeLabel := quoteEmpty(storeName)
	line := fmt.Sprintf(
		"failures store=%s %s=%s count=%d", storeLabel, key, value, count,
	)
	if p.legacySink != nil {
		p.legacySink.Notice("%s", line)
	}
	p.stream.PhaseStart(fmt.Sprintf("failures store=%s", storeLabel))
	p.stream.Log("%s", line)
	p.stream.PhaseEnd(capture_events.Verdict{
		OK: true,
		Diagnostic: map[string]any{
			"store": storeLabel,
			key:     value,
			"count": count,
		},
	})
}

// closeLegacy finalizes the legacy sink (TAP plan emission / NDJSON
// flush); Run defers it exactly where the pre-Stage-B
// `defer sink.Finalize()` sat. No-op on the unified paths, whose
// trailing records come from stream.Finalize via finish.
func (p *pipeline) closeLegacy() {
	if p.legacySink != nil {
		p.legacySink.Finalize()
	}
}

// setupPipeline builds the observability surface for a Run.
//
// VIEWPORT (progress active): the viewport renders on stderr, the
// legacy sink is routed to io.Discard so per-record output does not
// race the TUI, and finish blocks until the program's render loop
// exits so the final frame flushes and the terminal restores before
// the caller sets the exit code. This path ignores -format and is
// byte-identical to the pre-format-rework viewport path.
//
// PIPE (progress inactive): -format selects the renderer. tap/json get
// the unified Stage B renderers on os.Stdout (no legacy sink — see the
// pipeline field docs). tap-legacy/json-legacy reproduce the
// pre-unification construction exactly: the legacy sink on
// os.Stdout(/os.Stderr) with the bridge as the plugin Stream; their
// finish routes through the bridge's no-op Finalize, keeping the wire
// byte-identical (the pinned rollback guarantee).
func (cmd *Capture) setupPipeline(label string) pipeline {
	if progressActive(cmd.Progress, os.Stderr) {
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
		rep := capture_viewport.NewReporter(p)
		return pipeline{
			stream:         rep,
			legacySink:     capture_sink.NewNDJSON(io.Discard, io.Discard),
			finish:         makeFinish(rep, runDone),
			viewportActive: true,
		}
	}

	switch resolveFormat(cmd.Format, os.Stdout) {
	case formatTAP:
		r := capture_render_tap.New(os.Stdout)
		return pipeline{stream: r, finish: makeFinish(r, nil)}
	case formatJSON:
		r := capture_render_ndjson.New(os.Stdout)
		return pipeline{stream: r, finish: makeFinish(r, nil)}
	case formatTAPLegacy:
		sink := capture_sink.NewTAP(os.Stdout)
		bridge := capture_render_legacy.NewSinkBridge(sink)
		return pipeline{
			stream:     bridge,
			legacySink: sink,
			finish:     makeFinish(bridge, nil),
		}
	default: // formatJSONLegacy — validateFormat bounds the set
		sink := capture_sink.NewNDJSON(os.Stdout, os.Stderr)
		bridge := capture_render_legacy.NewSinkBridge(sink)
		return pipeline{
			stream:     bridge,
			legacySink: sink,
			finish:     makeFinish(bridge, nil),
		}
	}
}

// makeFinish builds the once-guarded terminal-event closure: route err
// through the stream's Finalize exactly once (the unified renderers
// emit their trailing bailout/plan/summary; the viewport adapter sends
// BatchDone; the legacy bridge's Finalize is a deliberate no-op), then
// — when runDone is non-nil, i.e. the viewport path — block until the
// program's render loop exits so the final frame flushes and the
// terminal restores before the caller sets the exit code. The
// sync.Once makes Run's deferred guard a no-op after an inline
// finish(planErr)/finish(batchErr) already ran.
func makeFinish(
	stream cutting_garden_plugins.Reporter,
	runDone <-chan struct{},
) func(error) {
	var once sync.Once
	return func(err error) {
		once.Do(func() {
			stream.Finalize(err)
			if runDone != nil {
				<-runDone
			}
		})
	}
}

// reportReceipt emits the receipt phase on the event stream: a phase
// wrapping the human-readable receipt line, persisted as a checkmark by
// the viewport. The verdict diagnostic carries the receipt
// machine-readably for the unified formats (the legacy wire keeps
// StoreGroupReceipt; the viewport's OK-collapse hides the diagnostic —
// the post-teardown stdout reprint covers TTY humans). Semantics only —
// the receipt's identity is the blob itself.
func reportReceipt(
	stream cutting_garden_plugins.Reporter,
	storeLabel, receiptID string,
	count int,
) {
	stream.PhaseStart(fmt.Sprintf("receipt store=%s", storeLabel))
	stream.Log("receipt store=%s id=%s count=%d", storeLabel, receiptID, count)
	stream.PhaseEnd(capture_events.Verdict{
		OK: true,
		Diagnostic: map[string]any{
			"store":      storeLabel,
			"receipt_id": receiptID,
			"count":      count,
		},
	})
}

// capturedReceipt records a store-group receipt the loop emitted, so the
// receipt id(s) can be reprinted to stdout after the viewport tears down
// (the live sink that would have shown them is suppressed under -progress).
type capturedReceipt struct {
	store     string
	receiptID string
	count     int
}

// capturedFailureReceipt records a failure receipt (or its local
// spill) the loop emitted, so the line can be reprinted to stdout
// after the viewport tears down — same rationale as capturedReceipt.
type capturedFailureReceipt struct {
	store string
	key   string // "id" (store blob) or "spill" (local fallback path)
	value string
	count int
}

func (cmd *Capture) Run(req command.Request) {
	ctx := req.Context.(errors.Context)

	if err := validateFormat(cmd.Format); err != nil {
		errors.ContextCancelWithBadRequestf(ctx, "%s", err.Error())
		return
	}

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
	//
	// Plugins emit entries/failures on p.stream. On the *-legacy pipe
	// paths that is the legacy bridge: Entry/Failure forward 1:1 to the
	// sink (byte-identical wire) and every other event is a no-op. On the
	// TTY path it is the viewport reporter, whose Entry/Failure are
	// no-ops — per-entry lines intentionally do not render in the TTY
	// tail. On the unified tap/json paths it is the Stage B renderer.
	// The orchestrator's own events route through the pipeline helpers,
	// which preserve the pre-Stage-B direct-sink order on the legacy and
	// viewport paths.
	p := cmd.setupPipeline(captureLabel(args))
	defer p.closeLegacy()

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
	defer p.finish(errCaptureAborted)

	var envBlobStore blob_store_env.BlobStoreEnv
	if p.viewportActive {
		envBlobStore = command_components.MakeBlobStoreEnvWithErr(ctx,
			&reporterLineWriter{
				log: func(s string) { p.stream.Log("%s", s) },
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
	var failureReceipts []capturedFailureReceipt

	// Classify failures precede any store group; carryFailures folds
	// them into the FIRST group's failure receipt so they are recorded
	// durably (they have no group of their own).
	var carryFailures []capture_failures.FailureV1

	// Stores the run's groups capture into, in first-use order and deduped
	// by effective store id (groups sharing the default store collapse to
	// one entry). Recorded per group rather than per successful receipt:
	// blobs land in the store during CaptureRoot/CaptureProtocol (and via
	// failure receipts) even when the group's receipt write later fails,
	// and packing a store that received nothing is a no-op. Populated only
	// under --pack; consumed by packWrittenStores after the group loop.
	var packTargets []writtenStore
	recordPackTarget := func(id string, bs blob_stores.BlobStoreInitialized) {
		if !cmd.Pack {
			return
		}
		for _, w := range packTargets {
			if w.id == id {
				return
			}
		}
		packTargets = append(packTargets, writtenStore{id: id, store: bs})
	}

	for _, cf := range classifyFails {
		p.failurePhase(cf.arg, cf.err)
		failCount++
		carryFailures = append(carryFailures, capture_failures.FailureV1{
			Path:  cf.arg,
			Op:    capture_failures.OpPlugin,
			Error: cf.err.Error(),
		})
	}

	if planErr != nil {
		p.failure("(arguments)", planErr)
		// On the unified pipe paths Finalize(planErr) renders the bailout
		// and trailing plan/summary; legacy/viewport semantics unchanged.
		p.finish(planErr)
		errors.ContextCancelWithBadRequestf(ctx, "%s", planErr.Error())
		return
	}

	for _, group := range groups {
		if group.switchNotice != "" {
			p.notice("%s", group.switchNotice)
		}

		var blobStore blob_stores.BlobStoreInitialized
		var storeName string
		if group.storeID.IsEmpty() {
			blobStore = envBlobStore.GetDefaultBlobStore()
		} else {
			blobStore = envBlobStore.GetBlobStore(group.storeID)
			storeName = group.storeID.String()
		}

		p.setStore(storeName)

		// effectiveStoreId resolves the default-store sentinel ("") to its
		// actual id (RFC 0001 §Store-Hint Resolution). Reused for the
		// store hint below and as the --pack dedup key so groups sharing
		// the default store pack it once.
		effectiveStoreId := storeName
		if effectiveStoreId == "" {
			effectiveStoreId = envBlobStore.GetDefaultBlobStoreId()
		}
		recordPackTarget(effectiveStoreId, blobStore)

		groupFailures := carryFailures
		carryFailures = nil

		var entries []capture_receipt.EntryV1
		entryRoots := 0
		protocolReceipts := 0

		for _, root := range group.roots {
			if root.shadowNotice != "" {
				p.notice("%s", root.shadowNotice)
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
					StoreName:          storeName,
					PriorReceiptDigest: findPriorReceipt(cgEnvDir, storeName, root.path),
					Reporter:           p.stream,
					BinaryVersion:      buildinfo.Version,
				})
				if perr != nil {
					p.failurePhase(root.path, perr)
					failCount++
					groupFailures = append(groupFailures, capture_failures.FailureV1{
						Root:  root.path,
						Path:  root.path,
						Op:    capture_failures.OpPlugin,
						Error: perr.Error(),
					})
					continue
				}
				p.receipt(storeName, res.ReceiptDigest, res.ObjectCount)
				receipts = append(receipts, capturedReceipt{
					store: storeName, receiptID: res.ReceiptDigest, count: res.ObjectCount,
				})
				protocolReceipts++
				captureLogEntries = append(captureLogEntries, captureLogEntry{
					Ts:        capture_log.Timestamp(),
					ReceiptID: res.ReceiptDigest,
					StoreID:   storeName,
					Roots:     []string{root.path},
				})
				continue
			}

			// root.plugin is the base Plugin interface (RFC 0005's
			// scheme-registry fallback widened captureRoot.plugin beyond
			// CapturePlugin); classifyArg's resolveCapturePlugin already
			// guaranteed the resolved plugin implements ProtocolCapturePlugin
			// (handled above) or CapturePlugin, so this assertion cannot
			// fail on a path that reached here through the planner.
			cp, ok := root.plugin.(cutting_garden_plugins.CapturePlugin)
			if !ok {
				perr := errors.ErrorWithStackf(
					"internal error: resolved plugin for %q supports neither "+
						"the RFC 0002 protocol capture interface nor the legacy "+
						"EntryV1 CapturePlugin interface (resolveCapturePlugin "+
						"invariant violated)", root.path,
				)
				p.failurePhase(root.path, perr)
				failCount++
				groupFailures = append(groupFailures, capture_failures.FailureV1{
					Root:  root.path,
					Path:  root.path,
					Op:    capture_failures.OpPlugin,
					Error: perr.Error(),
				})
				continue
			}

			result := cp.CaptureRoot(cutting_garden_plugins.CaptureRootRequest{
				Context:   ctx,
				Source:    root.sourceURL,
				RawArg:    root.path,
				BlobStore: blobStore,
				Reporter:  p.stream,
			})
			entries = append(entries, result.Entries...)
			entryRoots++
			failCount += result.FailCount
			groupFailures = append(groupFailures, result.Failures...)
		}

		// Collapse Root to "." when the EntryV1 entries came from a
		// single root per RFC 0001 §Root Encoding. Multi-root groups
		// keep distinct Root values.
		if entryRoots == 1 {
			for i := range entries {
				entries[i].Root = "."
			}
		}

		// receiptID is the group's success-receipt id; "" when no
		// EntryV1 receipt applies (empty group, protocol-only group)
		// or its write failed. The failure receipt records it as
		// Meta.Receipt. groupLogIdx tracks the group's captures.log
		// entry (if one was built) so the failure-receipt block below
		// can annotate it with outcome + failure_receipt_id.
		receiptID := ""
		groupLogIdx := -1
		if len(entries) == 0 {
			// Protocol roots already emitted their own receipts; only
			// warn about a skipped store-group receipt when there was
			// EntryV1 work that produced nothing.
			if protocolReceipts == 0 {
				p.notice(
					"notice: no entries captured for store=%s; receipt skipped",
					quoteEmpty(storeName),
				)
			}
		} else {
			hint, hintErr := capture_receipt.ComputeStoreHint(blobStore, effectiveStoreId)
			if hintErr != nil {
				p.notice(
					"notice: omitting store-hint for store=%s: %v",
					quoteEmpty(storeName), hintErr,
				)
			}

			var err error
			receiptID, err = capture_receipt.WriteV1ToStore(blobStore, entries, hint)
			if err != nil {
				p.failurePhase("(receipt)", err)
				failCount++
				groupFailures = append(groupFailures, capture_failures.FailureV1{
					Path:  "(receipt)",
					Op:    capture_failures.OpReceiptWrite,
					Error: err.Error(),
				})
				receiptID = ""
			} else {
				p.receipt(storeName, receiptID, len(entries))
				receipts = append(receipts, capturedReceipt{
					store: storeName, receiptID: receiptID, count: len(entries),
				})

				captureLogEntries = append(captureLogEntries, captureLogEntry{
					Ts:        capture_log.Timestamp(),
					ReceiptID: receiptID,
					StoreID:   storeName,
					Roots:     rootPaths(group.roots),
				})
				groupLogIdx = len(captureLogEntries) - 1
			}
		}

		// One failure receipt per group that had failures, or that was
		// active when a signal aborted the run (outcome aborted enables
		// resume-style retry — design §Write path). A group with zero
		// failures and no abort writes nothing. The write never alters
		// the run's exit code: failCount alone drives it.
		ctxAborted := ctx.Err() != nil
		if len(groupFailures) == 0 && !ctxAborted {
			continue
		}

		fv := buildFailureReceipt(
			rootPaths(group.roots), len(entries), groupFailures,
			receiptID, ctxAborted, signalCauseName(ctx),
		)
		failuresID, spillPath, ferr := writeFailureReceipt(blobStore, cgEnvDir, fv)
		switch {
		case ferr != nil:
			// Degrade to a notice (design §Error handling): losing the
			// failure receipt must not mask the run's own outcome.
			p.notice(
				"notice: failure receipt for store=%s not recorded: %v",
				quoteEmpty(storeName), ferr,
			)
		case spillPath != "":
			p.failures(storeName, "spill", spillPath, len(groupFailures))
			failureReceipts = append(failureReceipts, capturedFailureReceipt{
				store: storeName, key: "spill", value: spillPath,
				count: len(groupFailures),
			})
			// Outcome is journaled even when the failure receipt
			// spilled; the spill path itself stays on stderr only.
			if groupLogIdx >= 0 {
				captureLogEntries[groupLogIdx].Outcome = fv.Meta.Outcome
			}
		default:
			p.failures(storeName, "id", failuresID, len(groupFailures))
			failureReceipts = append(failureReceipts, capturedFailureReceipt{
				store: storeName, key: "id", value: failuresID,
				count: len(groupFailures),
			})
			if groupLogIdx >= 0 {
				captureLogEntries[groupLogIdx].Outcome = fv.Meta.Outcome
				captureLogEntries[groupLogIdx].FailureReceiptID = failuresID
			} else {
				// No success entry exists for this group (empty group or
				// its receipt write failed) — journal a receipt-less line
				// so the log still leads triage to the failure receipt.
				captureLogEntries = append(captureLogEntries, captureLogEntry{
					Ts:               fv.Meta.Ts,
					StoreID:          storeName,
					Roots:            rootPaths(group.roots),
					Outcome:          fv.Meta.Outcome,
					FailureReceiptID: failuresID,
				})
			}
		}
	}

	// --pack: consolidate loose blobs into archive files now that every
	// receipt is durable. Runs before teardown so its notices route
	// through the live pipeline like any other; deliberately non-fatal
	// (see packWrittenStores).
	if cmd.Pack {
		packWrittenStores(ctx, p.notice, packTargets)
	}

	if len(captureLogEntries) > 0 {
		appendCaptureLog(cgEnvDir, p.notice, captureLogEntries)
	}

	// Tear down the viewport BEFORE setting the exit code so its final frame
	// flushes and the terminal restores first. The BatchDone error mirrors
	// the failure-vs-success the exit code will reflect. On the legacy pipe
	// paths finish routes through the bridge's no-op Finalize, so this
	// ordering is identical to the pre-viewport path; on the unified pipe
	// paths it renders the trailing bailout/plan/summary.
	var batchErr error
	if failCount > 0 {
		batchErr = errCaptureFailed(failCount)
	}
	p.finish(batchErr)

	// The live sink that would have shown receipt id(s) was suppressed under
	// the viewport; reprint them on stdout so they survive (and stay
	// greppable). Inactive mode already emitted them via the real sink — do
	// NOT double-print.
	if p.viewportActive {
		for _, r := range receipts {
			fmt.Fprintf(os.Stdout, "receipt store=%s id=%s count=%d\n",
				quoteEmpty(r.store), r.receiptID, r.count)
		}
		// Failure-receipt ids (or spill paths) survive viewport
		// teardown the same way the success-receipt ids do.
		for _, f := range failureReceipts {
			fmt.Fprintf(os.Stdout, "failures store=%s %s=%s count=%d\n",
				quoteEmpty(f.store), f.key, f.value, f.count)
		}
	}

	if failCount > 0 {
		// Plain (non-BadRequest) cancel: per-entry capture failures are
		// runtime IO trouble (exit 2), not a malformed invocation (exit
		// 64). Matches diff/restore, which route runtime failures
		// through ContextCancelWithError. Retagged after dewey RFC 0002
		// made the status tagging deliberate rather than incidental.
		errors.ContextCancelWithErrorf(ctx,
			"capture failed entries: %d", failCount)
	}
}

// packWrittenStores runs madder's pack operation on every store written
// during the run that supports packfiles. A store supports packfiles iff
// its underlying blob store implements blob_stores.PackableArchive (today
// the madder inventory-archive stores) — the same duck-typed capability
// check madder's own `pack` command uses; stores that don't (plain local
// stores, remote SFTP/S3/WebDAV) are skipped with a notice so an explicit
// --pack still reports what it did.
//
// It is deliberately non-fatal: by the time it runs every receipt is
// already durable, so packing is a storage optimization. A pack failure
// degrades to a notice and never changes the run's exit code, mirroring
// the failure-receipt handling above.
func packWrittenStores(
	ctx errors.Context,
	notice func(format string, args ...any),
	targets []writtenStore,
) {
	for _, w := range targets {
		packable, ok := w.store.BlobStore.(blob_stores.PackableArchive)
		if !ok {
			notice(
				"notice: store=%s does not support packfiles; pack skipped",
				quoteEmpty(w.id),
			)
			continue
		}

		if err := packable.Pack(blob_stores.PackOptions{Context: ctx}); err != nil {
			notice("notice: pack failed for store=%s: %v", quoteEmpty(w.id), err)
			continue
		}

		notice("notice: packed store=%s", quoteEmpty(w.id))
	}
}

// writtenStore pairs a store's effective id with its initialized handle
// for the --pack pass. Kept as an ordered slice (first-write order); the
// handful of stores a run touches makes linear dedup cheaper than a
// map+slice pair.
type writtenStore struct {
	id    string
	store blob_stores.BlobStoreInitialized
}

// errCaptureFailed is the BatchDone error shown in the viewport's final
// frame; it mirrors the message the exit-code path uses. Kept as a plain
// error because the viewport only renders its Error() string and the exit
// code is set independently via ContextCancelWithErrorf below.
func errCaptureFailed(n int) error {
	return fmt.Errorf("capture failed entries: %d", n)
}

// errCaptureAborted is the BatchDone error the deferred teardown guard in
// Run sends when a panic-unwind (ctx cancel, SIGINT) reaches it before any
// inline finish call. It only ever shows in the viewport's final frame; the
// real cause still prints via dewey's error machinery after teardown.
var errCaptureAborted = fmt.Errorf("capture aborted")
