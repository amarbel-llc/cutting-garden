package cutting_garden_plugin_git

import (
	"encoding/json"
	"strings"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/madder/go/pkgs/markl"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// payloadMeta is the JCS body of the jcs-git-capture-payload-v1 node.
type payloadMeta struct {
	Remote      string `json:"remote"`
	Branch      string `json:"branch"`
	Tip         string `json:"tip"`
	ObjectCount int    `json:"object_count"`
}

// readNode fetches and parses one protocol node blob by its markl
// digest string.
func readNode(
	store blob_stores.BlobStoreInitialized,
	digest string,
) (node capture_plugin.Node, err error) {
	var id markl.Id
	if err = id.Set(digest); err != nil {
		return capture_plugin.Node{}, errors.Wrapf(err, "parse node id %q", digest)
	}
	reader, err := store.MakeBlobReader(&id)
	if err != nil {
		return capture_plugin.Node{}, errors.Wrapf(err, "open node %s", digest)
	}
	defer errors.DeferredCloser(&err, reader)

	node, err = capture_plugin.ParseNode(reader)
	if err != nil {
		return capture_plugin.Node{}, errors.Wrapf(err, "parse node %s", digest)
	}
	return node, nil
}

// loadReceiptPayload reads the receipt node, follows its `payload`
// reference to the git payload node, and returns that node plus its
// decoded metadata. It validates the receipt is a git-kind receipt.
func loadReceiptPayload(
	store blob_stores.BlobStoreInitialized,
	receiptDigest string,
) (capture_plugin.Node, payloadMeta, error) {
	receipt, err := readNode(store, receiptDigest)
	if err != nil {
		return capture_plugin.Node{}, payloadMeta{}, err
	}
	if kind, ok := capture_plugin.KindFromReceiptType(receipt.Type); !ok || kind != captureKind {
		return capture_plugin.Node{}, payloadMeta{}, errors.ErrorWithStackf(
			"git plugin: receipt %s is not a git receipt (type %q)",
			receiptDigest, receipt.Type)
	}

	payloadRef, ok := receipt.RefByAlias("payload")
	if !ok {
		return capture_plugin.Node{}, payloadMeta{}, errors.ErrorWithStackf(
			"git plugin: receipt %s has no payload reference", receiptDigest)
	}

	payload, err := readNode(store, payloadRef.Digest)
	if err != nil {
		return capture_plugin.Node{}, payloadMeta{}, err
	}

	var meta payloadMeta
	if err := json.Unmarshal(payload.Body, &meta); err != nil {
		return capture_plugin.Node{}, payloadMeta{}, errors.Wrapf(err,
			"git plugin: decode payload body of %s", payloadRef.Digest)
	}
	if meta.Tip == "" {
		return capture_plugin.Node{}, payloadMeta{}, errors.ErrorWithStackf(
			"git plugin: payload %s records no tip", payloadRef.Digest)
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
