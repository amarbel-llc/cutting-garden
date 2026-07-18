package capture_wire

import (
	"fmt"

	"code.linenisgreat.com/cutting-garden/internal/capture_plugin"
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// DiffProtocol compares a captured receipt against the live source by
// re-capturing it (with the same format the receipt recorded) and
// comparing payload digests. Content-addressing makes this exact: an
// unchanged source re-emits the byte-identical payload blob (same
// markl id), so a digest match means no drift. A changed source yields
// one difference line naming the old and new payload digests.
//
// Relocated from plugins/web's DiffProtocol AS-IS (cutting-garden#146
// decision 4 / case-file finding): there is still no cheap freshness
// probe, so every diff pays a full re-capture. That cost is documented
// and accepted as an interim state by the #146 case file — not fixed
// by this relocation.
//
// The re-capture writes a fresh tree into the same store; identical
// blobs dedup by construction, so the cost of an unchanged diff is a
// capture round trip, not duplicated storage.
func (p *Plugin) DiffProtocol(
	req cutting_garden_plugins.ProtocolDiffRequest,
) (cutting_garden_plugins.ProtocolDiffResult, error) {
	// Parse the stored receipt once and derive both the payload ref and
	// the recorded format from it, rather than re-reading the same
	// receipt blob.
	storedReceipt, err := capture_plugin.ReadNode(req.BlobStore, req.ReceiptDigest)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}

	storedPayload, err := capture_plugin.PayloadRefOfReceipt(req.BlobStore, req.ReceiptDigest)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}

	format, err := formatFromReceipt(req.BlobStore, storedReceipt, req.ReceiptDigest)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}

	target, err := p.captureTarget(req.Source, req.RawSource)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}

	liveReceipt, err := p.capture(req.Context, req.BlobStore, req.StoreName, target, format)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}

	livePayload, err := capture_plugin.PayloadRefOfReceipt(req.BlobStore, liveReceipt)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}

	if storedPayload.Digest == livePayload.Digest {
		return cutting_garden_plugins.ProtocolDiffResult{}, nil
	}

	return cutting_garden_plugins.ProtocolDiffResult{
		Differences: []string{
			fmt.Sprintf("M %s payload %s -> %s",
				req.RawSource, storedPayload.Digest, livePayload.Digest),
		},
	}, nil
}
