package trellis_eval

import (
	"context"
	"net/url"
	"sort"
	"strings"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// A filter (`filter=` on read_facets, list_nodes and the mutate_nodes sweep;
// `list --facets --filter`) is one trellis step, split in two (RFC 0012 §6,
// cutting-garden#388): the PUSHDOWN, a FacetFilter of the step's positive
// single-value `=` terms on declared facet dimensions, each dimension once,
// which goes to the plugin exactly as a filter always has; and the RESIDUAL,
// every other term, which the host matches over the nodes the pushdown
// returned. No term is dropped.

// Residual is the host-side remainder of a split filter. The zero value has
// no terms and matches every node.
type Residual struct {
	step trellis.Step
	src  []rune
}

// Empty reports whether the residual has no terms: the filter was all
// pushdown.
func (r Residual) Empty() bool { return len(r.step.Terms) == 0 }

// String is the residual's terms as the caller wrote them, space-joined; ""
// when empty.
func (r Residual) String() string {
	parts := make([]string, len(r.step.Terms))
	for i, t := range r.step.Terms {
		parts[i] = t.Span.Text(r.src)
	}
	return strings.Join(parts, " ")
}

// ValidateFields refuses a residual field predicate naming a field the plugin
// declares nowhere — neither a facet dimension nor a listing field of any
// type, nor a reserved `_body`/`_words` — as the pushdown refuses an
// undeclared dimension (cutting-garden#161): a typo is a usage error naming
// the valid fields, never a filter that silently matches nothing. A plugin
// that declares neither schema has nothing to check against, and passes.
func (r Residual) ValidateFields(lister cgp.RootLister) error {
	known := map[string]bool{bodyField: true, wordsField: true}
	declared := false
	if fd, ok := lister.(cgp.FacetDescriber); ok {
		for _, t := range fd.DescribeFacets() {
			for _, d := range t.Dimensions {
				known[d.Key], declared = true, true
			}
		}
	}
	if ld, ok := lister.(cgp.ListingFieldsDescriber); ok {
		for _, t := range ld.DescribeListingFields() {
			for _, f := range t.Fields {
				known[f.Key], declared = true, true
			}
		}
	}
	if !declared {
		return nil
	}
	var unknown string
	walkFieldNames(r.step.Terms, func(name string) {
		if unknown == "" && !known[name] {
			unknown = name
		}
	})
	if unknown == "" {
		return nil
	}
	names := make([]string, 0, len(known))
	for name := range known {
		names = append(names, name)
	}
	sort.Strings(names)
	return errors.BadRequestf(
		"filter field %q is not declared; valid fields: %s (see describe_node_types)",
		unknown, strings.Join(names, ", "))
}

// walkFieldNames visits the field name of every field predicate in terms,
// OR-alternatives included.
func walkFieldNames(terms []trellis.Term, visit func(string)) {
	for _, t := range terms {
		switch b := t.Basic.(type) {
		case trellis.FieldPredBasicTerm:
			visit(b.FieldPred.Field.Name)
		case trellis.GroupBasicTerm:
			if alts, ok := b.Group.Body.(trellis.Alternatives); ok {
				for _, run := range alts.Alts {
					walkFieldNames(run.Terms, visit)
				}
			}
		}
	}
}

// Filter keeps the nodes the residual matches, through the same matcher a
// trellis query uses (facet predicates, no-value alias implications, leaf
// fields), so a split filter's two halves agree with a query. anchor scopes
// any search acceleration; opts are Evaluate's options.
func (r Residual) Filter(
	ctx context.Context, lister cgp.RootLister, anchor *url.URL, nodes []cgp.Node, opts ...EvaluateOption,
) ([]cgp.Node, error) {
	if r.Empty() {
		return nodes, nil
	}
	return newEvaluator(lister, anchor, opts...).filter(ctx, nodes, r.step)
}

// Matcher returns a per-node match of the residual sharing one evaluator, so
// a fold or a sweep probes the plugin's schema once.
func (r Residual) Matcher(
	lister cgp.RootLister, anchor *url.URL, opts ...EvaluateOption,
) func(context.Context, cgp.Node) (bool, error) {
	if r.Empty() {
		return func(context.Context, cgp.Node) (bool, error) { return true, nil }
	}
	ev := newEvaluator(lister, anchor, opts...)
	return func(ctx context.Context, n cgp.Node) (bool, error) { return ev.matchStep(ctx, n, r.step) }
}

// FacetCounts is the summary of a split filter at u (RFC 0012 §6): with no
// residual, the plugin's one-shot count or the framework fold, as always;
// with one, a fold over each container's listing under the pushdown (the
// plugin's enriched listing when it serves one, else ListRoots), lifting the
// nodes both halves match. It is as complete as those listings: one a plugin
// reported partial, or a level of facetless leaves the filter cannot be
// matched against, marks the summary partial.
func FacetCounts(
	ctx context.Context,
	lister cgp.RootLister,
	u *url.URL,
	pushdown cgp.FacetFilter,
	residual Residual,
	opts ...EvaluateOption,
) (cgp.FacetResult, bool, error) {
	if residual.Empty() {
		return cgp.ResolveFacetCounts(ctx, lister, u, pushdown)
	}
	aliases := cgp.NoValueAliasesFor(lister)
	enriched, _ := lister.(cgp.EnrichedLister)
	match := residual.Matcher(lister, u, opts...)
	return cgp.FoldFacetsMatching(ctx, lister, u,
		func(ctx context.Context, at *url.URL) ([]cgp.Node, bool, error) {
			lctx, partial := cgp.WithPartialListings(ctx)
			nodes, err := listUnder(lctx, lister, enriched, at, pushdown)
			if err != nil {
				return nil, false, err
			}
			return nodes, len(partial.All()) == 0 && !facetlessLeaves(lister, nodes), nil
		},
		func(ctx context.Context, n cgp.Node) (bool, error) {
			if !pushdown.MatchesNode(aliases, n.Type, n.Facets) {
				return false, nil
			}
			return match(ctx, n)
		})
}

// listUnder lists u's children through the plugin's pushdown-filtered
// enriched listing when it serves one at u, else ListRoots.
func listUnder(
	ctx context.Context, lister cgp.RootLister, enriched cgp.EnrichedLister, u *url.URL, pushdown cgp.FacetFilter,
) ([]cgp.Node, error) {
	if enriched != nil {
		nodes, handled, err := enriched.ListEnriched(ctx, u, pushdown)
		if err != nil || handled {
			return nodes, err
		}
	}
	return lister.ListRoots(ctx, u)
}

// facetlessLeaves reports a listing holding leaves none of which carries a
// facet: a filter cannot be matched against them.
func facetlessLeaves(lister cgp.RootLister, nodes []cgp.Node) bool {
	leaves := false
	for _, n := range nodes {
		if nt, known := cgp.NodeTypeFor(lister, n.Type); known && nt.Container {
			continue
		}
		if len(n.Facets) > 0 {
			return false
		}
		leaves = true
	}
	return leaves
}

// SplitFilter parses raw as one trellis step and splits it against schema,
// the plugin's declared facet types (nil when it declares none: then every
// positive single-value `=` term is pushed down unchecked, as a filter always
// was). legacy reports the deprecated comma-joined `dim=value,dim=value` form,
// which parses as the same AND of equalities with no residual; it is told
// apart by a top-level comma, which no one-step filter has outside a `[…]`
// list or a quoted string. A filter that is not one step (a combinator, a
// leading `->`) is a usage error, never an unfiltered result.
func SplitFilter(raw string, schema []cgp.NodeTypeFacets) (
	pushdown cgp.FacetFilter, residual Residual, legacy bool, err error,
) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, Residual{}, false, nil
	}
	if hasTopLevelComma(raw) {
		pushdown, err = cgp.ParseFacetFilter(raw)
		if err != nil {
			return nil, Residual{}, true, errors.BadRequestf("filter %q: %s", raw, err)
		}
		return pushdown, Residual{}, true, nil
	}
	q, perr := trellis.Parse(raw)
	if perr != nil {
		return nil, Residual{}, false, errors.BadRequestf("filter %q: %s", raw, perr)
	}
	if q.Path.Leading != nil || len(q.Path.Steps) != 1 {
		return nil, Residual{}, false, errors.BadRequestf(
			"filter %q: a filter is one trellis step (terms joined by spaces); "+
				"a path with `->`, `<-` or a closure belongs in list_nodes' query", raw)
	}
	step := q.Path.Steps[0]
	if err := validateStep(step); err != nil {
		return nil, Residual{}, false, err
	}
	residual.src = []rune(raw)
	lowered := map[string]bool{}
	for _, term := range step.Terms {
		if pred, ok := lowerable(term, schema); ok && !lowered[pred.Dimension] {
			lowered[pred.Dimension] = true
			pushdown = append(pushdown, pred)
			continue
		}
		residual.step.Terms = append(residual.step.Terms, term)
	}
	return pushdown, residual, false, nil
}

// lowerable reports the FacetPredicate a term lowers to: a positive,
// non-exact, single-value `=` on a declared facet dimension (any field when
// schema is nil) with a plain string value.
func lowerable(term trellis.Term, schema []cgp.NodeTypeFacets) (cgp.FacetPredicate, bool) {
	if term.Negate || term.Exact {
		return cgp.FacetPredicate{}, false
	}
	fp, ok := term.Basic.(trellis.FieldPredBasicTerm)
	if !ok || fp.FieldPred.Op != trellis.FieldOpEq || fp.FieldPred.List || len(fp.FieldPred.Values) != 1 {
		return cgp.FacetPredicate{}, false
	}
	switch fp.FieldPred.Values[0].(type) {
	case trellis.StringValue, trellis.Bareword:
	default:
		return cgp.FacetPredicate{}, false
	}
	field := fp.FieldPred.Field.Name
	if schema != nil {
		if _, declared := cgp.FindFacetDimension(schema, field); !declared {
			return cgp.FacetPredicate{}, false
		}
	}
	return cgp.FacetPredicate{Dimension: field, Value: valueString(fp.FieldPred.Values[0])}, true
}

// hasTopLevelComma reports a comma outside any `[…]` and any quoted string.
func hasTopLevelComma(s string) bool {
	depth, quoted := 0, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case quoted && c == '\\':
			i++
		case c == '"':
			quoted = !quoted
		case quoted:
		case c == '[':
			depth++
		case c == ']':
			depth--
		case c == ',' && depth == 0:
			return true
		}
	}
	return false
}
