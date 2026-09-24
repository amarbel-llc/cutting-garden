package organize

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// countingWriteLister is a fakeLister that also declares facet writes and
// answers FacetCounts with a fixed summary — the forge-shaped plugin whose
// container counts carry zero-count milestones (forge organize F3).
type countingWriteLister struct {
	fakeLister
	writes   []cgp.NodeTypeFacetWrites
	summary  cgp.FacetSummary
	countErr error
	counted  int
}

func (l *countingWriteLister) DescribeFacetWrites() []cgp.NodeTypeFacetWrites { return l.writes }

func (l *countingWriteLister) FacetCounts(
	context.Context, *url.URL, cgp.FacetFilter,
) (cgp.FacetResult, bool, error) {
	l.counted++
	if l.countErr != nil {
		return cgp.FacetResult{}, false, l.countErr
	}
	return cgp.FacetResult{Summary: l.summary, Complete: true}, true, nil
}

func milestoneLister(mode cgp.FacetWriteMode) *countingWriteLister {
	return &countingWriteLister{
		writes: []cgp.NodeTypeFacetWrites{{
			Tag: "ticket",
			Writes: []cgp.FacetWrite{{
				DimensionKey: "milestone", Mode: mode, Field: "milestone",
			}},
		}},
		summary: cgp.FacetSummary{
			"milestone": {"v0.1": 2, "v0.3": 0, "v0.0": 0},
			"state":     {"open": 2, "done": 0},
		},
	}
}

// Zero-count values join the sorted tail with the observed values — never
// ahead of the declared FacetWrite.Values, deduplicated against both — and
// render as empty buckets.
func TestGroupNodes_ZeroCountValuesJoinSortedTail(t *testing.T) {
	anchor := "fake://t/"
	nodes := []cgp.Node{{
		URI:    mustURL(t, "fake://t/1"),
		Type:   "ticket",
		Facets: map[string][]cgp.FacetValue{"milestone": {{Key: "v0.4"}}},
	}}

	_, buckets := groupNodes(
		nodes, groupSpec{Dim: "milestone", Kind: groupKindField}, boxIDsFor(nil, anchor),
		[]string{"v0.9"}, []string{"v0.3", "v0.9", "v0.2"}, false, nil,
	)

	var order []string
	for _, b := range buckets {
		order = append(order, b.Value)
		if b.Value != "v0.4" && len(b.Lines) != 0 {
			t.Errorf("bucket %s = %+v, want empty", b.Value, b.Lines)
		}
	}
	if want := []string{"v0.9", "v0.2", "v0.3", "v0.4"}; !slices.Equal(order, want) {
		t.Errorf("bucket order = %v, want %v", order, want)
	}
}

// zeroCountValues takes only the grouped dimension's count-0 entries, sorted,
// and only for a field grouping some type writes single-valued.
func TestZeroCountValues(t *testing.T) {
	ctx := context.Background()
	anchor := mustURL(t, "fake://t/")
	field := groupSpec{Dim: "milestone", Kind: groupKindField}

	one := milestoneLister(cgp.FacetWriteOne)
	if got := zeroCountValues(ctx, one, anchor, field); !slices.Equal(got, []string{"v0.0", "v0.3"}) {
		t.Errorf("single-valued writable: zeroCountValues = %v, want [v0.0 v0.3]", got)
	}

	many := milestoneLister(cgp.FacetWriteMany)
	if got := zeroCountValues(ctx, many, anchor, field); got != nil || many.counted != 0 {
		t.Errorf("many-valued: zeroCountValues = %v after %d counts, want nil and no fetch",
			got, many.counted)
	}

	readOnly := milestoneLister(cgp.FacetWriteOne)
	if got := zeroCountValues(ctx, readOnly, anchor, groupSpec{Dim: "state", Kind: groupKindField}); got != nil ||
		readOnly.counted != 0 {
		t.Errorf("undeclared write: zeroCountValues = %v after %d counts, want nil and no fetch",
			got, readOnly.counted)
	}

	tagged := milestoneLister(cgp.FacetWriteOne)
	if got := zeroCountValues(ctx, tagged, anchor, groupSpec{Dim: "milestone", Kind: groupKindTagWhole}); got != nil {
		t.Errorf("tag grouping: zeroCountValues = %v, want nil", got)
	}

	failing := milestoneLister(cgp.FacetWriteOne)
	failing.countErr = errors.New("backend down")
	if got := zeroCountValues(ctx, failing, anchor, field); got != nil {
		t.Errorf("counts error: zeroCountValues = %v, want nil (implicit surface)", got)
	}
}

// zeroCountTargets hands the container's zero values only to a type that
// writes the grouped dimension single-valued.
func TestZeroCountTargets(t *testing.T) {
	l := milestoneLister(cgp.FacetWriteOne)
	spec := groupSpec{Dim: "milestone", Kind: groupKindField}
	zeros := []string{"v0.3"}

	if got := zeroCountTargets(l, "ticket", spec, zeros); !slices.Equal(got, zeros) {
		t.Errorf("writing type: zeroCountTargets = %v, want %v", got, zeros)
	}
	if got := zeroCountTargets(l, "comment", spec, zeros); got != nil {
		t.Errorf("non-writing type: zeroCountTargets = %v, want nil", got)
	}
}
