package org

import (
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The headline type's fields, declared once (FDR 0025); the facet schema
// derives from it. Nothing is writable in slice 1. Not memoized: the state
// dimension's terminal values come from the configured accounts.
func unifiedFieldSets() []cutting_garden_plugins.NodeTypeUnifiedFields {
	identity := func(f cutting_garden_plugins.UnifiedField) cutting_garden_plugins.Codec {
		return cutting_garden_plugins.IdentityCodec{Field: f}
	}
	band := func(key, label string) cutting_garden_plugins.Codec {
		values := make([]cutting_garden_plugins.FieldValue, len(bandValues))
		for i, v := range bandValues {
			values[i] = cutting_garden_plugins.FieldValue{Value: v}
		}
		return facetOnlyCodec{field: cutting_garden_plugins.UnifiedField{
			Key: key, Label: label,
			Kind:            cutting_garden_plugins.FieldNumericBucket,
			Groupable:       true,
			Values:          values,
			RevalidateAfter: bandRevalidate,
		}}
	}
	date := func(key, label string) cutting_garden_plugins.Codec {
		return identity(cutting_garden_plugins.UnifiedField{
			Key: key, Label: label, Kind: cutting_garden_plugins.FieldDate, Groupable: true,
		})
	}
	return []cutting_garden_plugins.NodeTypeUnifiedFields{{
		Tag: typeHeadline,
		Codecs: []cutting_garden_plugins.Codec{
			identity(cutting_garden_plugins.UnifiedField{
				Key: fieldState, Label: "State",
				Kind:           cutting_garden_plugins.FieldCategorical,
				Groupable:      true,
				TerminalValues: allDoneKeywords(),
			}),
			tagsCodec{},
			identity(cutting_garden_plugins.UnifiedField{
				Key: fieldPriority, Label: "Priority",
				Kind:      cutting_garden_plugins.FieldCategorical,
				Groupable: true,
			}),
			date(fieldScheduled, "Scheduled"),
			date(fieldDeadline, "Deadline"),
			date(fieldClosed, "Closed"),
			band(fieldScheduledBand, "Scheduled"),
			band(fieldDeadlineBand, "Deadline"),
			identity(cutting_garden_plugins.UnifiedField{
				Key: fieldFile, Label: "File",
				Kind:      cutting_garden_plugins.FieldCategorical,
				Groupable: true,
			}),
			identity(cutting_garden_plugins.UnifiedField{
				Key: fieldTitle, Label: "Title",
				Kind:    cutting_garden_plugins.FieldText,
				Trailer: true,
			}),
		},
	}}
}

// DescribeUnified declares the org plugin's unified field model.
func (Plugin) DescribeUnified() []cutting_garden_plugins.NodeTypeUnifiedFields {
	return unifiedFieldSets()
}

// DescribeFacets derives the facet schema from the unified declaration.
func (Plugin) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	return cutting_garden_plugins.DeriveNodeTypeFacets(unifiedFieldSets())
}

// DescribeListingFields declares the Node.Fields keys each type carries.
func (Plugin) DescribeListingFields() []cutting_garden_plugins.NodeTypeListingFields {
	field := func(key, label string) cutting_garden_plugins.ListingField {
		return cutting_garden_plugins.ListingField{Key: key, Label: label}
	}
	return []cutting_garden_plugins.NodeTypeListingFields{
		{Tag: typeFile, Fields: []cutting_garden_plugins.ListingField{field(fieldModified, "Modified")}},
		{Tag: typeHeadline, Fields: []cutting_garden_plugins.ListingField{
			field(fieldState, "State"),
			field(fieldPriority, "Priority"),
			{Key: fieldTitle, Label: "Title", Trailer: true},
			field(fieldScheduled, "Scheduled"),
			field(fieldDeadline, "Deadline"),
			field(fieldClosed, "Closed"),
			field(fieldTags, "Tags"),
			field(fieldOwnTags, "Own tags"),
			field(fieldFile, "File"),
			field(fieldLevel, "Level"),
			field(fieldIDSynthetic, "Synthetic id"),
		}},
	}
}

// tagsCodec declares the effective tag set (D4) as the type's designated
// FieldTag under dodder-hyphen (D11). Read-only in slice 1.
type tagsCodec struct{}

func (tagsCodec) Fields() []cutting_garden_plugins.UnifiedField {
	return []cutting_garden_plugins.UnifiedField{{
		Key: fieldTags, Label: "Tags",
		Kind:        cutting_garden_plugins.FieldTag,
		Groupable:   true,
		MultiValued: true,
		Interpreter: "dodder-hyphen",
	}}
}

func (tagsCodec) Format(stored map[string]any) (map[string][]string, error) {
	tags := cutting_garden_plugins.StringsOf(stored, fieldTags)
	if len(tags) == 0 {
		return map[string][]string{}, nil
	}
	return map[string][]string{fieldTags: tags}, nil
}

func (tagsCodec) Parse(map[string][]string, map[string]any) (map[string]any, error) {
	return nil, errors.BadRequestf("org plugin: tags are read-only")
}

// facetOnlyCodec is a groupable dimension computed by the counting path
// with no stored counterpart (the date bands).
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
	return nil, errors.BadRequestf("org plugin: %s is not writable", c.field.Key)
}
