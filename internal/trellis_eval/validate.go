package trellis_eval

import (
	"regexp"

	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// unsupported is the message template for a grammatically-valid query that
// uses a form the evaluator defers. The %s names the specific form. The
// remaining deferrals are tracked in cutting-garden#211 (the slice-2 tracker).
const unsupported = "trellis: %s is not yet supported by the evaluator (cutting-garden#211)"

// qualifierReserved is the rejection for a `(x)` / `k=(x)` qualifier term in
// QUERY position (native tags design G10): the spelling belongs to organize's
// group-by dialect this slice and has no evaluator meaning yet.
const qualifierReserved = "trellis: qualifier terms are reserved; not evaluable yet " +
	"(%s is a group-by spelling — see `organize --group-by`)"

// Validate reports whether q is within the explicit-anchor evaluator's supported
// subset, returning a descriptive bad-request error for the first deferred form
// it encounters. Evaluate calls it before any traversal, so a query that reaches
// for a deferred feature fails fast and loudly rather than silently mismatching.
// The supported subset: a path anchored at an explicit URI whose steps are
// joined by the untyped combinators `->` / `<-` / `->>` / `<<-`, whose terms are
// type predicates, field predicates (any operator, `~=` included), bare-identifier
// (tag) predicates, OR-alternatives, and existential single-step forward subpaths,
// with only the `:` sigil. Typed edges, the default-anchor (root-aggregate
// leading-combinator) origin, version subpaths, non-`:` sigils, and mid-query
// object-identity terms remain deferred. Qualifier terms (`(x)`, `k=(x)`) are
// RESERVED in query position (native tags G10) — organize's group-by dialect
// is their only consumer this slice. A bare-identifier term is NOT deferred
// (#231 slice 3): it evaluates as a tag predicate through the node's
// tag-dimension interpreter (RFC 0014's deferred bare term, RFC 0019 §5/§6.2).
// A leading-URI origin term is NOT handled here — resolving it
// is the origin-in-expression path's job (EvaluateResolving / validateOriginQuery,
// resolve.go, cutting-garden#37).
func Validate(q *trellis.Query) error {
	if q == nil {
		return errors.BadRequestf("trellis: nil query")
	}
	if q.Path.Leading != nil {
		return errors.BadRequestf(unsupported,
			"a leading combinator (the default-anchor origin — pass an explicit <uri> instead)")
	}
	if len(q.Path.Steps) == 0 {
		return errors.BadRequestf("trellis: empty query")
	}
	return validatePathBody(q.Path.Steps, q.Path.Combinators)
}

// validatePathBody validates the steps-and-combinators body of a path against
// the evaluator's supported subset, independent of how the path is anchored.
// Both the explicit-anchor Validate and the origin-resolving path share it —
// the latter's remainder (after the leading URI origin is peeled off) is an
// ordinary anchored body (resolve.go, cutting-garden#37).
func validatePathBody(steps []trellis.Step, combinators []trellis.Combinator) error {
	for _, c := range combinators {
		switch c.Kind {
		case trellis.CombinatorFwd, trellis.CombinatorBack,
			trellis.CombinatorFwdClosure, trellis.CombinatorBackClosure:
			// The untyped graph directions — supported (slice-2a).
		default:
			// Typed forward/back/closure combinators (and the invalid zero
			// value) remain deferred: the edge predicate depends on
			// edges-as-reference-valued-fields (hyphence#2).
			return errors.BadRequestf(unsupported, combinatorName(c.Kind))
		}
	}
	for _, step := range steps {
		if err := validateStep(step); err != nil {
			return err
		}
	}
	return nil
}

func validateStep(step trellis.Step) error {
	for _, term := range step.Terms {
		if err := validateBasic(term.Basic); err != nil {
			return err
		}
	}
	return nil
}

func validateBasic(basic trellis.BasicTerm) error {
	switch b := basic.(type) {
	case trellis.TypeBasicTerm:
		return validateSigil(b.Sigil)
	case trellis.QualifierBasicTerm:
		return errors.BadRequestf(qualifierReserved, "`"+b.String()+"`")
	case trellis.FieldPredBasicTerm:
		for _, v := range b.FieldPred.Values {
			if q, ok := v.(trellis.Qualifier); ok {
				return errors.BadRequestf(qualifierReserved,
					"`"+q.String()+"` in field `"+b.FieldPred.Field.Name+"`")
			}
		}
		if b.FieldPred.Op == trellis.FieldOpRegex {
			// Reject an invalid pattern up front (fail fast, actionable)
			// rather than at match time, where the evaluator has no error
			// path; a valid `~=` is an honest per-node walk.
			for _, v := range b.FieldPred.Values {
				if _, err := regexp.Compile(valueString(v)); err != nil {
					return errors.BadRequestf(
						"trellis: invalid `~=` regex %q: %v", valueString(v), err,
					)
				}
			}
		}
		return nil
	case trellis.GroupBasicTerm:
		return validateGroup(b.Group)
	case trellis.SigilBasicTerm:
		return validateSigil(&b.Sigil)
	case trellis.IdentBasicTerm:
		// A bare identifier is a tag predicate (#231 slice 3): the evaluator
		// matches it against the node's tag-dimension values through the
		// dimension's resolved interpreter. Only its optional version sigil is
		// still a per-host deferral; a bare `project` carries none.
		return validateSigil(b.Sigil)
	case trellis.DigestBasicTerm, trellis.MarklBasicTerm, trellis.QuotedRefBasicTerm:
		return errors.BadRequestf(unsupported,
			"an object-identity term (@digest, purpose@digest, or a quoted reference)")
	default:
		return errors.BadRequestf(unsupported, "this term")
	}
}

func validateGroup(g trellis.Group) error {
	switch body := g.Body.(type) {
	case trellis.SubPath:
		if body.Combinator.Kind != trellis.CombinatorFwd {
			return errors.BadRequestf(unsupported,
				"a subpath with a non-forward combinator")
		}
		if body.Path != nil {
			if body.Path.Leading != nil || len(body.Path.Combinators) > 0 {
				return errors.BadRequestf(unsupported,
					"a multi-step subpath (subpaths are a single existential step)")
			}
			for _, step := range body.Path.Steps {
				if err := validateStep(step); err != nil {
					return err
				}
			}
		}
		return nil
	case trellis.VersionSub:
		return errors.BadRequestf(unsupported, "a version subpath [+ ...]")
	case trellis.Alternatives:
		for _, alt := range body.Alts {
			for _, term := range alt.Terms {
				if err := validateBasic(term.Basic); err != nil {
					return err
				}
			}
		}
		return nil
	default:
		return errors.BadRequestf(unsupported, "this group")
	}
}

// validateSigil accepts only the default `:` (latest) sigil; every other
// version-set selector is a per-host capability the evaluator does not yet
// implement (FDR 0022 host capability contract).
func validateSigil(s *trellis.Sigil) error {
	if s == nil {
		return nil
	}
	if s.Runes != ":" {
		return errors.BadRequestf(
			"trellis: sigil %q is not supported by this host (only `:` latest)",
			s.Runes,
		)
	}
	return nil
}

func combinatorName(k trellis.CombinatorKind) string {
	switch k {
	case trellis.CombinatorBack:
		return "the reverse combinator `<-`"
	case trellis.CombinatorFwdClosure:
		return "the forward-closure combinator `->>`"
	case trellis.CombinatorBackClosure:
		return "the backward-closure combinator `<<-`"
	case trellis.CombinatorTypedFwd:
		return "a typed forward combinator `-[...]->`"
	case trellis.CombinatorTypedBack:
		return "a typed backward combinator `<-[...]-`"
	case trellis.CombinatorTypedClosure:
		return "a typed-closure combinator `-[...]->>`"
	default:
		return "this combinator"
	}
}
