package ynab

import (
	"context"
	"encoding/json"
	"net/url"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// ReadLeaf fetches a leaf node's content. Only a transaction node is
// readable: its structured view is the transaction JSON and its raw bytes are
// the same JSON, stamped application/json. Every other node kind reports
// ok=false so the consumer falls back to the child listing. node MUST be
// non-nil.
func (Plugin) ReadLeaf(
	ctx context.Context, node *url.URL,
) (cutting_garden_plugins.LeafContent, bool, error) {
	if node == nil {
		return cutting_garden_plugins.LeafContent{}, false, errors.ErrorWithStackf(
			"ynab plugin: ReadLeaf requires a node URI",
		)
	}
	ref, err := classifyURI(node)
	if err != nil {
		return cutting_garden_plugins.LeafContent{}, false, err
	}
	if ref.kind != kindTransaction {
		return cutting_garden_plugins.LeafContent{}, false, nil
	}
	c, err := resolveClient(ref)
	if err != nil {
		return cutting_garden_plugins.LeafContent{}, false, err
	}
	budgetID, err := resolveBudgetID(ctx, c, ref)
	if err != nil {
		return cutting_garden_plugins.LeafContent{}, false, err
	}
	txn, err := c.transaction(ctx, budgetID, ref.txnID)
	if err != nil {
		return cutting_garden_plugins.LeafContent{}, false, err
	}
	raw, err := json.Marshal(txn)
	if err != nil {
		return cutting_garden_plugins.LeafContent{}, false, errors.Wrap(err)
	}
	return cutting_garden_plugins.LeafContent{
		Structured:  txn,
		Raw:         raw,
		RawMimeType: mimeJSON,
	}, true, nil
}
