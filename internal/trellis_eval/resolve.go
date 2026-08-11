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
func EvaluateResolving(
	ctx context.Context,
	q *trellis.Query,
	resolver OriginResolver,
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
	remSteps := q.Path.Steps[1:]
	var remCombinators []trellis.Combinator
	if len(q.Path.Combinators) > 0 {
		remCombinators = q.Path.Combinators[1:]
	}

	ev := newEvaluator(lister, anchor)
	return ev.run(ctx, remSteps, remCombinators)
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
		if kind := q.Path.Combinators[0].Kind; kind != trellis.CombinatorFwd {
			return errors.BadRequestf(unsupported,
				"a non-forward combinator after the origin URI (only `->` bridges an "+
					"origin to its children this slice, got "+combinatorName(kind)+")")
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
