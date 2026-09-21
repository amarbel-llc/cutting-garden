package fastmail

import (
	"slices"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// TestUnifiedFieldSetsValidate pins fastmail's declaration against the SDK's
// cross-codec invariants — G6's one-FieldTag-per-type rule, which
// PresentUnifiedTags relies on. Like caldav, this unit pin IS the plugin's
// validation site.
func TestUnifiedFieldSetsValidate(t *testing.T) {
	if err := cutting_garden_plugins.ValidateUnifiedFieldSets(unifiedFieldSets()); err != nil {
		t.Fatalf("ValidateUnifiedFieldSets(unifiedFieldSets()) = %v, want nil", err)
	}
}

// fieldsOfType flattens one type's unified declaration by field key.
func fieldsOfType(t *testing.T, tag string) map[string]cutting_garden_plugins.UnifiedField {
	t.Helper()
	codecs := codecsForType(tag)
	if codecs == nil {
		t.Fatalf("codecsForType(%q) = nil, want a declaration", tag)
	}
	out := map[string]cutting_garden_plugins.UnifiedField{}
	for _, c := range codecs {
		for _, f := range c.Fields() {
			if _, dup := out[f.Key]; dup {
				t.Fatalf("%s declares field %q twice", tag, f.Key)
			}
			out[f.Key] = f
		}
	}
	return out
}

// TestUnifiedDeclaration_ThreadShape pins D6 (fastmail tags slice 1): the
// thread's designated FieldTag is `tags` (dodder-hyphen, writable, groupable,
// multi-valued); `from` is the ONLY inline atom; `subject` is the trailer;
// `date` is a groupable FieldDate that is NOT inline; `has_attachment` is a
// closed yes/no groupable categorical; everything but `tags` is read-only.
func TestUnifiedDeclaration_ThreadShape(t *testing.T) {
	fields := fieldsOfType(t, typeThread)

	tags := fields[listingFieldTags]
	if tags.Kind != cutting_garden_plugins.FieldTag || !tags.Groupable ||
		!tags.MultiValued || !tags.Writable || tags.Interpreter != "dodder-hyphen" {
		t.Errorf("tags field = %+v, want FieldTag groupable multi-valued writable dodder-hyphen", tags)
	}
	if tags.Inline {
		t.Error("tags is inline; the tag set renders key-free, never as a tags= atom")
	}

	from := fields[listingFieldFrom]
	if !from.Inline || !from.Groupable || from.Writable {
		t.Errorf("from field = %+v, want inline groupable read-only", from)
	}
	subject := fields[listingFieldSubject]
	if !subject.Trailer || subject.Writable {
		t.Errorf("subject field = %+v, want the read-only trailer", subject)
	}
	date := fields[listingFieldDate]
	if date.Kind != cutting_garden_plugins.FieldDate || !date.Groupable || date.Inline || date.Writable {
		t.Errorf("date field = %+v, want FieldDate groupable non-inline read-only", date)
	}
	att := fields[facetHasAttachment]
	if att.Kind != cutting_garden_plugins.FieldCategorical || !att.Groupable || att.Inline || att.Writable {
		t.Errorf("has_attachment field = %+v, want categorical groupable non-inline read-only", att)
	}
	var attValues []string
	for _, v := range att.Values {
		attValues = append(attValues, v.Value)
	}
	if !slices.Equal(attValues, []string{attachmentYes, attachmentNo}) {
		t.Errorf("has_attachment values = %v, want [yes no]", attValues)
	}

	for key, f := range fields {
		if f.Inline && key != listingFieldFrom {
			t.Errorf("%q is inline; from is the only inline atom (D6)", key)
		}
	}
	for _, retired := range []string{"read", "flagged", "folder", "year", "tag"} {
		if _, present := fields[retired]; present {
			t.Errorf("retired dimension %q is still declared", retired)
		}
	}
}

// TestUnifiedDeclaration_OnlyThreadHasTags pins G6's scope: the mailbox and
// email types declare no FieldTag field, and the mailbox counts are plain
// read-only, non-groupable, non-inline listing fields.
func TestUnifiedDeclaration_OnlyThreadHasTags(t *testing.T) {
	for _, tag := range []string{typeMailbox, typeEmail} {
		for key, f := range fieldsOfType(t, tag) {
			if f.Kind == cutting_garden_plugins.FieldTag {
				t.Errorf("%s declares FieldTag field %q; only the thread type carries tags", tag, key)
			}
			if f.Writable {
				t.Errorf("%s field %q is writable; the non-thread types are read-only", tag, key)
			}
		}
	}
	mailbox := fieldsOfType(t, typeMailbox)
	for _, key := range []string{listingFieldThreads, listingFieldEmails} {
		f, ok := mailbox[key]
		if !ok {
			t.Errorf("mailbox lacks the %q count field", key)
			continue
		}
		if f.Groupable || f.Inline || f.Trailer {
			t.Errorf("mailbox %q = %+v, want a plain listing field", key, f)
		}
	}
	email := fieldsOfType(t, typeEmail)
	if !email[listingFieldSubject].Trailer || !email[listingFieldFrom].Inline {
		t.Errorf("email fields = %+v, want subject trailer + from inline", email)
	}
}

func TestTagsCodec_Format(t *testing.T) {
	c := tagsCodec{}
	for name, stored := range map[string]map[string]any{
		"native":      {listingFieldTags: []string{"_inbox", "receipts"}},
		"json-round":  {listingFieldTags: []any{"_inbox", "receipts"}},
		"mixed-types": {listingFieldTags: []any{"_inbox", 7, "receipts"}},
	} {
		presented, err := c.Format(stored)
		if err != nil {
			t.Fatalf("Format(%s): %v", name, err)
		}
		if got := presented[listingFieldTags]; !slices.Equal(got, []string{"_inbox", "receipts"}) {
			t.Errorf("Format(%s)[tags] = %v, want [_inbox receipts]", name, got)
		}
	}
	for name, stored := range map[string]map[string]any{
		"absent": {},
		"empty":  {listingFieldTags: []string{}},
	} {
		presented, err := c.Format(stored)
		if err != nil {
			t.Fatalf("Format(%s): %v", name, err)
		}
		if len(presented) != 0 {
			t.Errorf("Format(%s) = %v, want no keys", name, presented)
		}
	}
}

// TestTagsCodec_FormatDoesNotAliasStored pins that Format hands back a copy: a
// caller reordering the presented set must not corrupt the node's own field.
func TestTagsCodec_FormatDoesNotAliasStored(t *testing.T) {
	stored := map[string]any{listingFieldTags: []string{"b", "a"}}
	presented, err := tagsCodec{}.Format(stored)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(presented[listingFieldTags])
	if got := stored[listingFieldTags].([]string); !slices.Equal(got, []string{"b", "a"}) {
		t.Errorf("stored tags mutated through the presented slice: %v", got)
	}
}

func TestTagsCodec_Parse(t *testing.T) {
	c := tagsCodec{}
	updates, err := c.Parse(map[string][]string{listingFieldTags: {"_inbox", "payee-acme"}}, nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, ok := updates[listingFieldTags].([]string); !ok || !slices.Equal(got, []string{"_inbox", "payee-acme"}) {
		t.Errorf("Parse = %v, want the full set under tags", updates)
	}

	// An absent set is the empty membership: a non-nil empty slice so a JSON
	// encoding of the patch carries [] rather than null.
	updates, err = c.Parse(map[string][]string{}, nil)
	if err != nil {
		t.Fatalf("Parse(absent): %v", err)
	}
	if got, ok := updates[listingFieldTags].([]string); !ok || got == nil || len(got) != 0 {
		t.Errorf("Parse(absent) = %#v, want an empty non-nil tags slice", updates)
	}
}

// TestTagsCodec_FormatAgreesWithFacetValues pins the G6 agreement (mirroring
// caldav's categories pin): the `tags` facet VALUES the counting path
// (threadFacets) emits equal Format's presented set (from threadFields) as
// SETS — label tags joined through the tree (D1) plus the state tags (D2),
// for a thread whose members span a continuation label, the inbox, an unseen
// message, and a flagged one.
func TestTagsCodec_FormatAgreesWithFacetValues(t *testing.T) {
	tree := newMailboxTree([]Mailbox{
		{ID: "in", Name: "Inbox", Role: "inbox"},
		{ID: "area", Name: "area"},
		{ID: "career", Name: "-career", ParentID: "area"},
		{ID: "proj", Name: "proj-x", ParentID: "career"},
		{ID: "msft", Name: "-msft", ParentID: "proj"},
		{ID: "payee", Name: "payee"},
	})
	view := threadView{
		threadID: "T1", name: "Subject", receivedAt: "2026-09-21T10:00:00Z",
		members: []Email{
			{ID: "e1", MailboxIDs: map[string]bool{"msft": true, "in": true}, Keywords: map[string]bool{}},
			{ID: "e2", MailboxIDs: map[string]bool{"payee": true}, Keywords: map[string]bool{"$seen": true, "$flagged": true}},
		},
	}

	var fromFacets []string
	for _, v := range threadFacets(view, tree)[facetTags] {
		fromFacets = append(fromFacets, v.Key)
	}
	presented, err := tagsCodec{}.Format(threadFields(view, tree))
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	fromFormat := slices.Clone(presented[listingFieldTags])

	want := []string{stateTagFlagged, stateTagInbox, stateTagUnread, "payee", "proj-x-msft"}
	slices.Sort(fromFacets)
	slices.Sort(fromFormat)
	if !slices.Equal(fromFacets, want) {
		t.Errorf("facet tags = %v, want %v", fromFacets, want)
	}
	if !slices.Equal(fromFacets, fromFormat) {
		t.Errorf("counting path emits %v but Format presents %v; the two must agree as sets",
			fromFacets, fromFormat)
	}
}

// TestThreadFacets_DateIsISODay pins the FieldDate value shape (#230): the
// thread's newest receivedAt as a YYYY-MM-DD bucket key with a sortable
// YYYYMMDD order, and no bucket at all for a malformed value.
func TestThreadFacets_DateIsISODay(t *testing.T) {
	tree := newMailboxTree(nil)
	facets := threadFacets(threadView{receivedAt: "2026-07-14T09:12:03Z"}, tree)
	got := facets[facetDate]
	if len(got) != 1 || got[0].Key != "2026-07-14" || got[0].Order != 20260714 {
		t.Errorf("date facet = %+v, want [{2026-07-14 20260714}]", got)
	}
	if _, present := threadFacets(threadView{receivedAt: "garbage"}, tree)[facetDate]; present {
		t.Error("a malformed receivedAt must contribute no date bucket")
	}
}

// TestDescribeFacets_Derived pins the legacy facet surface derived from the
// unified declaration: thread-v1's dimensions in declaration order with the
// right kinds, the mailbox and email types with none, and the retired
// read/flagged/folder/year/tag dimensions absent.
func TestDescribeFacets_Derived(t *testing.T) {
	byTag := map[string][]cutting_garden_plugins.FacetDimension{}
	for _, ntf := range (Plugin{}).DescribeFacets() {
		byTag[ntf.Tag] = ntf.Dimensions
	}
	var keys []string
	kinds := map[string]cutting_garden_plugins.FacetKind{}
	for _, d := range byTag[typeThread] {
		keys = append(keys, d.Key)
		kinds[d.Key] = d.Kind
	}
	if want := []string{facetTags, facetFrom, facetDate, facetHasAttachment}; !slices.Equal(keys, want) {
		t.Errorf("thread dimensions = %v, want %v", keys, want)
	}
	if kinds[facetDate] != cutting_garden_plugins.FacetDate {
		t.Errorf("date kind = %q, want %q", kinds[facetDate], cutting_garden_plugins.FacetDate)
	}
	if kinds[facetFrom] != cutting_garden_plugins.FacetLabelled {
		t.Errorf("from kind = %q, want labelled", kinds[facetFrom])
	}
	tagsDim, ok := cutting_garden_plugins.FindFacetDimension((Plugin{}).DescribeFacets(), facetTags)
	if !ok || !tagsDim.Multi {
		t.Errorf("tags dimension = %+v ok=%v, want a multi-valued dimension", tagsDim, ok)
	}
	for _, tag := range []string{typeMailbox, typeEmail} {
		if len(byTag[tag]) != 0 {
			t.Errorf("%s declares facet dimensions %v, want none", tag, byTag[tag])
		}
	}
}

// TestDescribeFacetWrites_Derived pins the write mappings: tags is the single
// write:many dimension (its own stored field), every other thread dimension
// an explicit write:none.
func TestDescribeFacetWrites_Derived(t *testing.T) {
	var writes []cutting_garden_plugins.FacetWrite
	for _, ntw := range (Plugin{}).DescribeFacetWrites() {
		if ntw.Tag == typeThread {
			writes = ntw.Writes
		}
	}
	byDim := map[string]cutting_garden_plugins.FacetWrite{}
	for _, w := range writes {
		byDim[w.DimensionKey] = w
	}
	if w := byDim[facetTags]; w.Mode != cutting_garden_plugins.FacetWriteMany || w.Field != listingFieldTags {
		t.Errorf("tags write = %+v, want write:many onto the tags field", w)
	}
	for _, dim := range []string{facetFrom, facetDate, facetHasAttachment} {
		w, ok := byDim[dim]
		if !ok || w.Mode != cutting_garden_plugins.FacetWriteNone {
			t.Errorf("%s write = %+v ok=%v, want an explicit write:none", dim, w, ok)
		}
	}
}

// TestDescribeListingFields_ConsistentWithUnified pins the hand-written
// listing-field schema against the unified declaration (no SDK derivation
// helper exists yet): every listing field is a unified field of its type with
// the same label and trailer flag, and none is Writable — the plugin has no
// FieldWriteApplier, and tags write through the membership path.
func TestDescribeListingFields_ConsistentWithUnified(t *testing.T) {
	for _, ntf := range (Plugin{}).DescribeListingFields() {
		unified := fieldsOfType(t, ntf.Tag)
		for _, lf := range ntf.Fields {
			uf, ok := unified[lf.Key]
			if !ok {
				t.Errorf("%s listing field %q has no unified counterpart", ntf.Tag, lf.Key)
				continue
			}
			if lf.Label != uf.Label || lf.Trailer != uf.Trailer {
				t.Errorf("%s listing field %q = %+v drifts from unified %+v", ntf.Tag, lf.Key, lf, uf)
			}
			if lf.Writable {
				t.Errorf("%s listing field %q is Writable without a FieldWriteApplier", ntf.Tag, lf.Key)
			}
		}
	}
}

// TestPresentBoxAtoms_FromOnly pins the D6 box interior: a thread presents
// exactly one atom, from=<addr>; tags are not an atom and subject is the
// trailer.
func TestPresentBoxAtoms_FromOnly(t *testing.T) {
	node := cutting_garden_plugins.Node{
		Type: typeThread,
		Fields: map[string]any{
			listingFieldSubject: "Your July receipt",
			listingFieldFrom:    "billing@acme.example",
			listingFieldDate:    "2026-07-14T09:12:03Z",
			listingFieldTags:    []string{"_inbox", "receipts"},
		},
	}
	atoms := (Plugin{}).PresentBoxAtoms(node)
	if len(atoms) != 1 || atoms[0].Name != listingFieldFrom || atoms[0].Value != "billing@acme.example" {
		t.Errorf("atoms = %+v, want exactly [from=billing@acme.example]", atoms)
	}
	if got := cutting_garden_plugins.PresentUnifiedTags(codecsForType(typeThread), node); !slices.Equal(got, []string{"_inbox", "receipts"}) {
		t.Errorf("PresentUnifiedTags = %v, want [_inbox receipts]", got)
	}
}
