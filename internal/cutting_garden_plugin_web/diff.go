package cutting_garden_plugin_web

import (
	"fmt"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
)

// DiffProtocol compares a captured web receipt against the live URL by
// re-capturing it (with the same format the receipt recorded) and
// comparing payload digests. Content-addressing makes this exact: an
// unchanged page re-emits the byte-identical payload blob (same markl id),
// so a digest match means no drift. A changed page yields one difference
// line naming the old and new payload digests.
//
// The re-capture writes a fresh tree into the same store; identical blobs
// dedup by construction, so the cost of an unchanged diff is a browser
// fetch, not duplicated storage.
func (Plugin) DiffProtocol(
	req cutting_garden_plugins.ProtocolDiffRequest,
) (cutting_garden_plugins.ProtocolDiffResult, error) {
	// Parse the stored receipt once and derive both the payload ref and the
	// recorded format from it, rather than re-reading the same receipt blob.
	storedReceipt, err := capture_plugin.ReadNode(req.BlobStore, req.ReceiptDigest)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}

	storedPayload, err := payloadRefFromReceipt(storedReceipt, req.ReceiptDigest)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}

	format, err := formatFromReceipt(req.BlobStore, storedReceipt, req.ReceiptDigest)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}

	target, err := captureTarget(req.Source, req.RawSource)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}

	liveReceipt, err := capture(req.Context, req.StoreName, target, format)
	if err != nil {
		return cutting_garden_plugins.ProtocolDiffResult{}, err
	}

	livePayload, err := receiptPayloadRef(req.BlobStore, liveReceipt)
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
