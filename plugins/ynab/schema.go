package ynab

import (
	"net/url"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

var (
	_ cutting_garden_plugins.BodyDescriber      = (*Plugin)(nil)
	_ cutting_garden_plugins.PatchKeysDescriber = (*Plugin)(nil)
)

// DescribePatchKeys declares the patch keys of the two patchable node types
// for the host's validation (cutting-garden#404): the host drops any other key
// before PatchNode sees it and, for a strict call, refuses the op. Value-level
// rules (a category on a split, a cleared state on a reconciled transaction)
// stay PatchNode's.
func (Plugin) DescribePatchKeys() []cutting_garden_plugins.NodeTypePatchKeys {
	return []cutting_garden_plugins.NodeTypePatchKeys{
		{Tag: typeTransaction, Keys: transactionPatchFields},
		{Tag: typePayee, Keys: payeePatchFields},
	}
}

// PatchTarget says which patchable node type a patch to uri targets: a
// transaction or a payee. Every other ynab node is not patchable. It
// classifies the URI alone, with no network call.
func (Plugin) PatchTarget(uri *url.URL) (string, bool) {
	ref, err := classifyURI(uri)
	if err != nil {
		return "", false
	}
	switch ref.kind {
	case kindTransaction:
		return typeTransaction, true
	case kindPayee:
		return typePayee, true
	default:
		return "", false
	}
}

// DescribeBodies describes the patch body of the two node types PatchNode
// writes (write.go, payee_write.go), so describe_node_types reports them
// writable and names every key a patch may carry. memo is written by the patch
// path but modelled by no facet dimension, so this is where it is advertised
// (cutting-garden#410). Neither type is created, replaced or deleted here.
func (Plugin) DescribeBodies() []cutting_garden_plugins.NodeTypeBody {
	return []cutting_garden_plugins.NodeTypeBody{
		{
			Tag: typeTransaction,
			Accepts: []string{
				`patch ops take a JSON object naming only the fields to change, all applied in one request: ` +
					`"category" (a category id, "Group/Category", or a bare name when it is unique; not on a split or a transfer), ` +
					`"cleared" ("cleared" or "uncleared"; never on a reconciled transaction), ` +
					`"memo" (a string of at most 200 characters, or null to clear it; a patch field only, no facet models it), ` +
					`"payee" (an EXISTING payee's name or id; never a transfer payee, and a payee is never created). ` +
					`A key outside that set is tolerated but not in the applied list`,
				"create, put and delete ops are not supported: transactions are imported or entered in YNAB",
			},
			Example: map[string]any{"category": "Food/Dining Out", "memo": "dinner with the group"},
		},
		{
			Tag: typePayee,
			Accepts: []string{
				`patch ops take {"name": "<new name>"}: a rename, refused when another live payee holds that name ` +
					`(YNAB has no merge) and for a transfer payee`,
				"create, put and delete ops are not supported: manage payees in YNAB itself",
			},
			Example: map[string]any{"name": "Service: Anthropic"},
		},
	}
}
