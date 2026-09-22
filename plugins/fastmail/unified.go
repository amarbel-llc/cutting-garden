package fastmail

import (
	"sync"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The unified field-codec model applied to fastmail (FDR 0025, fastmail tags
// slice 1 D6): the plugin declares its three node types' fields ONCE as codec
// sets (unifiedFieldSets / DescribeUnified), and the legacy surfaces derive
// from them — the facet declaration (facet.go DescribeFacets) via
// DeriveNodeTypeFacets, the write mappings (facet_write.go
// DescribeFacetWrites) via DeriveNodeTypeFacetWrites, and the box-atom
// presentation (present.go PresentBoxAtoms) via PresentUnifiedAtoms. The
// listing-field schema (listing.go DescribeListingFields) stays hand-written
// — the SDK has no listing-field derivation helper yet — and
// TestDescribeListingFields_ConsistentWithUnified pins it to this declaration.
//
// Deliberately NOT derived, as in caldav: the facet COUNTING path
// (facet.go's threadFacets / FacetCounts), which computes each thread's
// union/any-of values from its members — the tag set through the mailbox tree
// (D1/D2), the ISO day of its newest receivedAt, the capped sender histogram.
// Only the declarations derive; TestTagsCodec_FormatAgreesWithFacetValues
// pins that the counting path and tagsCodec.Format present the same set.

var (
	_ cutting_garden_plugins.Codec            = tagsCodec{}
	_ cutting_garden_plugins.Codec            = receivedDayCodec{}
	_ cutting_garden_plugins.Codec            = facetOnlyCodec{}
	_ cutting_garden_plugins.UnifiedDescriber = (*Plugin)(nil)
)

// unifiedFieldSets is fastmail's single field declaration (FDR 0025). Order
// matters: the groupable fields project into the facet-dimension order
// describe_node_types renders, and the inline fields into the box-atom order.
// The thread type carries the designated FieldTag (`tags`, G6 — the only
// writable field anywhere); mailbox and email declare no tag notion. The D6
// box shape follows from the flags: `from` is the ONLY inline atom, `subject`
// the trailer, `date` a groupable-but-not-inline FieldDate (so `--group-by
// date=(month)` coarsens by prefix, #230), `has_attachment` a closed yes/no
// grouping dimension with no listing counterpart.
//
// Memoized (sync.OnceValue): a pure constant read on per-node paths; no
// consumer mutates the returned slices.
var unifiedFieldSets = sync.OnceValue(func() []cutting_garden_plugins.NodeTypeUnifiedFields {
	subject := cutting_garden_plugins.IdentityCodec{Field: cutting_garden_plugins.UnifiedField{
		Key: listingFieldSubject, Label: "Subject",
		Kind:    cutting_garden_plugins.FieldText,
		Trailer: true,
	}}
	// from: as the atom it is the representative sender threadFields /
	// emailFields store; as a THREAD facet it is the union of member senders
	// (multi-valued, top-N capped in FacetCounts). Read-only: a sender is
	// not an organize edit. The email variant is not groupable — emails are
	// drilled-to leaves the counting path never summarizes, so advertising a
	// grouping dimension on them would promise values that never exist; the
	// same holds for its date.
	from := func(groupable bool) cutting_garden_plugins.IdentityCodec {
		return cutting_garden_plugins.IdentityCodec{Field: cutting_garden_plugins.UnifiedField{
			Key: listingFieldFrom, Label: "From",
			Kind:        cutting_garden_plugins.FieldLabelled,
			Inline:      true,
			Groupable:   groupable,
			MultiValued: groupable,
		}}
	}
	hasAttachment := facetOnlyCodec{field: cutting_garden_plugins.UnifiedField{
		Key: facetHasAttachment, Label: "Has attachment",
		Kind:      cutting_garden_plugins.FieldCategorical,
		Groupable: true,
		Values: []cutting_garden_plugins.FieldValue{
			{Value: attachmentYes},
			{Value: attachmentNo},
		},
	}}
	count := func(key, label string) cutting_garden_plugins.IdentityCodec {
		return cutting_garden_plugins.IdentityCodec{Field: cutting_garden_plugins.UnifiedField{
			Key: key, Label: label,
			Kind: cutting_garden_plugins.FieldCategorical,
		}}
	}

	return []cutting_garden_plugins.NodeTypeUnifiedFields{
		{Tag: typeMailbox, Codecs: []cutting_garden_plugins.Codec{
			count(listingFieldThreads, "Threads"),
			count(listingFieldEmails, "Emails"),
		}},
		{Tag: typeThread, Codecs: []cutting_garden_plugins.Codec{
			tagsCodec{}, from(true), receivedDayCodec{groupable: true}, hasAttachment, subject,
		}},
		{Tag: typeEmail, Codecs: []cutting_garden_plugins.Codec{
			from(false), receivedDayCodec{}, subject,
		}},
	}
})

// DescribeUnified declares fastmail's unified field-codec model (FDR 0025) —
// the single declaration the legacy facet / write / atom surfaces derive from.
func (Plugin) DescribeUnified() []cutting_garden_plugins.NodeTypeUnifiedFields {
	return unifiedFieldSets()
}

// codecsForType resolves the codec set declared for one node type tag. nil
// for a tag with no unified declaration (the raw message leaf).
func codecsForType(tag string) []cutting_garden_plugins.Codec {
	for _, set := range unifiedFieldSets() {
		if set.Tag == tag {
			return set.Codecs
		}
	}
	return nil
}

// tagsCodec declares the thread's designated tag set (G6): a WRITABLE,
// multi-valued, groupable FieldTag under the dodder-hyphen interpreter
// (RFC 0019 §6). The stored counterpart is the `tags` listing field
// threadFields carries — the label tags joined through the mailbox tree (D1)
// plus the state tags (D2). Format presents that set; Parse persists a
// FULL-SET replacement: the interpreter's Complete has already resolved the
// thread's final membership, so the delta targets the `tags` stored field
// verbatim — the write engine (mutate.go's PatchNode) diffs it against the
// live set and fans the change out over the members. Because MultiValued makes
// the derived FacetWrite Mode `many`, the write target is the field's own key
// (Source stays empty).
type tagsCodec struct{}

func (tagsCodec) Fields() []cutting_garden_plugins.UnifiedField {
	return []cutting_garden_plugins.UnifiedField{{
		Key: listingFieldTags, Label: "Tags",
		Kind:        cutting_garden_plugins.FieldTag,
		Groupable:   true,
		MultiValued: true,
		Writable:    true,
		Interpreter: "dodder-hyphen",
	}}
}

// Format presents the stored tag set verbatim, in STORED order —
// interpreter-normalized (SortKey) ordering is the framework's render-time
// job. The stored shape is the []string threadFields builds, or the []any it
// becomes after a JSON enrichment round-trip (the wire/MCP path) — the SDK's
// StringsOf tolerates both. An absent or empty set contributes nothing
// (absent key).
func (tagsCodec) Format(stored map[string]any) (map[string][]string, error) {
	tags := cutting_garden_plugins.StringsOf(stored, listingFieldTags)
	if len(tags) == 0 {
		return map[string][]string{}, nil
	}
	return map[string][]string{listingFieldTags: tags}, nil
}

// Parse replaces the thread's tag set with exactly the complete set passed
// under the tags key. An absent set is the empty membership — a non-nil empty
// slice so a JSON-encoded patch carries [] rather than null. The current
// stored value is unused: the replacement is absolute.
func (tagsCodec) Parse(edited map[string][]string, _ map[string]any) (map[string]any, error) {
	tags := edited[listingFieldTags]
	if tags == nil {
		tags = []string{}
	}
	return map[string]any{listingFieldTags: tags}, nil
}

// receivedDayCodec declares the thread/email `date` field: a read-only
// FieldDate whose stored counterpart is the ISO-8601 receivedAt listing
// field (a thread's newest). Format presents the ISO DAY of that timestamp —
// the same YYYY-MM-DD bucket key the counting path (threadFacets) emits, so
// the presented value and the facet value agree and the framework's prefix
// machinery (#230) coarsens both identically. Not inline: the date is a
// listing field and (on the thread, groupable) a grouping dimension, never a
// box atom (D6). Parse is defensively read-only — the derived surfaces gate
// on Writable before ever calling it.
type receivedDayCodec struct {
	groupable bool // the thread groups by date; an email leaf does not
}

func (c receivedDayCodec) Fields() []cutting_garden_plugins.UnifiedField {
	return []cutting_garden_plugins.UnifiedField{{
		Key: listingFieldDate, Label: "Date",
		Kind:      cutting_garden_plugins.FieldDate,
		Groupable: c.groupable,
	}}
}

func (receivedDayCodec) Format(stored map[string]any) (map[string][]string, error) {
	raw, _ := stored[listingFieldDate].(string)
	day, _ := dayBucketOf(raw)
	if day == "" {
		return map[string][]string{}, nil
	}
	return map[string][]string{listingFieldDate: {day}}, nil
}

func (receivedDayCodec) Parse(map[string][]string, map[string]any) (map[string]any, error) {
	return nil, errors.BadRequestf("fastmail plugin: date is not writable")
}

// facetOnlyCodec declares a GROUPABLE-only field with no stored counterpart
// (has_attachment): the bucket VALUE is computed by the counting path from
// the members, so Format is empty and the field is not a listing field.
// Parse is defensively read-only, as for receivedDayCodec.
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
	return nil, errors.BadRequestf("fastmail plugin: %s is not writable", c.field.Key)
}
