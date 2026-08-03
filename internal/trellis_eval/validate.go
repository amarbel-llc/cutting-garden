package trellis_eval

import (
	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// unsupported is the message template for a grammatically-valid query that
// uses a form the evaluator defers. The %s names the specific form. The
// remaining deferrals are tracked in cutting-garden#211 (the slice-2 tracker).
const unsupported = "trellis: %s is not yet supported by the evaluator (cutting-garden#211)"

// Validate reports whether q is within the evaluator's supported subset,
// returning a descriptive bad-request error for the first deferred form it
// encounters. Evaluate calls it before any traversal, so a query that reaches
// for a deferred feature fails fast and loudly rather than silently
// mismatching. The supported subset (slice-2a): a path anchored at an explicit
// URI whose steps are joined by the untyped combinators `->` / `<-` / `->>` /
// `<<-`, whose terms are type predicates, field predicates (any operator but
// `~=`), and existential single-step forward subpaths, with only the `:`
// sigil. Typed edges, the default-anchor origin, version subpaths,
// OR-alternatives, and identity/bare-tag terms remain deferred.
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
	for _, c := range q.Path.Combinators {
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
	for _, step := range q.Path.Steps {
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
	case trellis.FieldPredBasicTerm:
		if b.FieldPred.Op == trellis.FieldOpRegex {
			return errors.BadRequestf(unsupported, "the `~=` (regex) operator")
		}
		return nil
	case trellis.GroupBasicTerm:
		return validateGroup(b.Group)
	case trellis.SigilBasicTerm:
		return validateSigil(&b.Sigil)
	case trellis.IdentBasicTerm:
		return errors.BadRequestf(unsupported,
			"a bare identifier (tag) predicate")
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
		return errors.BadRequestf(unsupported, "an OR-alternatives group [a, b]")
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
