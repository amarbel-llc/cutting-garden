package cutting_garden_plugin_web

import (
	"encoding/json"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/madder/go/pkgs/markl"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// readNode reads and parses one hyphence node blob from the store by its
// markl digest. Mirrors the git binding's readNode.
func readNode(
	store blob_stores.BlobStoreInitialized,
	digest string,
) (node capture_plugin.Node, err error) {
	var id markl.Id
	if err = id.Set(digest); err != nil {
		return capture_plugin.Node{}, errors.Wrapf(err, "web plugin: parse blob id %q", digest)
	}

	reader, err := store.MakeBlobReader(&id)
	if err != nil {
		return capture_plugin.Node{}, errors.Wrapf(err, "web plugin: open blob %s", digest)
	}
	defer errors.DeferredCloser(&err, reader)

	node, err = capture_plugin.ParseNode(reader)
	return
}

// receiptPayloadRef walks a web receipt to its single payload reference,
// verifying the receipt kind and the payload's type lock along the way.
func receiptPayloadRef(
	store blob_stores.BlobStoreInitialized,
	receiptDigest string,
) (capture_plugin.Ref, error) {
	receipt, err := readNode(store, receiptDigest)
	if err != nil {
		return capture_plugin.Ref{}, err
	}
	if kind, ok := capture_plugin.KindFromReceiptType(receipt.Type); !ok || kind != captureKind {
		return capture_plugin.Ref{}, errors.ErrorWithStackf(
			"web plugin: receipt %s is not a web receipt (type %q)",
			receiptDigest, receipt.Type)
	}
	payloadRef, ok := receipt.RefByAlias("payload")
	if !ok {
		return capture_plugin.Ref{}, errors.ErrorWithStackf(
			"web plugin: receipt %s has no payload reference", receiptDigest)
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
	receipt, err := readNode(store, receiptDigest)
	if err != nil {
		return "", err
	}
	idRef, ok := receipt.RefByAlias("identity")
	if !ok {
		return "", errors.ErrorWithStackf(
			"web plugin: receipt %s has no identity reference", receiptDigest)
	}
	identity, err := readNode(store, idRef.Digest)
	if err != nil {
		return "", err
	}
	invRef, ok := identity.RefByAlias("invocation")
	if !ok {
		return "", errors.ErrorWithStackf(
			"web plugin: identity %s has no invocation reference", idRef.Digest)
	}
	inv, err := readNode(store, invRef.Digest)
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
			"web plugin: invocation %s records no format", invRef.Digest)
	}
	return body.Format, nil
}
