package cutting_garden_plugins

import (
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// A filter the schema refuses is the caller's mistake, so each refusal is a
// bad request (exit 64 on the command line, -32602 on the wire), not a
// plain error (cutting-garden#256).
func TestFacetFilterValidate_RefusalsAreBadRequests(t *testing.T) {
	dims := []NodeTypeFacets{{Tag: "t", Dimensions: []FacetDimension{
		{Key: "due", Kind: FacetDate},
		{Key: "status", Values: []FacetValue{{Key: "open"}}},
	}}}
	for _, raw := range []string{"nope=x", "due=aug", "status=closed"} {
		filter, err := ParseFacetFilter(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := filter.Validate(dims); err == nil || !errors.Is400BadRequest(err) {
			t.Errorf("Validate(%s) = %v, want a bad request", raw, err)
		}
	}
}
