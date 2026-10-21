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

// Normalized applies RFC 0012 §5.1's consumer rules once, where a result
// enters the framework: a named gap makes the result incomplete, and a gap
// marked declined whose dimension is nonetheless in Summary is read as partial.
func TestFacetResult_Normalized(t *testing.T) {
	summary := FacetSummary{"payee": {"Grocer": 2}, "account": {"Checking": 4}}

	t.Run("a gap overrides a claimed Complete", func(t *testing.T) {
		got := FacetResult{
			Summary: summary, Complete: true,
			Incomplete: map[string]FacetGap{"payee": {Reason: "top 1 of 2"}},
		}.Normalized()
		if got.Complete {
			t.Error("Complete = true beside a named gap")
		}
		if gap := got.Incomplete["payee"]; gap.Declined || gap.Reason != "top 1 of 2" {
			t.Errorf("payee gap = %+v, want it unchanged", gap)
		}
	})

	t.Run("a declined gap whose dimension is present reads as partial", func(t *testing.T) {
		got := FacetResult{
			Summary:    summary,
			Incomplete: map[string]FacetGap{"payee": {Declined: true, Reason: "too expensive"}},
		}.Normalized()
		if gap := got.Incomplete["payee"]; gap.Declined {
			t.Errorf("payee gap = %+v: it is in Summary, so it cannot be declined", gap)
		}
	})

	t.Run("a declined gap whose dimension is absent stays declined", func(t *testing.T) {
		got := FacetResult{
			Summary:    summary,
			Incomplete: map[string]FacetGap{"label": {Declined: true}},
		}.Normalized()
		if !got.Incomplete["label"].Declined {
			t.Error("label gap lost its declined mark")
		}
	})

	t.Run("an empty gap map is nil and Complete is untouched", func(t *testing.T) {
		got := FacetResult{Summary: summary, Complete: true, Incomplete: map[string]FacetGap{}}.Normalized()
		if got.Incomplete != nil || !got.Complete {
			t.Errorf("got Complete %v Incomplete %#v, want true and nil", got.Complete, got.Incomplete)
		}
	})

	t.Run("it does not mutate its receiver", func(t *testing.T) {
		in := FacetResult{
			Summary: summary, Complete: true,
			Incomplete: map[string]FacetGap{"payee": {Declined: true}},
		}
		_ = in.Normalized()
		if !in.Complete || !in.Incomplete["payee"].Declined {
			t.Error("Normalized mutated the original result")
		}
	})
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
