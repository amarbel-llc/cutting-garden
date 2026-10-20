package ynab

import (
	"sync"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The unified field-codec model applied to ynab (FDR 0025): the plugin
// declares transaction-v1's fields ONCE here, and the facet declaration
// (facet.go DescribeFacets) derives from it. The listing-field schema
// (listing.go DescribeListingFields) stays hand-written, as in the other
// plugins, and TestDescribeListingFields_ConsistentWithUnified pins it to this
// declaration. Nothing is Writable: slice 1 is read-only (categorize writes are
// cutting-garden#331).
//
// Deliberately NOT derived: the facet COUNTING path (transactionFacets), which
// computes the bucket values from the raw rows — the day key, the band, the
// yes/no flags. Only the declarations derive.

var (
	_ cutting_garden_plugins.Codec            = facetOnlyCodec{}
	_ cutting_garden_plugins.UnifiedDescriber = (*Plugin)(nil)
)

// Listing-field keys that are not also facet dimensions: the human-readable
// projection of a transaction (cutting-garden#160).
const (
	listingAmount = "amount"
	listingMemo   = "memo"
)

// unifiedFieldSets is the plugin's single field declaration. Order matters: the
// groupable fields project into the facet-dimension order describe_node_types
// renders. Memoized (sync.OnceValue): a pure constant read on per-node paths.
var unifiedFieldSets = sync.OnceValue(func() []cutting_garden_plugins.NodeTypeUnifiedFields {
	// text is a passthrough listing field with no grouping.
	text := func(key, label string) cutting_garden_plugins.IdentityCodec {
		return cutting_garden_plugins.IdentityCodec{Field: cutting_garden_plugins.UnifiedField{
			Key: key, Label: label, Kind: cutting_garden_plugins.FieldText,
		}}
	}
	// grouped is a stored string that is also its own facet value (account,
	// category, payee): the presented value IS the bucket key.
	grouped := func(key, label string) cutting_garden_plugins.IdentityCodec {
		return cutting_garden_plugins.IdentityCodec{Field: cutting_garden_plugins.UnifiedField{
			Key: key, Label: label, Kind: cutting_garden_plugins.FieldCategorical, Groupable: true,
		}}
	}
	// facetOnly is a groupable field with no stored counterpart: its bucket
	// value is computed by the counting path from the row.
	facetOnly := func(f cutting_garden_plugins.UnifiedField) facetOnlyCodec {
		f.Groupable = true
		return facetOnlyCodec{field: f}
	}
	yesNoValues := []cutting_garden_plugins.FieldValue{{Value: valueYes}, {Value: valueNo}}

	return []cutting_garden_plugins.NodeTypeUnifiedFields{{
		Tag: typeTransaction,
		Codecs: []cutting_garden_plugins.Codec{
			// date: the stored YYYY-MM-DD is the day bucket, so `date=(month)`
			// coarsens by prefix and a `date=2026-07` filter is pushed down to
			// the server as a since/until window (window.go).
			cutting_garden_plugins.IdentityCodec{Field: cutting_garden_plugins.UnifiedField{
				Key: facetDate, Label: "Date", Kind: cutting_garden_plugins.FieldDate, Groupable: true,
			}},
			grouped(facetAccount, "Account"),
			grouped(facetCategory, "Category"),
			grouped(facetPayee, "Payee"),
			cutting_garden_plugins.IdentityCodec{Field: cutting_garden_plugins.UnifiedField{
				Key: facetCleared, Label: "Cleared", Kind: cutting_garden_plugins.FieldCategorical,
				Groupable: true,
				Values: []cutting_garden_plugins.FieldValue{
					{Value: clearedCleared}, {Value: clearedUncleared}, {Value: clearedReconciled},
				},
			}},
			facetOnly(cutting_garden_plugins.UnifiedField{
				Key: facetApproved, Label: "Approved",
				Kind: cutting_garden_plugins.FieldCategorical, Values: yesNoValues,
			}),
			facetOnly(cutting_garden_plugins.UnifiedField{
				Key: facetFlag, Label: "Flag", Kind: cutting_garden_plugins.FieldCategorical,
			}),
			facetOnly(cutting_garden_plugins.UnifiedField{
				Key: facetTransfer, Label: "Transfer",
				Kind: cutting_garden_plugins.FieldCategorical, Values: yesNoValues,
			}),
			facetOnly(cutting_garden_plugins.UnifiedField{
				Key: facetAmountBand, Label: "Amount",
				Kind: cutting_garden_plugins.FieldNumericBucket,
				Values: []cutting_garden_plugins.FieldValue{
					{Value: band1000AndOver, Order: 5},
					{Value: band200To1000, Order: 4},
					{Value: band50To200, Order: 3},
					{Value: band10To50, Order: 2},
					{Value: bandUnder10, Order: 1},
				},
			}),
			text(listingAmount, "Amount"),
			text(listingMemo, "Memo"),
		},
	}}
})

// DescribeUnified declares ynab's unified field-codec model (FDR 0025) — the
// single declaration the legacy facet surface derives from.
func (Plugin) DescribeUnified() []cutting_garden_plugins.NodeTypeUnifiedFields {
	return unifiedFieldSets()
}

// facetOnlyCodec declares a GROUPABLE-only field with no stored counterpart:
// the bucket VALUE is computed by the counting path from the row, so Format is
// empty and the field is not a listing field. Parse is defensively read-only.
type facetOnlyCodec struct {
	field cutting_garden_plugins.UnifiedField
}

func (c facetOnlyCodec) Fields() []cutting_garden_plugins.UnifiedField {
	return []cutting_garden_plugins.UnifiedField{c.field}
}

func (facetOnlyCodec) Format(map[string]any) (map[string][]string, error) {
	return map[string][]string{}, nil
}

func (c facetOnlyCodec) Parse(map[string][]string, map[string]any) (map[string]any, error) {
	return nil, errors.BadRequestf("ynab plugin: %s is not writable", c.field.Key)
}
