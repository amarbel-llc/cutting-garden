package cutting_garden_plugin_git

import (
	"context"
	"fmt"
	"sort"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
)

var _ cutting_garden_plugins.ProtocolDiffPlugin = (*Plugin)(nil)

// DiffProtocol compares a git receipt against a live git source.
//
// It first does a cheap tip probe: `git ls-remote` the source's current
// tip (no object transfer) and compare it to the tip recorded in the
// receipt's payload. Equal ⇒ no drift, and — by git's merkle property —
// the entire reachable object set is unchanged, so it returns having
// touched nothing but the ref advertisement.
//
// A moved tip means the object set changed. DiffProtocol then negotiates
// just the differing objects (gitwire fetch-pack, `want live`/`have
// captured`) and, on a fast-forward, reports those as additions (`A`)
// with no removals. For a non-fast-forward (rebase/force-push) — where
// additions alone don't capture what became unreachable — or an
// unsupported transport, it falls back to a full clone and reports the
// exact symmetric difference (`A`/`D`). The tip move leads as an `M`
// line.
func (Plugin) DiffProtocol(
	req cutting_garden_plugins.ProtocolDiffRequest,
) (cutting_garden_plugins.ProtocolDiffResult, error) {
	remote, branch, err := remoteAndBranchFromArg(req.Source)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}

	payload, meta, err := loadReceiptPayload(req.BlobStore, req.ReceiptDigest)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}

	_, liveTip, err := resolveTip(req.Context, remote, branch)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}

	if liveTip == meta.Tip {
		return cutting_garden_plugins.ProtocolDiffResult{}, nil
	}

	differences := []string{
		fmt.Sprintf("M %s tip %s -> %s", req.RawSource, meta.Tip, liveTip),
	}

	objectDiffs, err := diffObjectsIncremental(req.Context, remote, branch, payload, meta.Tip, liveTip)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}
	differences = append(differences, objectDiffs...)

	return cutting_garden_plugins.ProtocolDiffResult{Differences: differences}, nil
}

// diffObjectsIncremental computes the object-level difference using a
// gitwire delta negotiation when possible. On a fast-forward the delta
// is exactly the added objects (no removals); otherwise it falls back to
// the full-clone symmetric difference.
func diffObjectsIncremental(
	ctx context.Context,
	remote, branch string,
	payload capture_plugin.Node,
	capturedTip, liveTip string,
) ([]string, error) {
	df, cleanup, err := negotiateDelta(ctx, remote, capturedTip, liveTip)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	if df == nil || !df.fastForward {
		return diffObjectSets(ctx, remote, branch, payload)
	}

	added := make([]string, 0, len(df.objects))
	for oid, typ := range df.objects {
		added = append(added, fmt.Sprintf("A %s %s", typ, oid))
	}
	sort.Strings(added)
	return added, nil
}

// diffObjectSets clones the live source, enumerates its objects, and
// returns the symmetric difference against the captured payload's
// objects as `A <type> <oid>` / `D <type> <oid>` lines (added first,
// each group sorted for deterministic output).
func diffObjectSets(
	ctx context.Context,
	remote, branch string,
	payload capture_plugin.Node,
) ([]string, error) {
	captured := capturedObjectTypes(payload)

	var live map[string]string
	if err := withBareClone(ctx, remote, branch, func(cloneDir, _, _ string) error {
		objs, lerr := listObjectTypes(ctx, cloneDir)
		if lerr != nil {
			return lerr
		}
		live = objs
		return nil
	}); err != nil {
		return nil, err
	}

	var added, deleted []string
	for oid, typ := range live {
		if _, ok := captured[oid]; !ok {
			added = append(added, fmt.Sprintf("A %s %s", typ, oid))
		}
	}
	for oid, typ := range captured {
		if _, ok := live[oid]; !ok {
			deleted = append(deleted, fmt.Sprintf("D %s %s", typ, oid))
		}
	}
	sort.Strings(added)
	sort.Strings(deleted)

	return append(added, deleted...), nil
}

// capturedObjectTypes maps each payload object reference's oid to its
// git type (reversed from the leaf type-string).
func capturedObjectTypes(payload capture_plugin.Node) map[string]string {
	out := make(map[string]string, len(payload.Refs))
	for _, ref := range payload.Refs {
		out[ref.Alias] = gitTypeFromObjectType(ref.TypeString)
	}
	return out
}
