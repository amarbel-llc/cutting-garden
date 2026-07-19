package trellis

import (
	"os"
	"strings"
	"testing"
)

// conformanceVectorsPegPath is the normative grammar file whose trailing
// "// ---- conformance vectors" comment block is the SINGLE SOURCE of the
// RFC 0014 example corpus. The tests below extract the vectors from it
// directly (rather than transcribing them into a slice here) so that a
// vector added to the .peg cannot silently escape the "each MUST parse"
// gate — the well-formed-only hole that let cutting-garden#152 (the leading-
// combinator forms `->> !task ^done` / `-[blocks]->> !task ^done`) slip a
// green langlang grammar check while remaining unparseable under the
// grammar's own Path production. The path is relative to this package's
// directory (Go runs tests with CWD = the package dir).
const conformanceVectorsPegPath = "../../docs/rfcs/0014-trellis.peg"

// loadConformanceVectors reads the normative grammar and returns every query
// string from its trailing conformance-vector comment block, in file order.
// The block's format (docs/rfcs/0014-trellis.peg):
//
//   - A vector STARTS on a comment line indented exactly three spaces past
//     the `//` ("//   query…"). Everything up to an inline em/en-dash (—/–,
//     the prose-annotation marker) is query text.
//   - A more-indented comment line ("//     …") CONTINUES the current
//     vector's query — but only until the query is "closed" by a dash
//     (inline on the start line, or leading a continuation line). Once
//     closed, further indented lines are multi-line prose and are skipped.
//   - The block header rule and blank comment lines are skipped.
//
// Every returned vector is a query the RFC asserts MUST parse; a broken
// extractor that leaked prose text would be caught by TestConformanceVectors
// below (prose does not parse as trellis).
func loadConformanceVectors(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(conformanceVectorsPegPath)
	if err != nil {
		t.Fatalf("read normative grammar %q: %v", conformanceVectorsPegPath, err)
	}

	var (
		vectors   []string
		cur       strings.Builder
		haveCur   bool
		curClosed bool
		inBlock   bool
	)
	flush := func() {
		if haveCur {
			if q := strings.TrimSpace(cur.String()); q != "" {
				vectors = append(vectors, q)
			}
		}
		cur.Reset()
		haveCur = false
		curClosed = false
	}

	for _, line := range strings.Split(string(raw), "\n") {
		if !inBlock {
			// The block begins at its header comment; the top-of-file doc
			// comment (which mentions no "conformance vectors") is skipped.
			if strings.HasPrefix(strings.TrimSpace(line), "//") &&
				strings.Contains(line, "conformance vectors") {
				inBlock = true
			}
			continue
		}

		trimmedLeft := strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(trimmedLeft, "//") {
			break // a non-comment line ends the block
		}
		body := strings.TrimPrefix(trimmedLeft, "//")
		indent := len(body) - len(strings.TrimLeft(body, " "))
		content := strings.TrimLeft(body, " ")
		if content == "" {
			continue // blank comment separator
		}
		if indent <= 2 {
			continue // the header rule / any shallow line: not a vector
		}

		// Drop an inline prose annotation (everything from the first dash).
		queryPart := content
		hasDash := false
		if idx := firstDashIndex(content); idx >= 0 {
			queryPart = content[:idx]
			hasDash = true
		}
		queryPart = strings.TrimRight(queryPart, " ")

		if indent == 3 { // a vector start
			flush()
			haveCur = true
			cur.WriteString(queryPart)
			curClosed = hasDash
			continue
		}

		// indent >= 4: a continuation or a prose line for the current
		// vector. Prose lines (query already closed) are skipped.
		if !haveCur || curClosed {
			continue
		}
		if queryPart != "" {
			if cur.Len() > 0 {
				cur.WriteByte(' ')
			}
			cur.WriteString(queryPart)
		}
		if hasDash {
			curClosed = true
		}
	}
	flush()
	return vectors
}

// firstDashIndex returns the byte index of the first em-dash (U+2014) or
// en-dash (U+2013) in s, or -1 — the prose-annotation marker separating a
// conformance vector's query text from its commentary.
func firstDashIndex(s string) int {
	em := strings.IndexRune(s, '—')
	en := strings.IndexRune(s, '–')
	switch {
	case em < 0:
		return en
	case en < 0:
		return em
	case em < en:
		return em
	default:
		return en
	}
}

// TestConformanceVectors extracts every RFC 0014 example straight from the
// normative grammar's conformance-vector block and requires each to parse
// into a non-nil AST — the corpus-level conformance gate, sourced from the
// .peg itself so no example can drift out of coverage (cutting-garden#152).
func TestConformanceVectors(t *testing.T) {
	vectors := loadConformanceVectors(t)

	// Guard against a silently-broken extractor: the corpus is ~two dozen
	// vectors, so an empty/tiny slice means extraction failed, not that the
	// grammar shrank.
	if len(vectors) < 20 {
		t.Fatalf("extracted only %d conformance vectors from %s; expected the full corpus "+
			"(~two dozen) — extraction likely broke", len(vectors), conformanceVectorsPegPath)
	}

	// The leading-combinator forms (FDR 0022 "roots as nodes";
	// cutting-garden#152) MUST be among the extracted corpus — they are the
	// vectors the well-formed-only gate used to miss.
	for _, want := range []string{`->> !task ^done`, `-[blocks]->> !task ^done`} {
		if !containsVector(vectors, want) {
			t.Errorf("conformance corpus is missing %q; extraction or the .peg block changed", want)
		}
	}

	for _, src := range vectors {
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

func containsVector(vectors []string, want string) bool {
	for _, v := range vectors {
		if v == want {
			return true
		}
	}
	return false
}

// TestLeadingCombinatorVectors asserts the two default-anchor traversal
// vectors — the forms cutting-garden#152 taught the grammar's Path
// production to accept via its `(Combinator SP)?` prefix (FDR 0022 "roots as
// nodes") — now PARSE, and that the parser records the leading combinator as
// Path.Leading (an explicit default-anchor origin) rather than as an
// interior between-steps join.
//
// The typed-closure form (`-[blocks]->>`) remains validation-RESERVED, but
// parsing and validation are separate layers: this asserts only that it
// PARSES (into a CombinatorTypedClosure), exactly as trellis.peg's vector
// comment promises ("parses; validation rejects (reserved)"). This replaces
// the former TestConformanceVectors_KnownGap_LeadingCombinator, which
// documented the pre-#152 gap by asserting these vectors did NOT parse.
func TestLeadingCombinatorVectors(t *testing.T) {
	cases := []struct {
		src      string
		wantKind CombinatorKind
	}{
		{`->> !task ^done`, CombinatorFwdClosure},
		{`-[blocks]->> !task ^done`, CombinatorTypedClosure},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			q := mustParse(t, tc.src)

			if q.Path.Leading == nil {
				t.Fatalf("Path.Leading = nil, want a leading %v combinator (default-anchor origin)", tc.wantKind)
			}
			if got := q.Path.Leading.Kind; got != tc.wantKind {
				t.Fatalf("Path.Leading.Kind = %v, want %v", got, tc.wantKind)
			}

			// The leading combinator is NOT an interior join: the explicit
			// `!task ^done` is the single Step, with no between-steps
			// Combinators.
			if len(q.Path.Steps) != 1 {
				t.Fatalf("len(Steps) = %d, want 1 (`!task ^done`)", len(q.Path.Steps))
			}
			if len(q.Path.Combinators) != 0 {
				t.Fatalf("len(Combinators) = %d, want 0 (leading combinator lives in Path.Leading, "+
					"not the interior joins)", len(q.Path.Combinators))
			}
			if got := len(q.Path.Steps[0].Terms); got != 2 {
				t.Fatalf("Steps[0]: len(Terms) = %d, want 2 (`!task`, `^done`)", got)
			}
		})
	}

	// The typed-closure leading combinator carries its edge predicate.
	q := mustParse(t, `-[blocks]->> !task ^done`)
	pred := q.Path.Leading.Pred
	if pred == nil || len(pred.Terms) != 1 {
		t.Fatalf("leading typed-closure Pred = %+v, want exactly one term (`blocks`)", pred)
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
	if q.Path.Leading != nil {
		t.Fatalf("Path.Leading = %+v, want nil (this path starts at an explicit step)", q.Path.Leading)
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
		// A leading combinator with NO following step is invalid: the
		// grammar's `(Combinator SP)? Step` still requires the Step
		// (cutting-garden#152 admits a leading combinator, not a bare one).
		{"leading combinator with no step", `->>`},
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
