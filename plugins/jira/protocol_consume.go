package jira

import (
	"encoding/json"

	"github.com/amarbel-llc/cutting-garden/pkgs/capture_plugin"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// outcomeIndexType is the jira plugin-outcome node: the per-issue reuse
// index (key → {updated, issue-node digest}) recorded under the receipt's
// outcome subtree (FDR 0019 §Severing). The NEXT capture's loadPriorIndex
// reads it to graft unchanged issue subtrees by markl-id. Registered in
// types_register.go's init alongside the tree node types.
const outcomeIndexType = "jcs-jira-outcome-index-v1"

// issueIndexEntry is one issue's reuse record: its key, its `updated`
// timestamp at capture time, and the markl-id of its issue-node subtree.
// Serialized into the outcome index and read back as a priorIssue.
type issueIndexEntry struct {
	Key     string
	Updated string
	Digest  string
}

// priorIssue is the decoded reuse record for one issue from a prior
// receipt's outcome index: the `updated` freshness value and the issue-node
// digest to graft when `updated` is unchanged.
type priorIssue struct {
	updated string
	digest  string
}

// indexBody is the JCS shape outcomePlugin emits and loadPriorIndex reads.
type indexBody struct {
	Issues []struct {
		Key     string `json:"key"`
		Updated string `json:"updated"`
		Digest  string `json:"digest"`
	} `json:"issues"`
}

// loadPriorIndex resolves a prior receipt digest to its per-issue reuse
// index (key → {updated, digest}). It is deliberately best-effort: an empty
// digest, an unreadable receipt, a non-jira receipt, or a missing/garbled
// outcome index all yield an empty map, which disables reuse and forces a
// full re-fetch of every issue — never an error, so a capture always
// succeeds even against an unfamiliar prior receipt.
func loadPriorIndex(store blob_stores.BlobStoreInitialized, receiptDigest string) map[string]priorIssue {
	if receiptDigest == "" {
		return nil
	}
	body, err := readOutcomeIndex(store, receiptDigest)
	if err != nil {
		return nil
	}
	prior := make(map[string]priorIssue, len(body.Issues))
	for _, e := range body.Issues {
		if e.Key == "" || e.Digest == "" {
			continue
		}
		prior[e.Key] = priorIssue{updated: e.Updated, digest: e.Digest}
	}
	return prior
}

// readOutcomeIndex walks receipt → outcome → plugin (the jira reuse index),
// verifying it is a jira-kind receipt and that the index node carries the
// expected type lock, and decodes the index body.
func readOutcomeIndex(store blob_stores.BlobStoreInitialized, receiptDigest string) (indexBody, error) {
	receipt, err := capture_plugin.ReadNode(store, receiptDigest)
	if err != nil {
		return indexBody{}, err
	}
	if kind, ok := capture_plugin.KindFromReceiptType(receipt.Type); !ok || kind != captureKind {
		return indexBody{}, errors.ErrorWithStackf(
			"jira plugin: receipt %s is not a jira receipt (type %q)", receiptDigest, receipt.Type,
		)
	}

	outcomeRef, ok := receipt.RefByAlias("outcome")
	if !ok {
		return indexBody{}, errors.ErrorWithStackf("jira plugin: receipt %s has no outcome ref", receiptDigest)
	}
	outcome, err := capture_plugin.ReadNode(store, outcomeRef.Digest)
	if err != nil {
		return indexBody{}, err
	}

	indexRef, ok := outcome.RefByAlias("plugin")
	if !ok {
		return indexBody{}, errors.ErrorWithStackf("jira plugin: outcome %s has no plugin index ref", outcomeRef.Digest)
	}
	if err := capture_plugin.VerifyRef(indexRef); err != nil {
		return indexBody{}, err
	}
	index, err := capture_plugin.ReadNode(store, indexRef.Digest)
	if err != nil {
		return indexBody{}, err
	}

	var body indexBody
	if err := json.Unmarshal(index.Body, &body); err != nil {
		return indexBody{}, errors.Wrapf(err, "jira plugin: decode outcome index %s", indexRef.Digest)
	}
	return body, nil
}
