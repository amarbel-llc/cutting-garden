// Package trellis_eval evaluates a parsed trellis Query (internal/trellis)
// against a plugin's traversal tree (internal/cutting_garden_plugins),
// returning the Nodes matched by the query's last step (RFC 0014, FDR 0022).
//
// This is the slice-1 evaluator (cutting-garden#164): a forward-containment
// walk from an explicit anchor, a predicate layer over Node.Type,
// Node.Facets, and leaf bodies, and `:`-only sigils. Forms the grammar
// admits but this slice does not yet implement — reverse/closure/typed
// combinators, non-`:` sigils, the `~=` operator, version subpaths,
// OR-alternatives, identity terms, bare-tag terms, the default-anchor
// (leading-combinator) origin — are rejected up front by Validate with a
// clear "not supported in slice-1" error, never silently mismatched. See
// docs/features/0022-trellis.md for the boundary taxonomy.
//
// It lives beside the parser (internal/trellis) rather than inside it so the
// pure-parser package stays free of the traversal-SDK dependency the
// evaluator needs.
package trellis_eval

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Evaluate resolves q against anchor's subtree via lister, returning the
// Nodes matched by q's last step. The query's first step filters anchor's
// immediate children (the set `list <anchor>` prints); each subsequent `->`
// descends one level via ListRoots over the prior step's matches. The result
// is deduplicated by URI.
//
// q must be a slice-1 query (see Validate); a query using a deferred form is
// rejected before any traversal happens. A predicate that needs a leaf fetch
// against a plugin that is not a LeafReader simply does not match (graceful
// degradation), rather than erroring.
func Evaluate(
	ctx context.Context,
	q *trellis.Query,
	anchor *url.URL,
	lister cgp.RootLister,
) ([]cgp.Node, error) {
	if err := Validate(q); err != nil {
		return nil, err
	}

	ev := newEvaluator(lister)
	steps := q.Path.Steps

	current, err := lister.ListRoots(ctx, anchor)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if current, err = ev.filter(ctx, current, steps[0]); err != nil {
		return nil, err
	}

	for i := 1; i < len(steps); i++ {
		descended, err := ev.descend(ctx, current)
		if err != nil {
			return nil, err
		}
		if current, err = ev.filter(ctx, descended, steps[i]); err != nil {
			return nil, err
		}
	}

	return current, nil
}

// evaluator carries the once-probed optional capabilities of the lister so
// per-node matching does not repeat the type assertions, plus the facet
// schema (tag -> declared dimension keys) used to route a field predicate to
// the cheap Node.Facets path versus a leaf fetch.
type evaluator struct {
	lister cgp.RootLister
	leaf   cgp.LeafReader                 // nil when the plugin cannot fetch leaves
	facets map[string]map[string]struct{} // tag -> set of declared dimension keys
}

func newEvaluator(lister cgp.RootLister) *evaluator {
	ev := &evaluator{lister: lister}
	if lr, ok := lister.(cgp.LeafReader); ok {
		ev.leaf = lr
	}
	if fd, ok := lister.(cgp.FacetDescriber); ok {
		ev.facets = make(map[string]map[string]struct{})
		for _, ntf := range fd.DescribeFacets() {
			keys := make(map[string]struct{}, len(ntf.Dimensions))
			for _, dim := range ntf.Dimensions {
				keys[dim.Key] = struct{}{}
			}
			ev.facets[ntf.Tag] = keys
		}
	}
	return ev
}

// descend returns the union of the immediate children of every node in nodes,
// deduplicated by URI so a child reachable from two parents appears once.
func (ev *evaluator) descend(
	ctx context.Context, nodes []cgp.Node,
) ([]cgp.Node, error) {
	var out []cgp.Node
	seen := make(map[string]struct{})
	for _, n := range nodes {
		if n.URI == nil {
			continue
		}
		children, err := ev.lister.ListRoots(ctx, n.URI)
		if err != nil {
			return nil, errors.Wrap(err)
		}
		for _, c := range children {
			key := c.URIString()
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, c)
		}
	}
	return out, nil
}

// filter keeps the nodes for which every term of step matches (space-ANDed).
func (ev *evaluator) filter(
	ctx context.Context, nodes []cgp.Node, step trellis.Step,
) ([]cgp.Node, error) {
	var out []cgp.Node
	for _, n := range nodes {
		ok, err := ev.matchStep(ctx, n, step)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, n)
		}
	}
	return out, nil
}

func (ev *evaluator) matchStep(
	ctx context.Context, n cgp.Node, step trellis.Step,
) (bool, error) {
	for _, term := range step.Terms {
		ok, err := ev.matchTerm(ctx, n, term)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func (ev *evaluator) matchTerm(
	ctx context.Context, n cgp.Node, term trellis.Term,
) (bool, error) {
	ok, err := ev.matchBasic(ctx, n, term.Basic)
	if err != nil {
		return false, err
	}
	if term.Negate {
		return !ok, nil
	}
	return ok, nil
}

func (ev *evaluator) matchBasic(
	ctx context.Context, n cgp.Node, basic trellis.BasicTerm,
) (bool, error) {
	switch b := basic.(type) {
	case trellis.TypeBasicTerm:
		// TypeTerm.Name carries the identifier without the leading '!'
		// (the parser consumes it), matching Node.Type's bare tag form.
		return n.Type == b.Type.Name, nil
	case trellis.FieldPredBasicTerm:
		return ev.matchFieldPred(ctx, n, b.FieldPred)
	case trellis.GroupBasicTerm:
		// Validate has guaranteed the group is an existential forward
		// subpath (SubPath, CombinatorFwd, single-step body).
		sub := b.Group.Body.(trellis.SubPath)
		return ev.matchSubPath(ctx, n, sub)
	case trellis.SigilBasicTerm:
		// A bare `:` selects the latest version-set (the slice-1 default)
		// and adds no predicate — a no-op that matches every candidate.
		return true, nil
	default:
		// Unreachable: Validate rejects every other BasicTerm form.
		return false, errors.BadRequestf(
			"trellis: internal: unvalidated basic term %T reached the evaluator", basic,
		)
	}
}

// matchSubPath is the existential spatial predicate `[-> step]`: the node
// matches if any of its immediate children matches the subpath's single
// step. The subject node is unchanged.
func (ev *evaluator) matchSubPath(
	ctx context.Context, n cgp.Node, sub trellis.SubPath,
) (bool, error) {
	if n.URI == nil {
		return false, nil
	}
	// Empty form `[->]`: "has any child."
	children, err := ev.lister.ListRoots(ctx, n.URI)
	if err != nil {
		return false, errors.Wrap(err)
	}
	if sub.Path == nil {
		return len(children) > 0, nil
	}
	step := sub.Path.Steps[0]
	for _, c := range children {
		ok, err := ev.matchStep(ctx, c, step)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// matchFieldPred resolves the field and applies the operator. Resolution:
// `_body` reads the leaf's raw bytes; a field declared as a facet dimension
// for this node's type reads the cheap Node.Facets membership; anything else
// fetches the leaf and indexes its structured projection by flat field name.
// Value lists distribute the operator as OR (RFC 0014).
func (ev *evaluator) matchFieldPred(
	ctx context.Context, n cgp.Node, fp trellis.FieldPred,
) (bool, error) {
	field := fp.Field.Name

	switch {
	case field == bodyField:
		return ev.matchBody(ctx, n, fp)
	case ev.isFacetField(n.Type, field):
		return matchFacet(n.Facets[field], fp), nil
	default:
		return ev.matchLeafField(ctx, n, field, fp)
	}
}

const bodyField = "_body"

func (ev *evaluator) isFacetField(tag, field string) bool {
	dims, ok := ev.facets[tag]
	if !ok {
		return false
	}
	_, ok = dims[field]
	return ok
}

// matchFacet matches fp against a node's membership in one facet dimension.
// The node matches iff, for some query value, some facet-value bucket key
// satisfies the operator (existential over both). A dimension the node does
// not contribute to has no bucket keys and so matches nothing.
func matchFacet(values []cgp.FacetValue, fp trellis.FieldPred) bool {
	for _, qv := range fp.Values {
		want := valueString(qv)
		for _, fv := range values {
			if compare(fp.Op, fv.Key, want) {
				return true
			}
		}
	}
	return false
}

// matchLeafField fetches the node's leaf body and matches fp against the
// named field of its structured projection (JSON-marshalled, indexed by flat
// field name — no path walking). A plugin that cannot fetch the leaf, a
// non-leaf node, a nil structured view, or an absent field all yield no
// match rather than an error.
func (ev *evaluator) matchLeafField(
	ctx context.Context, n cgp.Node, field string, fp trellis.FieldPred,
) (bool, error) {
	if ev.leaf == nil || n.URI == nil {
		return false, nil
	}
	content, ok, err := ev.leaf.ReadLeaf(ctx, n.URI)
	if err != nil {
		return false, errors.Wrap(err)
	}
	if !ok || content.Structured == nil {
		return false, nil
	}

	fields, err := structuredFields(content.Structured)
	if err != nil {
		return false, err
	}
	raw, present := fields[field]
	if !present {
		return false, nil
	}
	got := scalarString(raw)

	for _, qv := range fp.Values {
		if compare(fp.Op, got, valueString(qv)) {
			return true, nil
		}
	}
	return false, nil
}

// matchBody matches fp against the leaf's raw bytes, treated as text. Binary
// bodies (a non-text RawMimeType) never match. Only substring-shaped and
// equality operators are meaningful here; ordering operators compare the
// whole body lexicographically.
func (ev *evaluator) matchBody(
	ctx context.Context, n cgp.Node, fp trellis.FieldPred,
) (bool, error) {
	if ev.leaf == nil || n.URI == nil {
		return false, nil
	}
	content, ok, err := ev.leaf.ReadLeaf(ctx, n.URI)
	if err != nil {
		return false, errors.Wrap(err)
	}
	if !ok || content.Raw == nil || !isTextMime(content.RawMimeType) {
		return false, nil
	}
	body := string(content.Raw)
	for _, qv := range fp.Values {
		if compare(fp.Op, body, valueString(qv)) {
			return true, nil
		}
	}
	return false, nil
}

// isTextMime reports whether raw bytes of this IANA type are matchable as
// text. An empty type is treated as text (the plugin offered bytes with no
// declared type); anything under text/* is text; a handful of structured
// text types are admitted explicitly.
func isTextMime(mime string) bool {
	if mime == "" {
		return true
	}
	base := mime
	if i := strings.IndexByte(base, ';'); i >= 0 {
		base = base[:i]
	}
	base = strings.TrimSpace(strings.ToLower(base))
	if strings.HasPrefix(base, "text/") {
		return true
	}
	switch base {
	case "application/json", "application/xml", "application/x-yaml":
		return true
	}
	return false
}

// structuredFields marshals a structured projection to JSON and re-reads it
// as a flat string-keyed object. A projection that is not a JSON object
// (a scalar or array) has no addressable fields and yields an empty map.
func structuredFields(structured any) (map[string]any, error) {
	buf, err := json.Marshal(structured)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(buf, &fields); err != nil {
		// Not a JSON object; no addressable fields.
		return map[string]any{}, nil
	}
	return fields, nil
}

// scalarString renders a JSON-decoded scalar for comparison. Objects and
// arrays render as their JSON so a predicate against a compound field at
// least degrades to a stable string rather than panicking.
func scalarString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case nil:
		return ""
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		if buf, err := json.Marshal(v); err == nil {
			return string(buf)
		}
		return ""
	}
}

// valueString renders a trellis field-predicate value as the string the
// operators compare against.
func valueString(v trellis.Value) string {
	switch t := v.(type) {
	case trellis.StringValue:
		return t.Value
	case trellis.Bareword:
		return t.Name
	case trellis.DigestTerm:
		return "@" + t.Digest
	case trellis.MarklTerm:
		return t.Purpose + "@" + t.Digest
	default:
		return ""
	}
}

// compare applies a field operator to two strings. Ordering operators use
// lexicographic comparison, which orders the canonical fixed-width date
// forms (YYYYMMDD, RFC3339) correctly — richer typing is deferred (FDR 0022).
func compare(op trellis.FieldOp, got, want string) bool {
	switch op {
	case trellis.FieldOpEq:
		return got == want
	case trellis.FieldOpNotEq:
		return got != want
	case trellis.FieldOpContains:
		return strings.Contains(got, want)
	case trellis.FieldOpPrefix:
		return strings.HasPrefix(got, want)
	case trellis.FieldOpSuffix:
		return strings.HasSuffix(got, want)
	case trellis.FieldOpLt:
		return got < want
	case trellis.FieldOpLte:
		return got <= want
	case trellis.FieldOpGt:
		return got > want
	case trellis.FieldOpGte:
		return got >= want
	default:
		// FieldOpRegex / FieldOpInvalid are rejected by Validate.
		return false
	}
}
