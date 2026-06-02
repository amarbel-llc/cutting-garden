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
// the entire reachable object set is unchanged, so it returns with no
// clone.
//
// A moved tip means the object set changed, so DiffProtocol then clones
// the source (the object-level enumeration's unavoidable cost), lists
// every live object, and diffs that set against the objects the receipt
// captured: objects reachable now but not at capture are added (`A`),
// objects captured but no longer reachable are deleted (`D`). The tip
// move itself leads as an `M` line.
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

	objectDiffs, err := diffObjectSets(req.Context, remote, branch, payload)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}
	differences = append(differences, objectDiffs...)

	return cutting_garden_plugins.ProtocolDiffResult{Differences: differences}, nil
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
