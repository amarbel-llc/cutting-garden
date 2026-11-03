package cutting_garden_plugins

import (
	"strings"
	"testing"
)

// ListingField.Name marks the field holding a node's full hyphen name
// (cutting-garden#353): the `(tree)` grouping reads it, and a type has at most
// one.
func TestValidateListingFieldSets_AtMostOneNameField(t *testing.T) {
	ok := []NodeTypeListingFields{{Tag: "headline", Fields: []ListingField{
		{Key: "title", Trailer: true},
		{Key: "name", Name: true},
	}}}
	if err := ValidateListingFieldSets(ok); err != nil {
		t.Fatalf("one name field: %v", err)
	}
	if err := ValidateListingFieldSets(nil); err != nil {
		t.Fatalf("no sets: %v", err)
	}

	bad := []NodeTypeListingFields{{Tag: "headline", Fields: []ListingField{
		{Key: "name", Name: true},
		{Key: "path", Name: true},
	}}}
	err := ValidateListingFieldSets(bad)
	if err == nil {
		t.Fatal("two name fields must be a declaration error")
	}
	for _, frag := range []string{"headline", `"name"`, `"path"`} {
		if !strings.Contains(err.Error(), frag) {
			t.Errorf("error %q lacks %s", err, frag)
		}
	}
}

func TestListingNameKey(t *testing.T) {
	sets := []NodeTypeListingFields{
		{Tag: "file", Fields: []ListingField{{Key: "modified"}}},
		{Tag: "headline", Fields: []ListingField{{Key: "title"}, {Key: "name", Name: true}}},
	}
	if key, ok := ListingNameKey(sets, "headline"); !ok || key != "name" {
		t.Errorf("headline: got (%q, %v), want (name, true)", key, ok)
	}
	if key, ok := ListingNameKey(sets, "file"); ok {
		t.Errorf("file declares no name field, got %q", key)
	}
	if _, ok := ListingNameKey(sets, "unknown"); ok {
		t.Error("an undeclared type has no name field")
	}
}
