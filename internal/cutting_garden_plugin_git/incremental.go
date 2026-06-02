package cutting_garden_plugin_git

import (
	"context"
	"os"
	"sort"
	"strings"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/cutting-garden/internal/gitwire"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// deltaFetch is the result of a gitwire negotiation: a scratch repo
// holding only the objects reachable from the live tip but not from the
// captured tip, the oid→git-type map of those objects, and whether the
// change is a fast-forward of the captured tip.
type deltaFetch struct {
	scratchDir  string
	objects     map[string]string
	fastForward bool
}

// negotiateDelta fetches the delta between capturedTip and liveTip into a
// fresh scratch repo via gitwire (transferring only the differing
// objects), and determines whether liveTip fast-forwards capturedTip
// (i.e. capturedTip is a parent of one of the fetched commits, which —
// since the delta is closure(liveTip) − closure(capturedTip) — holds iff
// capturedTip is an ancestor of liveTip).
//
// A nil df signals "fall back to a full clone": the transport is
// unsupported or the negotiation failed. cleanup must always be called.
func negotiateDelta(
	ctx context.Context,
	remote, capturedTip, liveTip string,
) (df *deltaFetch, cleanup func(), err error) {
	scratch, err := os.MkdirTemp("", "cg-git-delta-*")
	if err != nil {
		return nil, func() {}, errors.Wrap(err)
	}
	cleanup = func() { _ = os.RemoveAll(scratch) }

	if err := runGit(ctx, "", "init", "-q", scratch); err != nil {
		cleanup()
		return nil, func() {}, err
	}

	if ferr := gitwire.FetchDelta(ctx, remote, liveTip, []string{capturedTip}, scratch); ferr != nil {
		// Unsupported transport or any negotiation failure → fall back to
		// the full-clone path; not a hard error.
		cleanup()
		return nil, func() {}, nil
	}

	objs, oerr := listObjectTypes(ctx, scratch)
	if oerr != nil {
		cleanup()
		return nil, func() {}, nil
	}

	ff, ferr := capturedTipIsDeltaParent(ctx, scratch, capturedTip, objs)
	if ferr != nil {
		cleanup()
		return nil, func() {}, nil
	}

	return &deltaFetch{scratchDir: scratch, objects: objs, fastForward: ff}, cleanup, nil
}

// capturedTipIsDeltaParent reports whether capturedTip appears as a
// parent of any commit in the delta. Because the delta is exactly
// closure(liveTip) − closure(capturedTip), this is true iff capturedTip
// is an ancestor of liveTip — the fast-forward condition under which the
// captured object set is wholly preserved (no removals).
func capturedTipIsDeltaParent(
	ctx context.Context,
	scratchDir, capturedTip string,
	objects map[string]string,
) (bool, error) {
	for oid, typ := range objects {
		if typ != "commit" {
			continue
		}
		out, err := gitOutput(ctx, scratchDir, "cat-file", "commit", oid)
		if err != nil {
			return false, err
		}
		for _, line := range strings.Split(out, "\n") {
			if line == "" {
				break // end of commit header
			}
			if strings.HasPrefix(line, "parent ") &&
				strings.TrimSpace(strings.TrimPrefix(line, "parent ")) == capturedTip {
				return true, nil
			}
		}
	}
	return false, nil
}

// tryIncrementalCapture rebuilds a receipt for liveTip from a prior
// receipt plus only the objects that changed since it. Returns ok=false
// (no error) to mean "fall back to a full capture": the prior receipt is
// unreadable, the transport is unsupported, or the change is not a
// fast-forward (so the captured object set is not wholly preserved).
func tryIncrementalCapture(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	w capture_plugin.Writer,
	remote, branch, priorReceiptDigest string,
) (cutting_garden_plugins.ProtocolCaptureResult, bool, error) {
	priorPayload, priorMeta, err := loadReceiptPayload(store, priorReceiptDigest)
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, false, nil
	}

	resolvedBranch, liveTip, err := resolveTip(ctx, remote, branch)
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, false, errors.Wrap(err)
	}

	// Unchanged since the prior capture: re-emit a receipt reusing the
	// prior object set, no fetch at all.
	if liveTip == priorMeta.Tip {
		res, werr := writeGitReceipt(ctx, w, remote, resolvedBranch, liveTip, priorPayload.Refs)
		if werr != nil {
			return cutting_garden_plugins.ProtocolCaptureResult{}, false, werr
		}
		return res, true, nil
	}

	df, cleanup, derr := negotiateDelta(ctx, remote, priorMeta.Tip, liveTip)
	if derr != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, false, derr
	}
	defer cleanup()
	if df == nil || !df.fastForward {
		return cutting_garden_plugins.ProtocolCaptureResult{}, false, nil
	}

	deltaRefs, serr := storeDeltaObjects(ctx, w, df)
	if serr != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, false, serr
	}

	// Fast-forward ⇒ closure(captured) ⊆ closure(live), so the new object
	// set is the prior set plus the delta.
	allRefs := unionRefs(priorPayload.Refs, deltaRefs)
	res, werr := writeGitReceipt(ctx, w, remote, resolvedBranch, liveTip, allRefs)
	if werr != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, false, werr
	}
	return res, true, nil
}

// storeDeltaObjects writes every fetched delta object into the blob
// store and returns one locked reference per object. The bytes come from
// `git cat-file <type> <oid>` (the raw object payload), identical to what
// the full-clone path streams, so an object's markl id is the same
// whichever path stored it.
func storeDeltaObjects(
	ctx context.Context,
	w capture_plugin.Writer,
	df *deltaFetch,
) ([]capture_plugin.Ref, error) {
	oids := make([]string, 0, len(df.objects))
	for oid := range df.objects {
		oids = append(oids, oid)
	}
	sort.Strings(oids)

	refs := make([]capture_plugin.Ref, 0, len(oids))
	for _, oid := range oids {
		typ := df.objects[oid]
		payload, err := gitOutput(ctx, df.scratchDir, "cat-file", typ, oid)
		if err != nil {
			return nil, err
		}
		digest, _, werr := w.WriteBlob(ctx, strings.NewReader(payload))
		if werr != nil {
			return nil, errors.Wrap(werr)
		}
		refs = append(refs, capture_plugin.LockedRef(oid, digest, objectTypeString(typ)))
	}
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
