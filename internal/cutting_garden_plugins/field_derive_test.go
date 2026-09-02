package cutting_garden_plugins

import (
	"reflect"
	"strings"
	"testing"
)

// splitTestCodec is a minimal 1<->2 split codec exercising the multi-field
// derivation path: one stored field "d" (spelled "DATExTIME") <-> presentation
// atoms d_date + d_time, both attributing (Source) to the parent "d".
type splitTestCodec struct{}

func (splitTestCodec) Fields() []UnifiedField {
	return []UnifiedField{
		{Key: "d_date", Inline: true, Writable: true, Source: "d"},
		{Key: "d_time", Inline: true, Writable: true, Source: "d"},
	}
}

func (splitTestCodec) Format(stored map[string]any) (map[string][]string, error) {
	raw, _ := stored["d"].(string)
	if raw == "" {
		return map[string][]string{}, nil
	}
	date, clock, found := strings.Cut(raw, "x")
	p := map[string][]string{"d_date": {date}}
	if found && clock != "" {
		p["d_time"] = []string{clock}
	}
	return p, nil
}

func (splitTestCodec) Parse(edited map[string][]string, current map[string]any) (map[string]any, error) {
	raw, _ := current["d"].(string)
	date, clock, _ := strings.Cut(raw, "x")
	if v, ok := edited["d_date"]; ok && len(v) > 0 {
		date = v[0]
	}
	if v, ok := edited["d_time"]; ok && len(v) > 0 {
		clock = v[0]
	}
	return map[string]any{"d": date + "x" + clock}, nil
}

// PresentUnifiedAtoms emits an atom per INLINE presentation field in codec-then-
// field order, carrying each field's Source as BoxAtom.Field, and skips Trailer
// fields (the description, not an atom).
func TestPresentUnifiedAtoms(t *testing.T) {
	codecs := []Codec{
		IdentityCodec{Field: UnifiedField{Key: "location", Inline: true, Writable: true}},
		splitTestCodec{},
		IdentityCodec{Field: UnifiedField{Key: "summary", Trailer: true, Writable: true}},
	}
	node := Node{Fields: map[string]any{"location": "Bank", "d": "2026x14", "summary": "hi"}}

	got := PresentUnifiedAtoms(codecs, node)
	want := []BoxAtom{
		{Name: "location", Value: "Bank", Field: ""},
		{Name: "d_date", Value: "2026", Field: "d"},
		{Name: "d_time", Value: "14", Field: "d"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PresentUnifiedAtoms = %#v, want %#v", got, want)
	}
}

// An absent stored value yields no atom; a date-only value (no clock) yields only
// the date atom — the resilient projection the legacy presenter performs.
func TestPresentUnifiedAtoms_OmitsAbsentAndPartial(t *testing.T) {
	codecs := []Codec{
		IdentityCodec{Field: UnifiedField{Key: "location", Inline: true}},
		splitTestCodec{},
	}
	node := Node{Fields: map[string]any{"d": "2026"}} // no location, date-only d
	got := PresentUnifiedAtoms(codecs, node)
	want := []BoxAtom{{Name: "d_date", Value: "2026", Field: "d"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PresentUnifiedAtoms = %#v, want %#v", got, want)
	}
}

// A plain-atom edit routes to its IdentityCodec and writes the stored key.
func TestParseUnifiedFieldEdits_PlainField(t *testing.T) {
	codecs := []Codec{IdentityCodec{Field: UnifiedField{Key: "location", Inline: true, Writable: true}}}
	updates, err := ParseUnifiedFieldEdits(codecs, []FieldEdit{{Name: "location", Value: "Office"}}, nil)
	if err != nil {
		t.Fatalf("ParseUnifiedFieldEdits: %v", err)
	}
	if !reflect.DeepEqual(updates, map[string]any{"location": "Office"}) {
		t.Fatalf("updates = %v, want {location: Office}", updates)
	}
}

// Split atoms recombine through their shared codec: editing one half preserves the
// other via current; editing both writes both.
func TestParseUnifiedFieldEdits_SplitRecombine(t *testing.T) {
	codecs := []Codec{splitTestCodec{}}
	current := map[string]any{"d": "2026x14"}

	// Edit only the date half: the clock is preserved from current.
	updates, err := ParseUnifiedFieldEdits(codecs, []FieldEdit{{Name: "d_date", Value: "2027"}}, current)
	if err != nil {
		t.Fatalf("Parse date-only: %v", err)
	}
	if got := updates["d"]; got != "2027x14" {
		t.Fatalf("date-only edit = %v, want 2027x14 (clock preserved)", got)
	}

	// Edit both halves.
	updates, err = ParseUnifiedFieldEdits(codecs,
		[]FieldEdit{{Name: "d_date", Value: "2027"}, {Name: "d_time", Value: "09"}}, current)
	if err != nil {
		t.Fatalf("Parse both: %v", err)
	}
	if got := updates["d"]; got != "2027x09" {
		t.Fatalf("both-halves edit = %v, want 2027x09", got)
	}
}

// An edit to a field no codec produces is a bad request (reject-unknown), not a
// silent drop.
func TestParseUnifiedFieldEdits_UnknownFieldRejected(t *testing.T) {
	codecs := []Codec{IdentityCodec{Field: UnifiedField{Key: "location", Writable: true}}}
	_, err := ParseUnifiedFieldEdits(codecs, []FieldEdit{{Name: "nope", Value: "x"}}, nil)
	if err == nil {
		t.Fatalf("ParseUnifiedFieldEdits of an unknown field: want error, got nil")
	}
}

// PresentUnifiedTags returns the designated FieldTag field's values (produced
// by tagTestCodec, facet_derive_test.go) in STORED order, skipping the non-tag
// codecs around it.
func TestPresentUnifiedTags_PicksDesignatedField(t *testing.T) {
	codecs := []Codec{
		IdentityCodec{Field: UnifiedField{Key: "location", Inline: true}},
		tagTestCodec{},
		splitTestCodec{},
	}
	node := Node{Fields: map[string]any{
		"location": "Bank",
		"cats":     []string{"work", "errand", "planning, misc"},
	}}
	got := PresentUnifiedTags(codecs, node)
	want := []string{"work", "errand", "planning, misc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PresentUnifiedTags = %v, want %v (stored order)", got, want)
	}
}

// A type declaring no FieldTag field has no tag notion: nil, not a panic or an
// error. So is an untagged node (the codec omits the key).
func TestPresentUnifiedTags_EmptyWhenNoFieldTag(t *testing.T) {
	noTagCodecs := []Codec{IdentityCodec{Field: UnifiedField{Key: "location", Inline: true}}}
	if got := PresentUnifiedTags(noTagCodecs, Node{Fields: map[string]any{"location": "x"}}); got != nil {
		t.Fatalf("PresentUnifiedTags with no FieldTag = %v, want nil", got)
	}

	tagCodecs := []Codec{tagTestCodec{}}
	if got := PresentUnifiedTags(tagCodecs, Node{Fields: map[string]any{}}); got != nil {
		t.Fatalf("PresentUnifiedTags of an untagged node = %v, want nil", got)
	}
}

// ValidateUnifiedFieldSets accepts one FieldTag per type (and none) and rejects
// a second, naming the type and both keys.
func TestValidateUnifiedFieldSets_OneFieldTagPerType(t *testing.T) {
	valid := []NodeTypeUnifiedFields{
		{Tag: "a", Codecs: []Codec{tagTestCodec{}, splitTestCodec{}}},
		{Tag: "b", Codecs: []Codec{IdentityCodec{Field: UnifiedField{Key: "location"}}}},
	}
	if err := ValidateUnifiedFieldSets(valid); err != nil {
		t.Fatalf("ValidateUnifiedFieldSets(valid) = %v, want nil", err)
	}

	doubled := []NodeTypeUnifiedFields{{
		Tag:    "a",
		Codecs: []Codec{tagTestCodec{}, tagTestCodec{key: "labels"}},
	}}
	err := ValidateUnifiedFieldSets(doubled)
	if err == nil {
		t.Fatal("ValidateUnifiedFieldSets(two FieldTag fields): want error, got nil")
	}
	for _, want := range []string{`"a"`, `"tags"`, `"labels"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

// An edit to a field declared read-only (Writable false) is a bad request even
// without the framework's own writability gate in front — the declaration is the
// single source of writability, and a direct SDK caller must not slip past it.
func TestParseUnifiedFieldEdits_ReadOnlyFieldRejected(t *testing.T) {
	codecs := []Codec{IdentityCodec{Field: UnifiedField{Key: "etag", Inline: true}}}
	_, err := ParseUnifiedFieldEdits(codecs, []FieldEdit{{Name: "etag", Value: "x"}}, nil)
	if err == nil {
		t.Fatalf("ParseUnifiedFieldEdits of a read-only field: want error, got nil")
	}
}
