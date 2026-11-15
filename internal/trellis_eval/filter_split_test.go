package trellis_eval

import (
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// cutting-garden#388 (RFC 0012 §6): SplitFilter parses a `filter=` string as
// one trellis step (or the legacy `dim=value,…` form) and splits it into the
// FacetFilter that is pushed down (the conjunction of positive single-value
// `=` terms on declared facet dimensions, each dimension once) and a residual
// step evaluated host-side; nothing is dropped.

var splitSchema = []cgp.NodeTypeFacets{{
	Tag: "todo-v1",
	Dimensions: []cgp.FacetDimension{
		{Key: "status"}, {Key: "component"}, {Key: "date_due", Kind: cgp.FacetDate},
	},
}}

func TestSplitFilter(t *testing.T) {
	for _, c := range []struct {
		raw          string
		pushdown     []string // "dim=value", in order
		residualText string   // the residual step as written, "" for none
		legacy       bool
	}{
		{raw: "status=completed", pushdown: []string{"status=completed"}},
		{raw: "status=completed component=VTODO", pushdown: []string{"status=completed", "component=VTODO"}},
		{raw: "status=[needs-action, completed]", residualText: "status=[needs-action, completed]"},
		{raw: "^status=completed", residualText: "^status=completed"},
		{raw: "status!=completed", residualText: "status!=completed"},
		{raw: `^status~="."`, residualText: `^status~="."`},
		{raw: "component=VTODO ^status=completed", pushdown: []string{"component=VTODO"}, residualText: "^status=completed"},
		{raw: "summary~=milk", residualText: "summary~=milk"}, // not a facet dimension: host-side
		{raw: "status=completed,component=VTODO", pushdown: []string{"status=completed", "component=VTODO"}, legacy: true},
		{raw: "status=[a, b] component=VTODO", pushdown: []string{"component=VTODO"}, residualText: "status=[a, b]"},
		{raw: "status=a status=b", pushdown: []string{"status=a"}, residualText: "status=b"}, // each dimension once
		{raw: `summary="a, b"`, residualText: `summary="a, b"`},                              // a quoted comma is not legacy
		{raw: "", pushdown: nil},
	} {
		t.Run(c.raw, func(t *testing.T) {
			pushdown, residual, legacy, err := SplitFilter(c.raw, splitSchema)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, p := range pushdown {
				got = append(got, p.Dimension+"="+p.Value)
			}
			if !equalStrings(got, c.pushdown) {
				t.Errorf("pushdown = %v, want %v", got, c.pushdown)
			}
			if gotText := residual.String(); gotText != c.residualText {
				t.Errorf("residual = %q, want %q", gotText, c.residualText)
			}
			if legacy != c.legacy {
				t.Errorf("legacy = %v, want %v", legacy, c.legacy)
			}
		})
	}
}

// A filter that is not one step is a usage error naming the problem, never an
// unfiltered result.
func TestSplitFilter_RejectsAPath(t *testing.T) {
	if _, _, _, err := SplitFilter("status=completed -> !todo-v1", splitSchema); err == nil {
		t.Fatal("a multi-step filter parsed; want a usage error")
	}
}
