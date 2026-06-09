package cutting_garden_plugin_web

import (
	"encoding/json"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// receiptPayloadRef reads a web receipt by digest and walks it to its
// single payload reference. A thin wrapper over payloadRefFromReceipt for
// callers that hold only the digest (restore, the live-capture diff path).
func receiptPayloadRef(
	store blob_stores.BlobStoreInitialized,
	receiptDigest string,
) (capture_plugin.Ref, error) {
	receipt, err := capture_plugin.ReadNode(store, receiptDigest)
	if err != nil {
		return capture_plugin.Ref{}, err
	}
	return payloadRefFromReceipt(receipt, receiptDigest)
}

// payloadRefFromReceipt walks an already-read web receipt to its single
// payload reference, verifying the receipt kind and the payload's type lock
// along the way. Splitting the read out lets DiffProtocol parse the stored
// receipt once and derive both the payload ref and the format from it.
func payloadRefFromReceipt(
	receipt capture_plugin.Node,
	receiptDigest string,
) (capture_plugin.Ref, error) {
	if kind, ok := capture_plugin.KindFromReceiptType(receipt.Type); !ok || kind != captureKind {
		return capture_plugin.Ref{}, errors.ErrorWithStackf(
			"web plugin: receipt %s is not a web receipt (type %q)",
			receiptDigest, receipt.Type,
		)
	}
	payloadRef, ok := receipt.RefByAlias("payload")
	if !ok {
		return capture_plugin.Ref{}, errors.ErrorWithStackf(
			"web plugin: receipt %s has no payload reference", receiptDigest,
		)
	}
	if err := capture_plugin.VerifyRef(payloadRef); err != nil {
		return capture_plugin.Ref{}, err
	}
	return payloadRef, nil
}

// receiptFormat recovers the capture format a receipt was produced with by
// walking receipt → identity → invocation and reading invocation.format.
// Diff re-captures with the same format so the payload digests are
// comparable.
func receiptFormat(
	store blob_stores.BlobStoreInitialized,
	receiptDigest string,
) (string, error) {
	receipt, err := capture_plugin.ReadNode(store, receiptDigest)
	if err != nil {
		return "", err
	}
	return formatFromReceipt(store, receipt, receiptDigest)
}

// formatFromReceipt recovers the capture format from an already-read
// receipt by walking receipt → identity → invocation. The receipt node is
// passed in so DiffProtocol need not re-read it; identity and invocation
// are still fetched from the store.
func formatFromReceipt(
	store blob_stores.BlobStoreInitialized,
	receipt capture_plugin.Node,
	receiptDigest string,
) (string, error) {
	idRef, ok := receipt.RefByAlias("identity")
	if !ok {
		return "", errors.ErrorWithStackf(
			"web plugin: receipt %s has no identity reference", receiptDigest,
		)
	}
	identity, err := capture_plugin.ReadNode(store, idRef.Digest)
	if err != nil {
		return "", err
	}
	invRef, ok := identity.RefByAlias("invocation")
	if !ok {
		return "", errors.ErrorWithStackf(
			"web plugin: identity %s has no invocation reference", idRef.Digest,
		)
	}
	inv, err := capture_plugin.ReadNode(store, invRef.Digest)
	if err != nil {
		return "", err
	}
	var body struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(inv.Body, &body); err != nil {
		return "", errors.Wrapf(err, "web plugin: decode invocation %s", invRef.Digest)
	}
	if body.Format == "" {
		return "", errors.ErrorWithStackf(
			"web plugin: invocation %s records no format", invRef.Digest,
		)
	}
	return body.Format, nil
}
