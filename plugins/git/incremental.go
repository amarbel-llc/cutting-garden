package cutting_garden_plugin_git

import (
	"context"
	"fmt"

	"code.linenisgreat.com/cutting-garden/pkgs/capture_events"
	"code.linenisgreat.com/cutting-garden/pkgs/capture_plugin"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/revlist"
	"github.com/go-git/go-git/v5/plumbing/storer"
)

// tryIncrementalCapture rebuilds a receipt for the live tip from a prior
// receipt plus only the objects that changed since it, fetching just the
// delta over the wire (the prior tip is advertised as a `have`). Returns
// ok=false (no error) to mean "fall back to a full capture": the prior
// receipt is unreadable, the fetch failed, or the change is not a
// fast-forward (so the prior object set is not wholly preserved).
//
// On a fast-forward the new object set is exactly the prior set plus the
// fetched delta; references are sorted by oid in writeGitReceipt, so an
// incremental capture and a full capture of the same state produce a
// byte-identical payload node.
func tryIncrementalCapture(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	w capture_plugin.Writer,
	remote, branch, priorReceiptDigest string,
	r cutting_garden_plugins.Reporter,
	version ...string,
) (cutting_garden_plugins.ProtocolCaptureResult, bool, error) {
	// The probe is a phase: it closes with a SKIP directive when nothing
	// changed, plain OK when changes were found (or on a soft fallback to
	// a full capture), and is left open on a hard error (the error
	// propagates; Finalize(err) marks the run failed).
	r.PhaseStart("check prior capture")

	priorPayload, priorMeta, err := loadReceiptPayload(store, priorReceiptDigest)
	if err != nil {
		// Unreadable prior receipt → fall back to a full capture.
		r.PhaseEnd(capture_events.Verdict{OK: true})
		return cutting_garden_plugins.ProtocolCaptureResult{}, false, nil
	}

	resolvedBranch, liveTip, err := listRemoteTip(ctx, remote, branch)
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, false, errors.Wrap(err)
	}

	// Unchanged since the prior capture: re-emit a receipt reusing the
	// prior object set, no fetch at all. The skip directive replaces the
	// old bare "no changes since prior capture" Log.
	if liveTip == priorMeta.Tip {
		r.PhaseEnd(capture_events.Verdict{
			OK: true,
			Directive: &capture_events.Directive{
				Kind:   capture_events.DirectiveSkip,
				Reason: "no changes since prior capture",
			},
		})
		res, werr := writeGitReceipt(ctx, w, remote, resolvedBranch, liveTip, optVersion(version), priorPayload.Refs)
		if werr != nil {
			return cutting_garden_plugins.ProtocolCaptureResult{}, false, werr
		}
		return res, true, nil
	}
	r.PhaseEnd(capture_events.Verdict{OK: true})

	// Seed the prior snapshot and fetch the live tip; only the delta
	// crosses the wire. A fetch failure is a soft miss → full capture
	// (the failed phase verdict carries the swallowed fetch error). The
	// old "fetching delta…" Log is folded into the phase description.
	r.PhaseStart(fmt.Sprintf("fetch delta from %s (%s..%s)",
		remote, shortHash(priorMeta.Tip), shortHash(liveTip)))
	seeded, err := seedStorer(store, priorPayload.Refs, priorMeta.Tip)
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, false, err
	}
	if ferr := fetchBranchInto(ctx, seeded, remote, resolvedBranch); ferr != nil {
		// TAP's tolerated-failure form: a bare not-ok inside a passing
		// run would fail strict harnesses, but this failure is absorbed
		// by the full-capture fallback — so it carries a TODO directive.
		r.PhaseEnd(capture_events.Verdict{
			OK:         false,
			Directive:  &capture_events.Directive{Kind: capture_events.DirectiveTodo, Reason: "fell back to full capture"},
			Diagnostic: map[string]any{"error": ferr.Error()},
		})
		return cutting_garden_plugins.ProtocolCaptureResult{}, false, nil
	}
	r.PhaseEnd(capture_events.Verdict{OK: true})

	// Incremental validity requires a fast-forward (the prior tip is an
	// ancestor of the live tip), so every prior object stays reachable.
	ff, err := isFastForward(seeded, priorMeta.Tip, liveTip)
	if err != nil || !ff {
		return cutting_garden_plugins.ProtocolCaptureResult{}, false, nil
	}

	deltaRefs, err := storeDeltaObjects(ctx, w, seeded, priorMeta.Tip, liveTip, r)
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, false, err
	}

	// Fast-forward ⇒ closure(prior) ⊆ closure(live), so the new object set
	// is the prior set plus the delta.
	allRefs := unionRefs(priorPayload.Refs, deltaRefs)
	res, werr := writeGitReceipt(ctx, w, remote, resolvedBranch, liveTip, optVersion(version), allRefs)
	if werr != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, false, werr
	}
	return res, true, nil
}

// isFastForward reports whether priorTip is an ancestor of liveTip — the
// condition under which the prior object closure is wholly contained in
// the live closure (no removals). Both commits must be present in st.
func isFastForward(st storer.EncodedObjectStorer, priorTip, liveTip string) (bool, error) {
	prior, err := object.GetCommit(st, plumbing.NewHash(priorTip))
	if err != nil {
		return false, errors.Wrapf(err, "git plugin: load prior commit %s", priorTip)
	}
	live, err := object.GetCommit(st, plumbing.NewHash(liveTip))
	if err != nil {
		return false, errors.Wrapf(err, "git plugin: load live commit %s", liveTip)
	}
	return prior.IsAncestor(live)
}

// storeDeltaObjects writes every object reachable from liveTip but not
// from priorTip — the fetched delta — into the blob store, returning one
// locked reference per object. The bytes are the raw object payloads a
// full capture stores, so an object's markl id is identical whichever
// path stored it.
func storeDeltaObjects(
	ctx context.Context,
	w capture_plugin.Writer,
	st storer.EncodedObjectStorer,
	priorTip, liveTip string,
	r cutting_garden_plugins.Reporter,
) ([]capture_plugin.Ref, error) {
	deltaHashes, err := revlist.Objects(st,
		[]plumbing.Hash{plumbing.NewHash(liveTip)},
		[]plumbing.Hash{plumbing.NewHash(priorTip)})
	if err != nil {
		return nil, errors.Wrapf(err, "git plugin: enumerate delta objects")
	}

	// Resolve every delta object up front so the Plan total can be framed
	// over the structural skeleton (commit+tree) only — matching the full
	// path. Blobs are written below but not reported individually, so
	// Plan.Items must equal the count of structural Progress emissions, not
	// len(deltaHashes).
	deltaObjects := make([]plumbing.EncodedObject, 0, len(deltaHashes))
	var structuralCount int64
	for _, h := range deltaHashes {
		obj, oerr := st.EncodedObject(plumbing.AnyObject, h)
		if oerr != nil {
			return nil, errors.Wrapf(oerr, "git plugin: resolve delta object %s", h)
		}
		deltaObjects = append(deltaObjects, obj)
		if isStructural(obj.Type()) {
			structuralCount++
		}
	}

	// The store phase starts only after the enumeration above so its
	// description carries the real structural total — mirroring the full
	// path's "store N objects" phase in storeAllObjects.
	r.PhaseStart(fmt.Sprintf("store %d delta objects", structuralCount))
	r.Plan(cutting_garden_plugins.ReportPlan{
		Items: structuralCount,
		Label: "storing git objects",
	})

	refs := make([]capture_plugin.Ref, 0, len(deltaObjects))
	var structural int64
	for _, obj := range deltaObjects {
		ref, werr := writeEncodedObject(ctx, w, obj)
		if werr != nil {
			return nil, werr
		}
		refs = append(refs, ref)
		if isStructural(obj.Type()) {
			structural++
			r.Progress(cutting_garden_plugins.ReportProgress{
				Item:  typeLabel(obj.Type()) + " " + obj.Hash().String(),
				Items: structural,
			})
		}
	}
	r.PhaseEnd(capture_events.Verdict{OK: true})
	return refs, nil
}

// unionRefs concatenates two reference lists, dropping duplicates by oid
// (alias). For a fast-forward the inputs are disjoint; the dedup guards
// against any overlap.
func unionRefs(a, b []capture_plugin.Ref) []capture_plugin.Ref {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]capture_plugin.Ref, 0, len(a)+len(b))
	for _, group := range [][]capture_plugin.Ref{a, b} {
		for _, r := range group {
			if seen[r.Alias] {
				continue
			}
			seen[r.Alias] = true
			out = append(out, r)
		}
	}
	return out
}
