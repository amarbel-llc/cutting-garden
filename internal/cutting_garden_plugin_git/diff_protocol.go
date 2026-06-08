package cutting_garden_plugin_git

import (
	"context"
	"fmt"
	"sort"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/revlist"
)

var _ cutting_garden_plugins.ProtocolDiffPlugin = (*Plugin)(nil)

// DiffProtocol compares a git receipt against a live git source, entirely
// in-process via go-git (no `git` binary).
//
// It first does a cheap tip probe (`listRemoteTip`, a ref advertisement
// with no object transfer) and compares the live tip to the tip recorded
// in the receipt's payload. Equal ⇒ no drift, and — by git's merkle
// property — the entire reachable object set is unchanged, so it returns
// having transferred nothing.
//
// A moved tip means the object set changed. DiffProtocol seeds an
// in-memory storer from the captured objects, advertises the captured tip
// as a `have`, and fetches the live tip so only the delta crosses the
// wire. The exact symmetric difference between the captured object set and
// the set reachable from the live tip is reported as `A`/`D` lines under a
// leading `M` tip line — fast-forwards yield additions only, rewrites and
// force-pushes also yield deletions. There is no full-clone fallback: the
// computation is exact for every case and every go-git transport.
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

	resolvedBranch, liveTip, err := listRemoteTip(req.Context, remote, branch)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}

	if liveTip == meta.Tip {
		return cutting_garden_plugins.ProtocolDiffResult{}, nil
	}

	differences := []string{
		fmt.Sprintf("M %s tip %s -> %s", req.RawSource, meta.Tip, liveTip),
	}

	objectDiffs, err := objectGraphDiff(
		req.Context, req.BlobStore, remote, resolvedBranch, payload, meta.Tip, liveTip,
	)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}
	differences = append(differences, objectDiffs...)

	return cutting_garden_plugins.ProtocolDiffResult{Differences: differences}, nil
}

// objectGraphDiff returns the symmetric difference between the captured
// object set and the set reachable from liveTip, as sorted `A`/`D` lines.
// It seeds an in-memory storer from the captured snapshot (advertising
// capturedTip as a `have`) and fetches liveTip, so only the differing
// objects cross the wire; the union then holds everything reachable from
// liveTip, and a rev-list walk from liveTip yields its exact closure.
func objectGraphDiff(
	ctx context.Context,
	store blob_stores.BlobStoreInitialized,
	remote, resolvedBranch string,
	payload capture_plugin.Node,
	capturedTip, liveTip string,
) ([]string, error) {
	seeded, err := seedStorer(store, payload.Refs, capturedTip)
	if err != nil {
		return nil, err
	}

	if err := fetchBranchInto(ctx, seeded, remote, resolvedBranch); err != nil {
		return nil, err
	}

	liveHashes, err := revlist.Objects(seeded, []plumbing.Hash{plumbing.NewHash(liveTip)}, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "git plugin: walk objects reachable from %s", liveTip)
	}

	liveSet := make(map[string]string, len(liveHashes))
	for _, h := range liveHashes {
		obj, oerr := seeded.EncodedObject(plumbing.AnyObject, h)
		if oerr != nil {
			return nil, errors.Wrapf(oerr, "git plugin: resolve object %s", h)
		}
		liveSet[h.String()] = obj.Type().String()
	}
	capturedSet := capturedObjectTypes(payload)

	var added, deleted []string
	for oid, typ := range liveSet {
		if _, ok := capturedSet[oid]; !ok {
			added = append(added, fmt.Sprintf("A %s %s", typ, oid))
		}
	}
	for oid, typ := range capturedSet {
		if _, ok := liveSet[oid]; !ok {
			deleted = append(deleted, fmt.Sprintf("D %s %s", typ, oid))
		}
	}
	sort.Strings(added)
	sort.Strings(deleted)

	return append(added, deleted...), nil
}

// capturedObjectTypes maps each payload object reference's oid to its git
// type (reversed from the leaf type-string).
func capturedObjectTypes(payload capture_plugin.Node) map[string]string {
	out := make(map[string]string, len(payload.Refs))
	for _, ref := range payload.Refs {
		out[ref.Alias] = gitTypeFromObjectType(ref.TypeString)
	}
	return out
}
