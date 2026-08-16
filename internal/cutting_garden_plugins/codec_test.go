package cutting_garden_plugins

import (
	"reflect"
	"testing"
)

// IdentityCodec projects a present stored value to a single presentation value and
// inverts it back unchanged — the reversibility bar for the passthrough case.
func TestIdentityCodec_RoundTrip(t *testing.T) {
	c := IdentityCodec{Field: UnifiedField{Key: "status", Kind: FieldCategorical, Inline: true, Writable: true}}

	presented, err := c.Format(map[string]any{"status": "COMPLETED"})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	want := map[string][]string{"status": {"COMPLETED"}}
	if !reflect.DeepEqual(presented, want) {
		t.Fatalf("Format = %v, want %v", presented, want)
	}

	updates, err := c.Parse(presented, nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	wantUpdates := map[string]any{"status": "COMPLETED"}
	if !reflect.DeepEqual(updates, wantUpdates) {
		t.Fatalf("Parse(Format(stored)) = %v, want %v (not reversible)", updates, wantUpdates)
	}
}

// A stored key distinct from the presentation key round-trips onto the stored key,
// not the presentation key.
func TestIdentityCodec_DistinctStoredKey(t *testing.T) {
	c := IdentityCodec{
		Field:     UnifiedField{Key: "location", Inline: true, Writable: true},
		StoredKey: "loc",
	}
	presented, err := c.Format(map[string]any{"loc": "Bank"})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got := presented["location"]; len(got) != 1 || got[0] != "Bank" {
		t.Fatalf("Format presented[location] = %v, want [Bank]", got)
	}
	updates, err := c.Parse(map[string][]string{"location": {"Office"}}, nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := updates["loc"]; got != "Office" {
		t.Fatalf("Parse updates[loc] = %v, want Office", got)
	}
	if _, ok := updates["location"]; ok {
		t.Fatalf("Parse wrote the presentation key, want only the stored key")
	}
}

// An absent or empty stored value produces NO presentation entry (not an empty
// slice), so a nil/blank field never renders a `key=` atom.
func TestIdentityCodec_AbsentAndEmptyOmitted(t *testing.T) {
	c := IdentityCodec{Field: UnifiedField{Key: "location", Inline: true}}
	for name, stored := range map[string]map[string]any{
		"absent": {},
		"empty":  {"location": ""},
		"nil":    {"location": nil},
	} {
		presented, err := c.Format(stored)
		if err != nil {
			t.Fatalf("%s: Format: %v", name, err)
		}
		if len(presented) != 0 {
			t.Errorf("%s: Format = %v, want no entries", name, presented)
		}
	}
}

// A non-string stored value (an integer priority that became float64 across a JSON
// enrichment round-trip) renders as its canonical integer string, without a
// spurious ".0".
func TestIdentityCodec_NumericStoredValue(t *testing.T) {
	c := IdentityCodec{Field: UnifiedField{Key: "priority", Kind: FieldNumericBucket, Inline: true}}
	for name, stored := range map[string]map[string]any{
		"int":     {"priority": 1},
		"float64": {"priority": float64(1)},
	} {
		presented, err := c.Format(stored)
		if err != nil {
			t.Fatalf("%s: Format: %v", name, err)
		}
		if got := presented["priority"]; len(got) != 1 || got[0] != "1" {
			t.Errorf("%s: Format presented[priority] = %v, want [1]", name, got)
		}
	}
}

// An edit that omits the codec's field leaves the stored side unchanged (no update).
func TestIdentityCodec_ParseOmittedFieldNoUpdate(t *testing.T) {
	c := IdentityCodec{Field: UnifiedField{Key: "status", Writable: true}}
	updates, err := c.Parse(map[string][]string{"other": {"x"}}, map[string]any{"status": "COMPLETED"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(updates) != 0 {
		t.Errorf("Parse = %v, want no updates for an unedited field", updates)
	}
}

// Fields exposes exactly the one presentation field, so a derivation helper can
// enumerate the unified surface.
func TestIdentityCodec_Fields(t *testing.T) {
	f := UnifiedField{Key: "summary", Kind: FieldText, Trailer: true, Writable: true}
	c := IdentityCodec{Field: f}
	got := c.Fields()
	if len(got) != 1 || !reflect.DeepEqual(got[0], f) {
		t.Fatalf("Fields = %v, want [%v]", got, f)
	}
}

func TestValueToString(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"COMPLETED", "COMPLETED"},
		{true, "true"},
		{5, "5"},
		{int64(9), "9"},
		{float64(1), "1"},
		{1.5, "1.5"},
	}
	for _, tc := range cases {
		got, err := valueToString(tc.in)
		if err != nil {
			t.Errorf("valueToString(%v): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("valueToString(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}

	if _, err := valueToString([]string{"a"}); err == nil {
		t.Errorf("valueToString of an unsupported type: want error, got nil")
	}
}
