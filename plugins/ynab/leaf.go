package ynab

import (
	"context"
	"encoding/json"
	"net/url"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// ReadLeaf fetches a leaf node's content. Only a transaction or payee node is
// readable: its structured view is its JSON and its raw bytes are the same
// JSON, stamped application/json. Every other node kind reports ok=false so the
// consumer falls back to the child listing. node MUST be non-nil.
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
	if ref.kind != kindTransaction && ref.kind != kindPayee {
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
	var body any
	if ref.kind == kindPayee {
		payee, err := c.payee(ctx, budgetID, ref.payeeID)
		if err != nil {
			return cutting_garden_plugins.LeafContent{}, false, err
		}
		body = payee
	} else {
		txn, err := c.transaction(ctx, budgetID, ref.txnID)
		if err != nil {
			return cutting_garden_plugins.LeafContent{}, false, err
		}
		body = txn
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return cutting_garden_plugins.LeafContent{}, false, errors.Wrap(err)
	}
	return cutting_garden_plugins.LeafContent{
		Structured:  body,
		Raw:         raw,
		RawMimeType: mimeJSON,
	}, true, nil
}
