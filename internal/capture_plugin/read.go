package capture_plugin

import (
	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
	"code.linenisgreat.com/piggy/go/pkgs/markl"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// ReadNode opens the blob identified by digest in store and parses it as a
// protocol node. It is the store-backed counterpart of ParseNode: the
// markl-id parse + MakeBlobReader + ParseNode loop that every binding's
// receipt walk needs lives here once, rather than being copied per plugin.
//
// Use this when the body is small (receipts, identity/invocation metadata,
// JCS payload bodies). For a node whose body may be large — a restored
// capture artifact — use OpenNodeBody to stream the body instead of
// buffering it.
func ReadNode(
	store blob_stores.BlobStoreInitialized,
	digest string,
) (node Node, err error) {
	var id markl.Id
	if err = id.Set(digest); err != nil {
		return Node{}, errors.Wrapf(err, "capture_plugin: parse node id %q", digest)
	}

	reader, err := store.MakeBlobReader(&id)
	if err != nil {
		return Node{}, errors.Wrapf(err, "capture_plugin: open node %s", digest)
	}
	defer errors.DeferredCloser(&err, reader)

	node, err = ParseNode(reader)
	if err != nil {
		return Node{}, errors.Wrapf(err, "capture_plugin: parse node %s", digest)
	}
	return node, nil
}
