package trellis_eval

import (
	"context"
	"net/url"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// OriginResolver maps a leading-URI origin term to the anchor URL and the
// RootLister that serves its subtree — the injection that lets the evaluator
// take its origin from the query expression itself rather than an out-of-band
// anchor (cutting-garden#37, the 2b-full resolver). command_components.
// ResolveRootListerPlugin has exactly this shape, so the evaluator stays
// registry-agnostic: it never imports the scheme registry, it is handed a
// resolver that consults it. Resolution also performs any plugin-specific
// canonicalization (a caldav account alias `caldav:fastmail` -> its home URL,
// cutting-garden#48).
type OriginResolver interface {
	Resolve(uri string) (*url.URL, cgp.RootLister, error)
}

// EvaluateResolving evaluates an origin-in-expression query: its leading step is
// a lone URI identity term (the origin), which resolver maps to an anchor and a
// lister; the remainder of the path then evaluates against that anchor exactly
// as an explicit-anchor Evaluate would. This is the origin-in-expression mode
// (cutting-garden#37): a consumer passes one trellis expression carrying its own
// anchor (`caldav:fastmail -> component=VEVENT`) rather than a separate <uri>
// plus a filter. A bare `<uri>` origin (no further steps) yields the anchor's
// children unfiltered — the set `list <uri>` prints.
//
// The two anchoring modes are mutually exclusive by construction: Evaluate takes
// an explicit anchor and its first step filters that anchor's children; here no
// anchor is supplied and the first step MUST be the origin. validateOriginQuery
// enforces exactly that, so a missing origin here (or, symmetrically, a leading
// URI handed to Evaluate, which rejects an identity term) is a loud bad request
// rather than a silent mismatch.
//
// opts apply to the resolved-anchor evaluator exactly as Evaluate's do, so the
// two modes stay equivalent under the same options: in particular a bare-tag
// step resolves through the same WithTagsInterpreter override in either entry
// point (#231 slice 3). EvaluateResolving has no production caller yet
// (origin-in-expression is wired only in tests), so threading opts here is what
// keeps that equivalence honest rather than serving a live override consumer.
func EvaluateResolving(
	ctx context.Context,
	q *trellis.Query,
	resolver OriginResolver,
	opts ...EvaluateOption,
) ([]cgp.Node, error) {
	if err := validateOriginQuery(q); err != nil {
		return nil, err
	}

	originURI, err := originString(q.Path.Steps[0])
	if err != nil {
		return nil, err
	}

	anchor, lister, err := resolver.Resolve(originURI)
	if err != nil {
		return nil, errors.Wrapf(err, "trellis: resolve origin %q", originURI)
	}

	// The remainder is the path with the origin term — and the forward
	// combinator bridging it to its children — removed: an ordinary anchored
	// body evaluated against the resolved anchor. `origin -> step1` therefore
	// evaluates identically to Evaluate(anchor, "step1"), the `->` standing in
	// for the implicit origin-node -> children hop.
	//
	// A bridging `->>` instead widens the first remaining step from the origin's
	// children to all of its descendants: `origin ->> step` evaluates like the
	// explicit-anchor `->> step` (#211).
	remSteps := q.Path.Steps[1:]
	var remCombinators []trellis.Combinator
	closure := false
	if len(q.Path.Combinators) > 0 {
		remCombinators = q.Path.Combinators[1:]
		closure = q.Path.Combinators[0].Kind == trellis.CombinatorFwdClosure
	}

	ev := newEvaluator(lister, anchor, opts...)
	return ev.run(ctx, remSteps, remCombinators, closure)
}

// Selection is an origin-in-expression query split for a consumer that
// resolves the origin itself and passes the rest on as a query string.
type Selection struct {
	// Origin is the origin URI, unquoted — what an OriginResolver resolves.
	Origin string
	// OriginSource is the origin as written (quoted when the source quoted
	// it), for echoing the expression back to a user.
	OriginSource string
	// Query is the source text after the origin's `->`, verbatim; empty for a
	// bare origin. Behind a `->>` it keeps the combinator as a LEADING `->>`
	// (`->> step…`), the explicit-anchor closure form, so the text alone says
	// how to evaluate it. Evaluating it against the resolved origin is
	// equivalent to EvaluateResolving over the whole expression.
	Query string
}

// SplitOrigin splits an origin-in-expression query (`origin -> step…`) into its
// origin and the remainder's source text, applying the same origin rules as
// EvaluateResolving: a single undecorated identifier, bridged to the rest by a
// forward `->` or a forward closure `->>`. The remainder is NOT validated
// against the evaluator's supported subset here — Evaluate does that when it
// runs.
func SplitOrigin(src string) (Selection, error) {
	lead, err := trellis.SplitLeadingStep(src)
	if err != nil {
		return Selection{}, errors.BadRequestf("%s", err)
	}
	origin, err := originString(lead.Step)
	if err != nil {
		return Selection{}, err
	}
	query := lead.Rest
	if lead.Combinator != nil {
		if err := originBridgeOK(lead.Combinator.Kind); err != nil {
			return Selection{}, err
		}
		if lead.Combinator.Kind == trellis.CombinatorFwdClosure {
			query = "->> " + query
		}
	}
	return Selection{
		Origin:       origin,
		OriginSource: lead.StepSource,
		Query:        query,
	}, nil
}

// originBridgeOK accepts the combinators that may join an origin to the rest:
// `->` (its children) and `->>` (all its descendants). Every other kind stays
// deferred (cutting-garden#211).
func originBridgeOK(kind trellis.CombinatorKind) error {
	if kind == trellis.CombinatorFwd || kind == trellis.CombinatorFwdClosure {
		return nil
	}
	return errors.BadRequestf(unsupported,
		"a non-forward combinator after the origin URI (only `->` and `->>` bridge "+
			"an origin to what it selects this slice, got "+combinatorName(kind)+")")
}

// validateOriginQuery checks q is a well-formed origin-in-expression query: no
// leading combinator, a leading lone URI origin term, a forward combinator (if
// any) bridging the origin to its children, and a remainder within the
// evaluator's supported subset. The origin term itself would be a deferred
// identity form under the explicit-anchor Validate, so it is checked here
// (originString) and excluded from the remainder body.
func validateOriginQuery(q *trellis.Query) error {
	if q == nil {
		return errors.BadRequestf("trellis: nil query")
	}
	if q.Path.Leading != nil {
		return errors.BadRequestf(unsupported,
			"a leading combinator before the origin URI")
	}
	if len(q.Path.Steps) == 0 {
		return errors.BadRequestf("trellis: empty query (no origin URI)")
	}
	if _, err := originString(q.Path.Steps[0]); err != nil {
		return err
	}
	if len(q.Path.Steps) > 1 {
		if err := originBridgeOK(q.Path.Combinators[0].Kind); err != nil {
			return err
		}
		return validatePathBody(q.Path.Steps[1:], q.Path.Combinators[1:])
	}
	return nil
}

// originString extracts the URI string from a leading origin step: a single,
// undecorated identity term (a plain or quoted identifier). A step with more
// than one term, a negated (`^`) or exact-matched (`=`) term, a non-identity
// term, or a version sigil other than the default `:` is rejected — the origin
// names a node to anchor at, not a predicate to match.
func originString(step trellis.Step) (string, error) {
	if len(step.Terms) != 1 {
		return "", errors.BadRequestf(
			"trellis: the origin must be a single URI term (the leading step has %d terms)",
			len(step.Terms),
		)
	}
	t := step.Terms[0]
	if t.Negate || t.Exact {
		return "", errors.BadRequestf(
			"trellis: the origin URI term cannot be negated (`^`) or exact-matched (`=`)",
		)
	}
	switch b := t.Basic.(type) {
	case trellis.IdentBasicTerm:
		if err := originSigilOK(b.Sigil); err != nil {
			return "", err
		}
		return b.Ident.Name, nil
	case trellis.QuotedRefBasicTerm:
		if err := originSigilOK(b.Sigil); err != nil {
			return "", err
		}
		return b.Ref.Value, nil
	default:
		return "", errors.BadRequestf(
			"trellis: the leading term is not a URI origin (expected a plain or quoted " +
				"identifier)",
		)
	}
}

// originSigilOK rejects a version sigil on the origin URI other than the default
// `:` (latest), which is a no-op: an anchor names a node, not a version-set.
func originSigilOK(s *trellis.Sigil) error {
	if s == nil || s.Runes == ":" {
		return nil
	}
	return errors.BadRequestf(
		"trellis: a version sigil %q on the origin URI is not supported (an anchor "+
			"names a node, not a version-set)", s.Runes,
	)
}
