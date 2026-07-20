package capture_wire

import (
	"encoding/json"

	"code.linenisgreat.com/cutting-garden/internal/capture_plugin"
	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// formatFromReceipt recovers the capture format an already-read
// receipt was produced with by walking receipt -> identity ->
// invocation and reading invocation.format — the RFC 0003 web-archive
// binding's invocation shape, the shape every capture_wire-produced
// receipt has (this plugin always drives an RFC 0003-speaking binary
// like chrest). Diff re-captures with the same format so the payload
// digests are comparable. Relocated from plugins/web/node_io.go's
// formatFromReceipt.
func formatFromReceipt(
	store blob_stores.BlobStoreInitialized,
	receipt capture_plugin.Node,
	receiptDigest string,
) (string, error) {
	idRef, ok := receipt.RefByAlias("identity")
	if !ok {
		return "", errors.ErrorWithStackf(
			"capture_wire: receipt %s has no identity reference", receiptDigest,
		)
	}
	identity, err := capture_plugin.ReadNode(store, idRef.Digest)
	if err != nil {
		return "", err
	}
	invRef, ok := identity.RefByAlias("invocation")
	if !ok {
		return "", errors.ErrorWithStackf(
			"capture_wire: identity %s has no invocation reference", idRef.Digest,
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
		return "", errors.Wrapf(err, "capture_wire: decode invocation %s", invRef.Digest)
	}
	if body.Format == "" {
		return "", errors.ErrorWithStackf(
			"capture_wire: invocation %s records no format", invRef.Digest,
		)
	}
	return body.Format, nil
}
