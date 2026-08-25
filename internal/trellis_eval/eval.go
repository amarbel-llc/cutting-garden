// Package trellis_eval evaluates a parsed trellis Query (internal/trellis)
// against a plugin's traversal tree (internal/cutting_garden_plugins),
// returning the Nodes matched by the query's last step (RFC 0014, FDR 0022).
//
// This is the slice-2 evaluator (cutting-garden#164, #211, #37): a walk over the
// untyped graph combinators — forward `->`, reverse `<-`, forward closure `->>`,
// backward closure `<<-` — with a predicate layer over Node.Type, Node.Facets,
// tag-dimension membership, and leaf bodies, `:`-only sigils, OR-alternatives,
// and `~=` regex. Reverse and
// the backward closure invert the child relation over the anchor's reachable
// subtree (RootLister is forward-only, so parents are computable no other way;
// FDR 0022 "scan-and-invert"). The anchor comes either from an explicit param
// (Evaluate) or is resolved from a leading-URI origin term in the query itself
// (EvaluateResolving, resolve.go, cutting-garden#37). Forms the grammar admits
// but the evaluator still defers — typed edges, non-`:` sigils, version
// subpaths, mid-query object-identity predicates, and the default-anchor
// (root-aggregate leading-combinator) origin — are rejected up front by Validate,
// never silently mismatched. See docs/features/0022-trellis.md for the boundary
// taxonomy.
//
// A bare-identifier (tag) term is NOT deferred (#231 slice 3): it matches
// against the subject node's tag-dimension values through the dimension's
// resolved TagInterpreter — exact under naive, transitive along the segment
// path under dodder-hyphen (RFC 0019 §5/§6.2). A tag dimension is a FieldTag
// field the plugin declares via the optional UnifiedDescriber capability; a
// plugin declaring none has no tag dimension, so a bare tag matches nothing.
//
// It lives beside the parser (internal/trellis) rather than inside it so the
// pure-parser package stays free of the traversal-SDK dependency the
// evaluator needs.
package trellis_eval

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Evaluate resolves q against anchor's subtree via lister, returning the
// Nodes matched by q's last step. The query's first step filters anchor's
// immediate children (the set `list <anchor>` prints); each subsequent step is
// reached by its preceding combinator over the prior step's matches — forward
// `->` descends one level, reverse `<-` ascends one level, `->>`/`<<-` take the
// transitive closure in that direction — and then filtered. Every frontier is
// deduplicated by URI.
//
// q must be within the evaluator's supported subset (see Validate); a query
// using a deferred form is rejected before any traversal happens. A predicate
// that needs a leaf fetch against a plugin that is not a LeafReader simply does
// not match (graceful degradation), rather than erroring.
func Evaluate(
	ctx context.Context,
	q *trellis.Query,
	anchor *url.URL,
	lister cgp.RootLister,
	opts ...EvaluateOption,
) ([]cgp.Node, error) {
	if err := Validate(q); err != nil {
		return nil, err
	}

	ev := newEvaluator(lister, anchor, opts...)
	return ev.run(ctx, q.Path.Steps, q.Path.Combinators)
}

// EvaluateOption configures an evaluation. The zero-option call preserves each
// tag field's plugin-declared interpreter default; the only option so far layers
// the global [tags] config override on top (RFC 0019 §4).
type EvaluateOption func(*evaluator)

// WithTagsInterpreter sets the global [tags] interpreter override the evaluator
// applies when resolving a tag field's interpreter for a bare-identifier term
// (#231 slice 3). The override wins over the field's declared default; an empty
// override (the default) leaves each field's declared interpreter in force. A
// caller wires it from cfg.Tags.Interpreter (command_components.LoadDefaultConfig),
// the same value organize's membership path resolves through.
func WithTagsInterpreter(override string) EvaluateOption {
	return func(ev *evaluator) { ev.tagsInterpreterOverride = override }
}

// run evaluates a validated steps-and-combinators body against ev.anchor: the
// first step filters the anchor's children, and each subsequent step is reached
// by its preceding combinator over the prior frontier and filtered. An empty
// body (no steps) returns the anchor's children unfiltered — the shape the
// origin-resolving entry point produces for a bare `<uri>` origin, matching the
// set `list <uri>` prints (resolve.go, cutting-garden#37). Shared by the
// explicit-anchor Evaluate and EvaluateResolving so both anchor identically.
func (ev *evaluator) run(
	ctx context.Context, steps []trellis.Step, combinators []trellis.Combinator,
) ([]cgp.Node, error) {
	current, err := ev.listEnriched(ctx, ev.anchor)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	if len(steps) == 0 {
		return current, nil
	}
	if current, err = ev.filter(ctx, current, steps[0]); err != nil {
		return nil, err
	}

	for i := 1; i < len(steps); i++ {
		reached, err := ev.traverse(ctx, current, combinators[i-1].Kind)
		if err != nil {
			return nil, err
		}
		if current, err = ev.filter(ctx, reached, steps[i]); err != nil {
			return nil, err
		}
	}

	return current, nil
}

// evaluator carries the once-probed optional capabilities of the lister so
// per-node matching does not repeat the type assertions, plus the facet
// schema (tag -> declared dimension keys) used to route a field predicate to
// the cheap Node.Facets path versus a leaf fetch, plus the anchor and a lazily
// built child->parents index the backward combinators invert against.
type evaluator struct {
	lister   cgp.RootLister
	enriched cgp.EnrichedLister // nil when the plugin serves no enriched listing
	anchor   *url.URL
	leaf     cgp.LeafReader // nil when the plugin cannot fetch leaves
	// facets maps tag -> declared dimension key -> the dimension's Kind. The
	// Kind is carried (not just presence) so matchFacet can give a FacetDate
	// dimension's `=` the hierarchy-prefix semantics the rest of the facet
	// surface has (cutting-garden#230).
	facets map[string]map[string]cgp.FacetKind

	// unified is the plugin's once-probed UnifiedDescriber, letting a bare-tag
	// term enumerate a node type's FieldTag dimensions (#231 slice 3). nil when
	// the plugin declares no unified fields — a bare tag then matches nothing.
	unified cgp.UnifiedDescriber
	// tagsInterpreterOverride is the global [tags] interpreter override
	// (RFC 0019 §4) a bare-tag term layers over each tag field's declared
	// default. "" (the WithTagsInterpreter-unset default) leaves the field's
	// declared interpreter in force.
	tagsInterpreterOverride string

	// parents maps a node's URI to the nodes under anchor whose immediate
	// children include it. Built on first backward use by scanning anchor's
	// reachable subtree once (see inverse); parentsBuilt guards the one-time
	// build so a genuinely empty index is not rebuilt.
	parents      map[string][]cgp.Node
	parentsBuilt bool

	// regex memoizes compiled `~=` patterns so a walk compiles each pattern
	// once rather than per candidate node. Populated lazily by regexFor.
	regex map[string]*regexp.Regexp
}

func newEvaluator(
	lister cgp.RootLister, anchor *url.URL, opts ...EvaluateOption,
) *evaluator {
	ev := &evaluator{lister: lister, anchor: anchor}
	if el, ok := lister.(cgp.EnrichedLister); ok {
		ev.enriched = el
	}
	if lr, ok := lister.(cgp.LeafReader); ok {
		ev.leaf = lr
	}
	if ud, ok := lister.(cgp.UnifiedDescriber); ok {
		ev.unified = ud
	}
	if fd, ok := lister.(cgp.FacetDescriber); ok {
		ev.facets = make(map[string]map[string]cgp.FacetKind)
		for _, ntf := range fd.DescribeFacets() {
			kinds := make(map[string]cgp.FacetKind, len(ntf.Dimensions))
			for _, dim := range ntf.Dimensions {
				kinds[dim.Key] = dim.Kind
			}
			ev.facets[ntf.Tag] = kinds
		}
	}
	for _, opt := range opts {
		opt(ev)
	}
	return ev
}

// listEnriched returns u's children, preferring the plugin's enriched listing
// (Facets and Fields populated) over the metadata-only ListRoots when the
// plugin serves one for this node. It passes no filter — the evaluator applies
// its own predicates — so ListEnriched returns the full, level-scoped child set
// (RFC 0012 §12.2), and a decline (ok==false, e.g. caldav at a calendar-home
// whose children are calendars, not the enrichable unit) or a plugin without
// the capability falls back to ListRoots.
//
// This is what makes a facet predicate match against a plugin (caldav) whose
// plain ListRoots leaves Node.Facets empty, and what enriches the returned
// nodes for display (cutting-garden#212). matchFacet already reads Node.Facets,
// so populating them is the whole fix. The cost is one bulk enriched fetch per
// container rather than a metadata-only listing; a query referencing no facet
// dimension pays it without needing it — a conditional "enrich only when the
// query touches a facet field" optimization is possible if that cost bites.
func (ev *evaluator) listEnriched(
	ctx context.Context, u *url.URL,
) ([]cgp.Node, error) {
	if ev.enriched != nil {
		nodes, ok, err := ev.enriched.ListEnriched(ctx, u, nil)
		if err != nil {
			return nil, err
		}
		if ok {
			return nodes, nil
		}
	}
	return ev.lister.ListRoots(ctx, u)
}

// traverse maps the current frontier through one combinator to the next
// frontier, before the next step's predicate filters it. Only the untyped
// graph directions reach here; Validate rejects the typed forms.
func (ev *evaluator) traverse(
	ctx context.Context, frontier []cgp.Node, kind trellis.CombinatorKind,
) ([]cgp.Node, error) {
	switch kind {
	case trellis.CombinatorFwd:
		return ev.descend(ctx, frontier)
	case trellis.CombinatorBack:
		return ev.ascend(ctx, frontier)
	case trellis.CombinatorFwdClosure:
		return ev.descendClosure(ctx, frontier)
	case trellis.CombinatorBackClosure:
		return ev.ascendClosure(ctx, frontier)
	default:
		// Unreachable: Validate rejects every other combinator kind.
		return nil, errors.BadRequestf(
			"trellis: internal: unvalidated combinator %v reached the evaluator", kind,
		)
	}
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
		children, err := ev.listEnriched(ctx, n.URI)
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

// inverse builds (once) and returns the child->parents index over the anchor's
// reachable subtree: parents[childURI] is the set of nodes whose immediate
// children include that child. RootLister only lists children, so this scan is
// the only way to answer a backward hop (FDR 0022 "scan-and-invert"). The scan
// is bounded by the anchor's subtree, deduplicates parents by URI, and is
// cycle-safe via a visited set.
//
// The anchor itself is a URL, not a Node, so the anchor's immediate children
// have no entry here — their only parent is the origin, which cannot be
// materialized as a result Node. Reversing off a depth-1 frontier therefore
// yields nothing: a query cannot reverse above its anchor.
func (ev *evaluator) inverse(ctx context.Context) (map[string][]cgp.Node, error) {
	if ev.parentsBuilt {
		return ev.parents, nil
	}
	parents := make(map[string][]cgp.Node)

	roots, err := ev.listEnriched(ctx, ev.anchor)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	// BFS over the reachable subtree. queue holds nodes whose children have not
	// yet been listed; visited guards against cycles and shared-child
	// re-descent while still recording every parent of a shared child.
	queue := append([]cgp.Node(nil), roots...)
	visited := make(map[string]struct{}, len(roots))
	for _, r := range roots {
		visited[r.URIString()] = struct{}{}
	}

	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		if parent.URI == nil {
			continue
		}
		children, err := ev.listEnriched(ctx, parent.URI)
		if err != nil {
			return nil, errors.Wrap(err)
		}
		for _, c := range children {
			key := c.URIString()
			parents[key] = appendUniqueByURI(parents[key], parent)
			if _, seen := visited[key]; seen {
				continue
			}
			visited[key] = struct{}{}
			queue = append(queue, c)
		}
	}

	ev.parents = parents
	ev.parentsBuilt = true
	return parents, nil
}

// ascend maps each node in frontier to its parents within the anchor's subtree
// (the reverse combinator `<-`, one backward hop), deduplicating by URI. Nodes
// at the anchor's top level have no parent Node and contribute nothing (see
// inverse).
func (ev *evaluator) ascend(
	ctx context.Context, frontier []cgp.Node,
) ([]cgp.Node, error) {
	parents, err := ev.inverse(ctx)
	if err != nil {
		return nil, err
	}
	var out []cgp.Node
	seen := make(map[string]struct{})
	for _, n := range frontier {
		for _, p := range parents[n.URIString()] {
			key := p.URIString()
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, p)
		}
	}
	return out, nil
}

// descendClosure returns every transitive descendant of the frontier (the
// forward closure `->>`, one-or-more hops), deduplicated by URI. The frontier
// nodes themselves are excluded (one-or-more, not zero) and seed the visited
// set so a cycle back onto them terminates.
func (ev *evaluator) descendClosure(
	ctx context.Context, frontier []cgp.Node,
) ([]cgp.Node, error) {
	var out []cgp.Node
	visited := make(map[string]struct{}, len(frontier))
	for _, n := range frontier {
		visited[n.URIString()] = struct{}{}
	}
	queue := append([]cgp.Node(nil), frontier...)
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if n.URI == nil {
			continue
		}
		children, err := ev.listEnriched(ctx, n.URI)
		if err != nil {
			return nil, errors.Wrap(err)
		}
		for _, c := range children {
			key := c.URIString()
			if _, seen := visited[key]; seen {
				continue
			}
			visited[key] = struct{}{}
			out = append(out, c)
			queue = append(queue, c)
		}
	}
	return out, nil
}

// ascendClosure returns every transitive ancestor of the frontier within the
// anchor's subtree (the backward closure `<<-`, one-or-more hops), deduplicated
// by URI. Like descendClosure the frontier seeds the visited set and is
// excluded from the result.
func (ev *evaluator) ascendClosure(
	ctx context.Context, frontier []cgp.Node,
) ([]cgp.Node, error) {
	parents, err := ev.inverse(ctx)
	if err != nil {
		return nil, err
	}
	var out []cgp.Node
	visited := make(map[string]struct{}, len(frontier))
	for _, n := range frontier {
		visited[n.URIString()] = struct{}{}
	}
	queue := append([]cgp.Node(nil), frontier...)
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, p := range parents[n.URIString()] {
			key := p.URIString()
			if _, seen := visited[key]; seen {
				continue
			}
			visited[key] = struct{}{}
			out = append(out, p)
			queue = append(queue, p)
		}
	}
	return out, nil
}

// appendUniqueByURI appends n to nodes unless a node with the same URI is
// already present, keeping a parent set free of duplicates.
func appendUniqueByURI(nodes []cgp.Node, n cgp.Node) []cgp.Node {
	key := n.URIString()
	for _, existing := range nodes {
		if existing.URIString() == key {
			return nodes
		}
	}
	return append(nodes, n)
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
		// Validate has narrowed the group body to a forward existential
		// subpath or an OR-alternatives group.
		switch body := b.Group.Body.(type) {
		case trellis.SubPath:
			return ev.matchSubPath(ctx, n, body)
		case trellis.Alternatives:
			return ev.matchAlternatives(ctx, n, body)
		default:
			return false, errors.BadRequestf(
				"trellis: internal: unvalidated group body %T reached the evaluator", body,
			)
		}
	case trellis.IdentBasicTerm:
		// A bare identifier is a tag predicate (#231 slice 3): match it against
		// the node's tag-dimension membership through the resolved interpreter.
		// Ident.Name carries the tag without decoration; its version sigil, if
		// any, is a no-op `:` (Validate rejects every other).
		return ev.matchTag(n, b.Ident.Name)
	case trellis.SigilBasicTerm:
		// A bare `:` selects the latest version-set (the default) and adds
		// no predicate — a no-op that matches every candidate.
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

// matchAlternatives is the OR-group `[a, b]`: the node matches if it satisfies
// every term of ANY one alternative (each alternative is an AND-run of terms,
// ConjRun). Reuses matchTerm, so an alternative may itself carry type, field,
// or subpath predicates.
func (ev *evaluator) matchAlternatives(
	ctx context.Context, n cgp.Node, alts trellis.Alternatives,
) (bool, error) {
	for _, alt := range alts.Alts {
		matched := true
		for _, term := range alt.Terms {
			ok, err := ev.matchTerm(ctx, n, term)
			if err != nil {
				return false, err
			}
			if !ok {
				matched = false
				break
			}
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

// matchTag evaluates a bare-identifier (tag) term against the node's tag
// dimensions (#231 slice 3): the RFC 0014 bare term the parser leaves for the
// type system to interpret. For each FieldTag dimension the node type declares
// (via the plugin's optional UnifiedDescriber), it resolves the dimension's tag
// interpreter — the field's declared default (UnifiedField.Interpreter, "" ->
// naive) with the global [tags] override layered on top — reads the node's tags
// for that dimension from Node.Facets, and asks the interpreter whether term
// matches (naive: exact, RFC 0019 §5; dodder-hyphen: transitive along the
// segment path, §6.2). Any dimension matching makes the node match. A plugin
// that declares no unified fields has no tag dimension and so matches nothing.
// An unknown interpreter name (from either the field or the override) is a loud
// bad request, surfaced rather than defaulted (RFC 0019 §3).
func (ev *evaluator) matchTag(n cgp.Node, term string) (bool, error) {
	if ev.unified == nil {
		return false, nil
	}
	for _, ntf := range ev.unified.DescribeUnified() {
		if ntf.Tag != n.Type {
			continue
		}
		for _, codec := range ntf.Codecs {
			for _, field := range codec.Fields() {
				if field.Kind != cgp.FieldTag {
					continue
				}
				interp, err := resolveTagInterpreter(
					field.Interpreter, ev.tagsInterpreterOverride,
				)
				if err != nil {
					return false, err
				}
				if interp.Matches(tagKeys(n.Facets[field.Key]), term) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

// tagKeys projects a facet dimension's membership values onto the raw tag
// strings the interpreter matches against — each FacetValue.Key is one tag the
// node carries in that dimension.
func tagKeys(values []cgp.FacetValue) []string {
	tags := make([]string, 0, len(values))
	for _, v := range values {
		tags = append(tags, v.Key)
	}
	return tags
}

// resolveTagInterpreter selects a tag dimension's interpreter under RFC 0019 §4
// precedence: the global [tags] override when set, else the field's declared
// default, else "naive". The resolved name MUST name a registered interpreter —
// an unknown name is a loud bad request, never a silent default (RFC 0019 §3).
// It matches command_components.ResolveTagInterpreter's precedence (tags slice 3
// Task A2) PLUS an empty->"naive" fallback A2 lacks: A2's callers pre-default the
// field to "naive" before calling, whereas matchTag passes the field's possibly-
// empty UnifiedField.Interpreter straight through (a FieldTag field may declare no
// explicit interpreter), so the default belongs here. Reimplemented over
// cgp.LookupTagInterpreter rather than importing the CLI seam so the evaluator
// keeps its lean dependency surface (cgp/trellis/errors) for a six-line resolution.
func resolveTagInterpreter(
	fieldDefault, override string,
) (cgp.TagInterpreter, error) {
	name := override
	if name == "" {
		name = fieldDefault
	}
	if name == "" {
		name = "naive"
	}
	interp, ok := cgp.LookupTagInterpreter(name)
	if !ok {
		return nil, errors.BadRequestf(
			"tag interpreter %q is not registered (builtins: naive, dodder-hyphen)",
			name,
		)
	}
	return interp, nil
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

	if field == bodyField {
		return ev.matchBody(ctx, n, fp)
	}
	if kind, ok := ev.facetKind(n.Type, field); ok {
		return ev.matchFacet(n.Facets[field], fp, kind), nil
	}
	return ev.matchLeafField(ctx, n, field, fp)
}

const bodyField = "_body"

// facetKind reports whether field is a declared facet dimension of tag,
// and that dimension's Kind — matchFacet needs the Kind to give a date
// dimension's `=` its hierarchy-prefix semantics.
func (ev *evaluator) facetKind(tag, field string) (cgp.FacetKind, bool) {
	kind, ok := ev.facets[tag][field]
	return kind, ok
}

// matchFacet matches fp against a node's membership in one facet dimension.
// The node matches iff, for some query value, some facet-value bucket key
// satisfies the operator (existential over both). A dimension the node does
// not contribute to has no bucket keys and so matches nothing.
//
// On a FacetDate-kind dimension, `=` with a shape-valid date-bucket value
// (YYYY / YYYY-MM / YYYY-MM-DD, ParseDateBucket) is hierarchy containment
// rather than string equality — `date_due=2026-09` matches the day-precise
// key "2026-09-10" — mirroring FacetPredicate.matches so trellis, `list
// --filter`, and the mcp read_facets filter agree (the design's uniformity
// decision, cutting-garden#230). `!=` on the same shape is the symmetric
// negation, keeping the existential structure the generic path has: the node
// matches iff SOME bucket key does NOT fall inside the bucket — so
// `date_due!=2026-09` excludes a node whose only key is "2026-09-10" instead
// of matching it on raw string inequality. A value that is not shape-valid
// falls back to exact semantics for both operators: the evaluator has no
// schema-aware validation pass for predicate values (Validate sees only the
// query), so a malformed date simply matches nothing here (`=`) or behaves
// as raw inequality (`!=`), exactly as any other non-existent bucket key
// would. Every other operator keeps trellis's raw string semantics — `^=` on
// a date key stays an unvalidated string prefix (RFC 0014).
func (ev *evaluator) matchFacet(
	values []cgp.FacetValue, fp trellis.FieldPred, kind cgp.FacetKind,
) bool {
	for _, qv := range fp.Values {
		want := valueString(qv)
		if kind == cgp.FacetDate &&
			(fp.Op == trellis.FieldOpEq || fp.Op == trellis.FieldOpNotEq) {
			if _, ok := cgp.ParseDateBucket(want); ok {
				for _, fv := range values {
					contained := cgp.DateBucketMatches(fv.Key, want)
					if contained == (fp.Op == trellis.FieldOpEq) {
						return true
					}
				}
				continue
			}
		}
		for _, fv := range values {
			if ev.satisfies(fp.Op, fv.Key, want) {
				return true
			}
		}
	}
	return false
}

// matchLeafField matches fp against the node's named field. It prefers the
// inline Node.Fields projection (populated by an enriched listing,
// cutting-garden#212), falling back to a leaf fetch — ReadLeaf's structured
// projection, JSON-marshalled and indexed by flat field name — only when the
// field is not present inline. A plugin that cannot fetch the leaf, a non-leaf
// node, a nil structured view, or an absent field all yield no match rather
// than an error.
func (ev *evaluator) matchLeafField(
	ctx context.Context, n cgp.Node, field string, fp trellis.FieldPred,
) (bool, error) {
	// Cheap path: the enriched listing may already carry this field inline,
	// avoiding a per-node leaf fetch. The inline value is authoritative for
	// the field, so a present-but-unmatched field returns false here rather
	// than re-reading the same data from the leaf.
	if raw, present := n.Fields[field]; present {
		return ev.matchScalar(scalarString(raw), fp), nil
	}

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
	return ev.matchScalar(scalarString(raw), fp), nil
}

// matchScalar reports whether got satisfies fp for any of fp's values — the
// value list distributes the operator as OR (RFC 0014).
func (ev *evaluator) matchScalar(got string, fp trellis.FieldPred) bool {
	for _, qv := range fp.Values {
		if ev.satisfies(fp.Op, got, valueString(qv)) {
			return true
		}
	}
	return false
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
	return ev.matchScalar(string(content.Raw), fp), nil
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
		// FieldOpRegex is handled by satisfies before reaching compare;
		// FieldOpInvalid is rejected by Validate.
		return false
	}
}

// satisfies applies a field operator to two strings, handling the regex
// operator (`~=`) — compiled once per pattern and cached — and delegating every
// other operator to compare. Validate has already rejected an invalid regex, so
// a nil compiled pattern (an impossible compile failure) is treated as a
// non-match defensively.
func (ev *evaluator) satisfies(op trellis.FieldOp, got, want string) bool {
	if op == trellis.FieldOpRegex {
		re := ev.regexFor(want)
		return re != nil && re.MatchString(got)
	}
	return compare(op, got, want)
}

// regexFor returns the compiled form of pattern, memoized per evaluator so a
// walk compiles each pattern once rather than per candidate node. A compile
// failure (unreachable after Validate) caches and returns nil so a bad pattern
// is not recompiled per node.
func (ev *evaluator) regexFor(pattern string) *regexp.Regexp {
	if re, ok := ev.regex[pattern]; ok {
		return re
	}
	if ev.regex == nil {
		ev.regex = make(map[string]*regexp.Regexp)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		re = nil
	}
	ev.regex[pattern] = re
	return re
}
