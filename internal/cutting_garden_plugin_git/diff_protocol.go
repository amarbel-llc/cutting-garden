package cutting_garden_plugin_git

import (
	"fmt"

	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
)

var _ cutting_garden_plugins.ProtocolDiffPlugin = (*Plugin)(nil)

// DiffProtocol compares a git receipt against a live git source by
// branch tip: it resolves the source's current tip with `git ls-remote`
// (no object transfer) and compares it to the tip recorded in the
// receipt's payload. An unchanged tip means — by git's merkle property —
// an unchanged reachable object set, so it reports no drift; a moved tip
// is reported as a single difference line.
//
// This mirrors the freshness probe of the EntryV1 diff path: tip
// equality is the sound, cheap drift signal. Object-level enumeration
// (which objects were added/removed) would require cloning the source
// and is left as a follow-up.
func (Plugin) DiffProtocol(
	req cutting_garden_plugins.ProtocolDiffRequest,
) (cutting_garden_plugins.ProtocolDiffResult, error) {
	remote, branch, err := remoteAndBranchFromArg(req.Source)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}

	_, meta, err := loadReceiptPayload(req.BlobStore, req.ReceiptDigest)
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

	return cutting_garden_plugins.ProtocolDiffResult{
		Differences: []string{
			fmt.Sprintf("M %s tip %s -> %s", req.RawSource, meta.Tip, liveTip),
		},
	}, nil
}
