package cutting_garden_plugins

import (
	"context"
	"fmt"
	"net/url"
)

// The bounds on a framework fold (RFC 0012 §8): the most descendants one fold
// visits, and the most ListRoots calls it makes (each may be a network round
// trip, so a wide-but-shallow tree and a narrow-but-deep one are both held in
// check), before it stops and marks its result partial. They are guardrails
// against an unbounded lazy tree, not tuning knobs.
const (
	FacetFoldVisitLimit = 10000
	FacetFoldListLimit  = 200
)

// CanSummarizeFacets reports whether the framework can produce a facet
// summary for lister's nodes at all: either the plugin may answer one-shot
// (FacetCounter) or it declares a schema the framework fold can read
// (FacetDescriber). A plugin with neither has no facets to summarize.
func CanSummarizeFacets(lister RootLister) bool {
	if _, ok := lister.(FacetCounter); ok {
		return true
	}
	_, ok := lister.(FacetDescriber)
	return ok
}

// ResolveFacetCounts is the one place the framework computes a node's hoisted
// summary (RFC 0012 §4): the plugin's one-shot FacetCounts when it implements
// it and answers ok, otherwise the framework fold over ListRoots. ok is false
// only when neither path can summarize the node. An error aborts the read.
func ResolveFacetCounts(
	ctx context.Context,
	lister RootLister,
	node *url.URL,
	filter FacetFilter,
) (result FacetResult, ok bool, err error) {
	if counter, isCounter := lister.(FacetCounter); isCounter {
		result, ok, err = counter.FacetCounts(ctx, node, filter)
		if err != nil || ok {
			return result, ok, err
		}
	}
	return FoldFacets(ctx, lister, node, filter)
}

// FoldFacets is the framework fold (RFC 0012 §4.2): it hoists Node.Facets up
// from node's descendants through ListRoots. Each descendant that matches
// filter adds its lift (one to every bucket of every declared dimension it
// carries) to the summary; the walk descends through every container whether
// or not the container itself matches. node's own facets are not part of the
// result (the framework holds only its URI, not its Node). Dimension keys the
// plugin did not declare are ignored (RFC 0012 §2). The walk visits at most
// FacetFoldVisitLimit descendants and makes at most FacetFoldListLimit
// ListRoots calls; beyond either it stops and the result is marked
// Complete == false (§8). A ListRoots error aborts the fold (§9).
//
// ok is false when the lister declares no dimension at all (no FacetDescriber,
// or one with an empty schema): with nothing declared nothing can be counted,
// and no walk is made.
func FoldFacets(
	ctx context.Context,
	lister RootLister,
	node *url.URL,
	filter FacetFilter,
) (FacetResult, bool, error) {
	describer, ok := lister.(FacetDescriber)
	if !ok {
		return FacetResult{}, false, nil
	}

	declared := map[string]bool{}
	for _, key := range declaredDimensionKeys(describer.DescribeFacets()) {
		declared[key] = true
	}
	if len(declared) == 0 {
		return FacetResult{}, false, nil
	}

	fold := facetFold{
		lister:   lister,
		filter:   filter,
		declared: declared,
		summary:  FacetSummary{},
	}
	if err := fold.walk(ctx, node); err != nil {
		return FacetResult{}, false, err
	}
	return FacetResult{Summary: fold.summary, Complete: !fold.truncated}, true, nil
}

type facetFold struct {
	lister    RootLister
	filter    FacetFilter
	declared  map[string]bool
	summary   FacetSummary
	visited   int
	listed    int
	truncated bool
}

func (f *facetFold) walk(ctx context.Context, node *url.URL) error {
	if f.listed >= FacetFoldListLimit {
		f.truncated = true
		return nil
	}
	f.listed++
	children, err := f.lister.ListRoots(ctx, node)
	if err != nil {
		return fmt.Errorf("facet fold: list %s: %w", node, err)
	}
	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return err
		}
		if f.visited >= FacetFoldVisitLimit {
			f.truncated = true
			return nil
		}
		f.visited++
		if f.filter.Matches(child.Facets) {
			f.lift(child)
		}
		nodeType, known := NodeTypeFor(f.lister, child.Type)
		if !known || !nodeType.Container || child.URI == nil {
			continue
		}
		if err := f.walk(ctx, child.URI); err != nil {
			return err
		}
		if f.truncated {
			return nil
		}
	}
	return nil
}

// lift adds one node's declared facet membership to the summary (RFC 0012 §3):
// a multi-valued node adds one to each of its buckets.
func (f *facetFold) lift(child Node) {
	for dimension, values := range child.Facets {
		if !f.declared[dimension] {
			continue
		}
		histogram := f.summary[dimension]
		if histogram == nil {
			histogram = FacetHistogram{}
			f.summary[dimension] = histogram
		}
		for _, value := range values {
			histogram[value.Key]++
		}
	}
}
