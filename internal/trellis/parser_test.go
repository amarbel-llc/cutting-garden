package trellis

import (
	"strings"
	"testing"
)

// conformanceVectors are RFC 0014's Examples, taken verbatim from
// docs/rfcs/0014-trellis.peg's trailing comment block ("each MUST parse").
// Two-line vectors in that file are joined with a space here; the file's
// prose commentary (the em-dash lines) is not part of the query text.
//
// NOTE #12/#13 (the leading-combinator forms) are deliberately absent from
// this table — see TestConformanceVectors_KnownGap_LeadingCombinator below
// for why, and cutting-garden#152 for the tracked grammar gap.
var conformanceVectors = []string{
	`!task priority-1_should [due<="2026-08-01"]`,
	`!newsblur-story-v1 year=2026 [-> _body*=["zettelkasten", "localfirst", "git", "obsidian", "roam research"]]`,
	`!caldav-object-v1 component=VEVENT dtstart^=["20260718", "20260719"]`,
	`!caldav-object-v1 component=VEVENT dtstart>="20260720" dtstart<"20260727"`,
	`!forgejo-issue-v1 created^="2026-07-17" state=closed`,
	`!forgejo-issue-v1 state=open [+ state=closed]`,
	`!newsblur-story-v1 [-> [+ _body*="merkle"]]`,
	`+`,
	`!web-page-v1 [+]`,
	`!web-page-v1 ^[+]`,
	`!task ^done project-cutting-garden`,
	`"event.summary"*="standup"`,
	`[story-8841 !newsblur-story-v1 year=2026 [-> content-8841 @blake2b256-9ft3x]]`,
	`caldav:fastmail -> component=VEVENT dtstart^="20260718"`,
	`!root-v1 scheme=caldav -> !caldav-object-v1 dtstart^="20260718"`,
	`work -> !caldav-object-v1 component=VEVENT`,
	`web:http://example.com +`,
	`"one/uno.zettel"`,
	`12.7`,
	`_mother=@blake2b256-9ft3x`,
	`piggy-piv_auth-v1@ssh_ecdsa_nistp256_pub-qqxyz`,
	`one/uno@blake2b256-9ft3x`,
	`"my thing"@blake2b256-9ft3x`,
	`blocks=task/other@blake2b256-9ft3x`,
	`due = "2026-08-01"`,
}

// TestConformanceVectors parses every RFC 0014 example and requires a
// non-nil AST — the corpus-level conformance gate the task asked for.
func TestConformanceVectors(t *testing.T) {
	for _, src := range conformanceVectors {
		t.Run(src, func(t *testing.T) {
			q, err := Parse(src)
			if err != nil {
				t.Fatalf("Parse(%q) = _, %v; want a parse", src, err)
			}
			if q == nil {
				t.Fatalf("Parse(%q) = nil, nil; want a non-nil *Query", src)
			}
			if len(q.Path.Steps) == 0 {
				t.Fatalf("Parse(%q): Path has zero Steps", src)
			}
		})
	}
}

// TestConformanceVectors_KnownGap_LeadingCombinator documents a confirmed
// gap between RFC 0014's conformance-vector corpus and its own normative
// grammar (docs/rfcs/0014-trellis.peg): `Path <- Step (SP Combinator SP
// Step)*` requires a non-empty leading Step, so these two vectors (default-
// anchor traversal, FDR 0022 "Roots as nodes") do NOT parse under the
// CURRENT grammar — confirmed both by this package's parser (below) and
// independently by feeding the raw strings to langlang's compiled grammar
// (`go run ./cmd/langlang -grammar ... -input ...`), which fails at the
// same point. This is a semantic gap (what the grammar matches), not a
// parser bug, so it is not silently special-cased here: fixing it means
// changing Path's production (e.g. an optional leading Step), a normative
// call for RFC 0014's owner. Tracked at cutting-garden#152. If that issue
// lands a grammar fix, this test (and the file's leading comment) should be
// updated together with the parser.
func TestConformanceVectors_KnownGap_LeadingCombinator(t *testing.T) {
	for _, src := range []string{
		`->> !task ^done`,
		`-[blocks]->> !task ^done`,
	} {
		t.Run(src, func(t *testing.T) {
			if _, err := Parse(src); err == nil {
				t.Fatalf("Parse(%q) succeeded; cutting-garden#152 expects this to still fail "+
					"under the current grammar — if it now parses, the grammar gap was fixed "+
					"and this test (and its comment) need updating, not deleting silently", src)
			}
		})
	}
}

// ---- shape assertions -----------------------------------------------------

func mustParse(t *testing.T, src string) *Query {
	t.Helper()
	q, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	return q
}

func TestShape_MultiStepPathWithCombinator(t *testing.T) {
	q := mustParse(t, `caldav:fastmail -> component=VEVENT dtstart^="20260718"`)

	if got, want := len(q.Path.Steps), 2; got != want {
		t.Fatalf("len(Steps) = %d, want %d", got, want)
	}
	if got, want := len(q.Path.Combinators), 1; got != want {
		t.Fatalf("len(Combinators) = %d, want %d", got, want)
	}
	if got, want := q.Path.Combinators[0].Kind, CombinatorFwd; got != want {
		t.Fatalf("Combinators[0].Kind = %v, want %v", got, want)
	}

	step0 := q.Path.Steps[0]
	if len(step0.Terms) != 1 {
		t.Fatalf("Steps[0]: len(Terms) = %d, want 1", len(step0.Terms))
	}
	ident, ok := step0.Terms[0].Basic.(IdentBasicTerm)
	if !ok {
		t.Fatalf("Steps[0].Terms[0].Basic = %T, want IdentBasicTerm", step0.Terms[0].Basic)
	}
	if got, want := ident.Ident.Name, "caldav:fastmail"; got != want {
		t.Fatalf("Steps[0] ident = %q, want %q (strict sigil rule: ':' is interior here)", got, want)
	}
	if ident.Sigil != nil {
		t.Fatalf("Steps[0] ident.Sigil = %+v, want nil (no trailing sigil left over)", ident.Sigil)
	}

	step1 := q.Path.Steps[1]
	if len(step1.Terms) != 2 {
		t.Fatalf("Steps[1]: len(Terms) = %d, want 2", len(step1.Terms))
	}
	fp, ok := step1.Terms[1].Basic.(FieldPredBasicTerm)
	if !ok {
		t.Fatalf("Steps[1].Terms[1].Basic = %T, want FieldPredBasicTerm", step1.Terms[1].Basic)
	}
	if got, want := fp.FieldPred.Field.Name, "dtstart"; got != want {
		t.Fatalf("dtstart field name = %q, want %q", got, want)
	}
	if got, want := fp.FieldPred.Op, FieldOpPrefix; got != want {
		t.Fatalf("dtstart op = %v, want %v (^=)", got, want)
	}
}

func TestShape_SubPathPredicate(t *testing.T) {
	q := mustParse(t, `!newsblur-story-v1 year=2026 [-> _body*=["zettelkasten", "localfirst", "git", "obsidian", "roam research"]]`)

	if len(q.Path.Steps) != 1 {
		t.Fatalf("len(Steps) = %d, want 1 (a subpath predicate is not a top-level step)", len(q.Path.Steps))
	}
	step := q.Path.Steps[0]
	if len(step.Terms) != 3 {
		t.Fatalf("len(Terms) = %d, want 3 (type, field pred, subpath group)", len(step.Terms))
	}

	group, ok := step.Terms[2].Basic.(GroupBasicTerm)
	if !ok {
		t.Fatalf("Terms[2].Basic = %T, want GroupBasicTerm", step.Terms[2].Basic)
	}
	sub, ok := group.Group.Body.(SubPath)
	if !ok {
		t.Fatalf("Group.Body = %T, want SubPath", group.Group.Body)
	}
	if got, want := sub.Combinator.Kind, CombinatorFwd; got != want {
		t.Fatalf("SubPath.Combinator.Kind = %v, want %v", got, want)
	}
	if sub.Path == nil {
		t.Fatalf("SubPath.Path = nil, want a non-nil nested path")
	}
	if len(sub.Path.Steps) != 1 || len(sub.Path.Steps[0].Terms) != 1 {
		t.Fatalf("SubPath.Path shape = %+v, want exactly one step with one term", sub.Path)
	}
	innerFP, ok := sub.Path.Steps[0].Terms[0].Basic.(FieldPredBasicTerm)
	if !ok {
		t.Fatalf("SubPath's inner term = %T, want FieldPredBasicTerm", sub.Path.Steps[0].Terms[0].Basic)
	}
	if !innerFP.FieldPred.List {
		t.Fatalf("_body predicate: List = false, want true (bracketed value list)")
	}
	if got, want := len(innerFP.FieldPred.Values), 5; got != want {
		t.Fatalf("_body predicate: len(Values) = %d, want %d", got, want)
	}
}

func TestShape_VersionSubPath(t *testing.T) {
	q := mustParse(t, `!forgejo-issue-v1 state=open [+ state=closed]`)

	step := q.Path.Steps[0]
	if len(step.Terms) != 3 {
		t.Fatalf("len(Terms) = %d, want 3", len(step.Terms))
	}
	group, ok := step.Terms[2].Basic.(GroupBasicTerm)
	if !ok {
		t.Fatalf("Terms[2].Basic = %T, want GroupBasicTerm", step.Terms[2].Basic)
	}
	vs, ok := group.Group.Body.(VersionSub)
	if !ok {
		t.Fatalf("Group.Body = %T, want VersionSub", group.Group.Body)
	}
	if got, want := vs.Sigil.Runes, "+"; got != want {
		t.Fatalf("VersionSub.Sigil = %q, want %q", got, want)
	}
	if vs.Step == nil {
		t.Fatalf("VersionSub.Step = nil, want a non-nil step (state=closed)")
	}
	if len(vs.Step.Terms) != 1 {
		t.Fatalf("VersionSub.Step: len(Terms) = %d, want 1", len(vs.Step.Terms))
	}
	fp, ok := vs.Step.Terms[0].Basic.(FieldPredBasicTerm)
	if !ok {
		t.Fatalf("VersionSub.Step.Terms[0].Basic = %T, want FieldPredBasicTerm", vs.Step.Terms[0].Basic)
	}
	if got, want := fp.FieldPred.Field.Name, "state"; got != want {
		t.Fatalf("field = %q, want %q", got, want)
	}
}

func TestShape_EmptyVersionSubPath(t *testing.T) {
	q := mustParse(t, `!web-page-v1 [+]`)
	step := q.Path.Steps[0]
	group := step.Terms[1].Basic.(GroupBasicTerm)
	vs, ok := group.Group.Body.(VersionSub)
	if !ok {
		t.Fatalf("Group.Body = %T, want VersionSub", group.Group.Body)
	}
	if vs.Step != nil {
		t.Fatalf("VersionSub.Step = %+v, want nil (the empty `[+]` form)", vs.Step)
	}
}

func TestShape_ValueList(t *testing.T) {
	q := mustParse(t, `!caldav-object-v1 component=VEVENT dtstart^=["20260718", "20260719"]`)
	step := q.Path.Steps[0]
	fp := step.Terms[2].Basic.(FieldPredBasicTerm).FieldPred
	if got, want := fp.Field.Name, "dtstart"; got != want {
		t.Fatalf("field = %q, want %q", got, want)
	}
	if !fp.List {
		t.Fatalf("List = false, want true")
	}
	if got, want := len(fp.Values), 2; got != want {
		t.Fatalf("len(Values) = %d, want %d", got, want)
	}
	for i, want := range []string{"20260718", "20260719"} {
		sv, ok := fp.Values[i].(StringValue)
		if !ok {
			t.Fatalf("Values[%d] = %T, want StringValue", i, fp.Values[i])
		}
		if sv.Value != want {
			t.Fatalf("Values[%d] = %q, want %q", i, sv.Value, want)
		}
	}
}

func TestShape_MarklTermQuotedPurpose(t *testing.T) {
	q := mustParse(t, `"my thing"@blake2b256-9ft3x`)
	term := q.Path.Steps[0].Terms[0]
	mb, ok := term.Basic.(MarklBasicTerm)
	if !ok {
		t.Fatalf("Basic = %T, want MarklBasicTerm", term.Basic)
	}
	if !mb.Markl.PurposeQuoted {
		t.Fatalf("PurposeQuoted = false, want true")
	}
	if got, want := mb.Markl.Purpose, "my thing"; got != want {
		t.Fatalf("Purpose = %q, want %q (decoded, unquoted)", got, want)
	}
	if got, want := mb.Markl.Digest, "blake2b256-9ft3x"; got != want {
		t.Fatalf("Digest = %q, want %q", got, want)
	}
}

func TestShape_SpacedFieldPredicate(t *testing.T) {
	q := mustParse(t, `due = "2026-08-01"`)
	if len(q.Path.Steps) != 1 || len(q.Path.Steps[0].Terms) != 1 {
		t.Fatalf("shape = %+v, want exactly one step with one term "+
			"(spaced '=' is ONE greedy field predicate, never `due` + a bare exact-match term)", q.Path)
	}
	fp, ok := q.Path.Steps[0].Terms[0].Basic.(FieldPredBasicTerm)
	if !ok {
		t.Fatalf("Basic = %T, want FieldPredBasicTerm", q.Path.Steps[0].Terms[0].Basic)
	}
	if got, want := fp.FieldPred.Field.Name, "due"; got != want {
		t.Fatalf("Field.Name = %q, want %q", got, want)
	}
	if got, want := fp.FieldPred.Op, FieldOpEq; got != want {
		t.Fatalf("Op = %v, want %v", got, want)
	}
	sv, ok := fp.FieldPred.Values[0].(StringValue)
	if !ok {
		t.Fatalf("Values[0] = %T, want StringValue", fp.FieldPred.Values[0])
	}
	if got, want := sv.Value, "2026-08-01"; got != want {
		t.Fatalf("value = %q, want %q", got, want)
	}
}

// TestShape_StrictSigilRule covers the strict-sigil-rule pair the task
// asked for: `todo:` (identifier + trailing latest sigil) vs
// `caldav:fastmail` (one identifier, ':' is interior because more
// identifier content follows it).
func TestShape_StrictSigilRule(t *testing.T) {
	t.Run("todo: is identifier plus sigil", func(t *testing.T) {
		q := mustParse(t, `todo:`)
		term := q.Path.Steps[0].Terms[0]
		ib, ok := term.Basic.(IdentBasicTerm)
		if !ok {
			t.Fatalf("Basic = %T, want IdentBasicTerm", term.Basic)
		}
		if got, want := ib.Ident.Name, "todo"; got != want {
			t.Fatalf("Ident.Name = %q, want %q", got, want)
		}
		if ib.Sigil == nil {
			t.Fatalf("Sigil = nil, want a trailing latest sigil (\":\")")
		}
		if got, want := ib.Sigil.Runes, ":"; got != want {
			t.Fatalf("Sigil.Runes = %q, want %q", got, want)
		}
	})

	t.Run("caldav:fastmail is one identifier", func(t *testing.T) {
		q := mustParse(t, `caldav:fastmail`)
		term := q.Path.Steps[0].Terms[0]
		ib, ok := term.Basic.(IdentBasicTerm)
		if !ok {
			t.Fatalf("Basic = %T, want IdentBasicTerm", term.Basic)
		}
		if got, want := ib.Ident.Name, "caldav:fastmail"; got != want {
			t.Fatalf("Ident.Name = %q, want %q (':' must be interior, not a sigil)", got, want)
		}
		if ib.Sigil != nil {
			t.Fatalf("Sigil = %+v, want nil (nothing left over to be a sigil)", ib.Sigil)
		}
	})
}

// TestShape_ReservedFormsParse checks that the two grammar-reserved forms
// parse successfully (into dedicated AST values a later validation layer
// can reject), rather than erroring in this package.
func TestShape_ReservedFormsParse(t *testing.T) {
	t.Run("~= field operator", func(t *testing.T) {
		q := mustParse(t, `_body~="pattern"`)
		fp := q.Path.Steps[0].Terms[0].Basic.(FieldPredBasicTerm).FieldPred
		if got, want := fp.Op, FieldOpRegex; got != want {
			t.Fatalf("Op = %v, want %v (reserved, not an error)", got, want)
		}
	})

	t.Run("-[pred]->> typed transitive closure", func(t *testing.T) {
		q := mustParse(t, `!task -[blocks]->> !task ^done`)
		if len(q.Path.Combinators) != 1 {
			t.Fatalf("len(Combinators) = %d, want 1", len(q.Path.Combinators))
		}
		comb := q.Path.Combinators[0]
		if got, want := comb.Kind, CombinatorTypedClosure; got != want {
			t.Fatalf("Kind = %v, want %v (reserved, not an error)", got, want)
		}
		if comb.Pred == nil || len(comb.Pred.Terms) != 1 {
			t.Fatalf("Pred = %+v, want one term (blocks)", comb.Pred)
		}
	})
}

// ---- negative cases ---------------------------------------------------------

func TestParse_MalformedInputs(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"unbalanced brackets (missing close)", `!task priority-1_should [due<="2026-08-01"`},
		{"unbalanced brackets (missing open)", `!task priority-1_should due<="2026-08-01"]`},
		// NOTE: `a->b` (no whitespace, bare identifier on the left) is NOT
		// a good malformed-input example — it's a legitimate, unambiguous
		// FieldPred parse: identifier "a-" (a valid tag-like ident: '-' is
		// unconditionally identifier content, per dependent-tag syntax),
		// FieldOp ">" (single-char, no whitespace required), Bareword
		// value "b". Confirmed against langlang's compiled grammar, not
		// just this parser. `!task->!done` has no such escape hatch
		// (TypeTerm's leading '!' bars FieldPred's FieldName from ever
		// matching), so the missing mandatory whitespace around the
		// combinator genuinely strands "->!done" as trailing garbage.
		{"combinator without mandatory whitespace", `!task->!done`},
		{"term-final @ (dangling MarklTerm digest slot)", `done@`},
		{"unterminated string", `"unterminated`},
		{"empty group", `[]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, err := Parse(tc.src)
			if err == nil {
				t.Fatalf("Parse(%q) = %+v, nil; want a syntax error", tc.src, q)
			}
			if _, isSyntaxErr := err.(*SyntaxError); !isSyntaxErr {
				t.Fatalf("Parse(%q) error = %v (%T), want *SyntaxError", tc.src, err, err)
			}
		})
	}
}

// TestParse_TopLevelRejectsTrailingGarbage exercises Query's SP? EOF tail:
// a syntactically valid query followed by unparseable trailing content must
// fail the whole parse, not silently ignore the tail.
func TestParse_TopLevelRejectsTrailingGarbage(t *testing.T) {
	_, err := Parse(`!task ]`)
	if err == nil {
		t.Fatalf("Parse(%q) succeeded; want a trailing-input syntax error", `!task ]`)
	}
	if !strings.Contains(err.Error(), "trellis: syntax error") {
		t.Fatalf("error = %v, want a trellis syntax error", err)
	}
}
