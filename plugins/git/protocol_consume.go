package cutting_garden_plugin_git

import (
	"encoding/json"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/capture_plugin"
	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// payloadMeta is the JCS body of the jcs-git-capture-payload-v1 node.
type payloadMeta struct {
	Remote      string `json:"remote"`
	Branch      string `json:"branch"`
	Tip         string `json:"tip"`
	ObjectCount int    `json:"object_count"`
}

// loadReceiptPayload reads the receipt node, follows its `payload`
// reference to the git payload node, and returns that node plus its
// decoded metadata. It validates the receipt is a git-kind receipt.
func loadReceiptPayload(
	store blob_stores.BlobStoreInitialized,
	receiptDigest string,
) (capture_plugin.Node, payloadMeta, error) {
	receipt, err := capture_plugin.ReadNode(store, receiptDigest)
	if err != nil {
		return capture_plugin.Node{}, payloadMeta{}, err
	}
	if kind, ok := capture_plugin.KindFromReceiptType(receipt.Type); !ok || kind != captureKind {
		return capture_plugin.Node{}, payloadMeta{}, errors.ErrorWithStackf(
			"git plugin: receipt %s is not a git receipt (type %q)",
			receiptDigest, receipt.Type,
		)
	}

	payloadRef, ok := receipt.RefByAlias("payload")
	if !ok {
		return capture_plugin.Node{}, payloadMeta{}, errors.ErrorWithStackf(
			"git plugin: receipt %s has no payload reference", receiptDigest,
		)
	}
	// Honor the FDR-0001 type lock: a signed reference whose signature
	// disagrees with this binary's type definition is rejected.
	if err := capture_plugin.VerifyRef(payloadRef); err != nil {
		return capture_plugin.Node{}, payloadMeta{}, err
	}

	payload, err := capture_plugin.ReadNode(store, payloadRef.Digest)
	if err != nil {
		return capture_plugin.Node{}, payloadMeta{}, err
	}

	for _, objRef := range payload.Refs {
		if err := capture_plugin.VerifyRef(objRef); err != nil {
			return capture_plugin.Node{}, payloadMeta{}, err
		}
	}

	var meta payloadMeta
	if err := json.Unmarshal(payload.Body, &meta); err != nil {
		return capture_plugin.Node{}, payloadMeta{}, errors.Wrapf(err,
			"git plugin: decode payload body of %s", payloadRef.Digest)
	}
	if meta.Tip == "" {
		return capture_plugin.Node{}, payloadMeta{}, errors.ErrorWithStackf(
			"git plugin: payload %s records no tip", payloadRef.Digest,
		)
	}

	return payload, meta, nil
}

// gitTypeFromObjectType reverses objectTypeString:
// "git-capture-object-commit-v1" → "commit". Returns "" if the
// type-string is not a git object leaf type.
func gitTypeFromObjectType(typeString string) string {
	const prefix = "git-capture-object-"
	const suffix = "-v1"
	if !strings.HasPrefix(typeString, prefix) || !strings.HasSuffix(typeString, suffix) {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(typeString, prefix), suffix)
}
