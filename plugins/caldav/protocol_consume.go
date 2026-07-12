package caldav

import (
	"encoding/json"

	"code.linenisgreat.com/cutting-garden/pkgs/capture_plugin"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// resourceRecord is one entry in the caldav payload body's `resources`
// list: the native identity, the server-relative href (the diff probe's
// match key), and the etag captured at the time (the freshness signal).
// Mirrors the JCS shape writeCaldavReceipt emits.
type resourceRecord struct {
	ID   string `json:"id"`
	Href string `json:"href"`
	Etag string `json:"etag"`
}

// payloadMeta is the decoded caldav payload body.
type payloadMeta struct {
	Endpoint    string           `json:"endpoint"`
	ObjectCount int              `json:"object_count"`
	Resources   []resourceRecord `json:"resources"`
}

// loadReceiptPayload reads the receipt, validates it is a caldav-kind
// receipt, follows its `payload` reference, verifies the FDR-0001 type
// locks, and returns the payload node plus its decoded body. The consume
// side of CaptureProtocol — shared by DiffProtocol (and, later, restore).
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
			"caldav plugin: receipt %s is not a caldav receipt (type %q)",
			receiptDigest, receipt.Type,
		)
	}

	payloadRef, ok := receipt.RefByAlias("payload")
	if !ok {
		return capture_plugin.Node{}, payloadMeta{}, errors.ErrorWithStackf(
			"caldav plugin: receipt %s has no payload reference", receiptDigest,
		)
	}
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
			"caldav plugin: decode payload body of %s", payloadRef.Digest)
	}

	return payload, meta, nil
}
