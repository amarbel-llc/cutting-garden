package cutting_garden_plugins

import (
	"reflect"
	"testing"
)

// IsComplete is the consumer-side reading of RFC 0012 §5.1: complete means
// Complete AND no named gap, so a result that names a gap but forgot to clear
// Complete is still read as partial rather than trusted.
func TestFacetResult_IsComplete(t *testing.T) {
	gap := map[string]FacetGap{"payee": {Reason: "top 20 of 31"}}
	for name, tc := range map[string]struct {
		result FacetResult
		want   bool
	}{
		"complete, no gaps":           {FacetResult{Complete: true}, true},
		"incomplete, no gaps":         {FacetResult{Complete: false}, false},
		"incomplete with a named gap": {FacetResult{Complete: false, Incomplete: gap}, false},
		"complete claimed beside a gap (non-conformant)": {
			FacetResult{Complete: true, Incomplete: gap}, false,
		},
		"complete with an empty gap map": {FacetResult{Complete: true, Incomplete: map[string]FacetGap{}}, true},
	} {
		if got := tc.result.IsComplete(); got != tc.want {
			t.Errorf("%s: IsComplete() = %v, want %v", name, got, tc.want)
		}
	}
}

func TestFacetResult_IncompleteDimensionsAreSorted(t *testing.T) {
	result := FacetResult{Incomplete: map[string]FacetGap{
		"payee": {}, "account": {}, "label": {Declined: true},
	}}
	want := []string{"account", "label", "payee"}
	if got := result.IncompleteDimensions(); !reflect.DeepEqual(got, want) {
		t.Errorf("IncompleteDimensions() = %v, want %v", got, want)
	}
	if got := (FacetResult{}).IncompleteDimensions(); len(got) != 0 {
		t.Errorf("no gaps gave %v, want none", got)
	}
}
