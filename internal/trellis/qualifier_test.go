package trellis

import (
	"errors"
	"strings"
	"testing"
)

// The `(…)` qualifier term (native tags design G10, slice 1 task 2):
// `Qualifier <- '(' Ident ')'`, admitted as a FieldPred Value (`k=(x)`, a
// value hole with a meta qualifier) and as a BasicTerm on its own (`(x)`).
// Parens are Reserved runes (hyphence master 0ac8742), so they split
// identifiers: a bare `c(1)` is no longer one Ident.

func TestShape_QualifierValue(t *testing.T) {
	q := mustParse(t, `date_due=(month)`)
	if got := len(q.Path.Steps[0].Terms); got != 1 {
		t.Fatalf("len(Terms) = %d, want 1 (one field predicate)", got)
	}
	fp, ok := q.Path.Steps[0].Terms[0].Basic.(FieldPredBasicTerm)
	if !ok {
		t.Fatalf("Basic = %T, want FieldPredBasicTerm", q.Path.Steps[0].Terms[0].Basic)
	}
	if fp.FieldPred.Field.Name != "date_due" || fp.FieldPred.Op != FieldOpEq {
		t.Fatalf("FieldPred = %+v, want date_due =", fp.FieldPred)
	}
	if fp.FieldPred.List || len(fp.FieldPred.Values) != 1 {
		t.Fatalf("Values = %+v (List=%v), want one bare value", fp.FieldPred.Values, fp.FieldPred.List)
	}
	qv, ok := fp.FieldPred.Values[0].(QualifierValue)
	if !ok {
		t.Fatalf("Values[0] = %T, want QualifierValue", fp.FieldPred.Values[0])
	}
	if qv.Qualifier.Name != "month" {
		t.Fatalf("Qualifier.Name = %q, want %q", qv.Qualifier.Name, "month")
	}

	// Writer round-trip: the value renders back as `(month)`, so a writer
	// emits `date_due=(month)`; that spelling re-parses to the same AST.
	spelled := fp.FieldPred.Field.Name + fp.FieldPred.Op.String() + qv.String()
	if spelled != `date_due=(month)` {
		t.Fatalf("round-trip spelling = %q, want %q", spelled, `date_due=(month)`)
	}
	again := mustParse(t, spelled)
	if got := again.Path.Steps[0].Terms[0].Basic.(FieldPredBasicTerm).FieldPred.Values[0]; got != qv {
		t.Fatalf("re-parse Values[0] = %+v, want %+v", got, qv)
	}
}

func TestShape_QualifierInValueList(t *testing.T) {
	q := mustParse(t, `k=[(a), b]`)
	fp := q.Path.Steps[0].Terms[0].Basic.(FieldPredBasicTerm).FieldPred
	if !fp.List || len(fp.Values) != 2 {
		t.Fatalf("Values = %+v (List=%v), want a two-element list", fp.Values, fp.List)
	}
	if _, ok := fp.Values[0].(QualifierValue); !ok {
		t.Fatalf("Values[0] = %T, want QualifierValue", fp.Values[0])
	}
	if _, ok := fp.Values[1].(Bareword); !ok {
		t.Fatalf("Values[1] = %T, want Bareword", fp.Values[1])
	}
}

func TestShape_QualifierBasicTerm(t *testing.T) {
	q := mustParse(t, `(tags)`)
	if got := len(q.Path.Steps[0].Terms); got != 1 {
		t.Fatalf("len(Terms) = %d, want 1", got)
	}
	term := q.Path.Steps[0].Terms[0]
	qt, ok := term.Basic.(QualifierBasicTerm)
	if !ok {
		t.Fatalf("Basic = %T, want QualifierBasicTerm", term.Basic)
	}
	if qt.Qualifier.Name != "tags" {
		t.Fatalf("Qualifier.Name = %q, want %q", qt.Qualifier.Name, "tags")
	}
	if got := qt.String(); got != `(tags)` {
		t.Fatalf("String() = %q, want %q", got, `(tags)`)
	}
	if again := mustParse(t, qt.String()); again.Path.Steps[0].Terms[0].Basic != qt {
		t.Fatalf("re-parse of %q = %+v, want %+v", qt.String(), again.Path.Steps[0].Terms[0].Basic, qt)
	}

	// Alongside other terms in a step, and negatable like any BasicTerm.
	q = mustParse(t, `!task ^(tags)`)
	if got := len(q.Path.Steps[0].Terms); got != 2 {
		t.Fatalf("len(Terms) = %d, want 2", got)
	}
	if neg := q.Path.Steps[0].Terms[1]; !neg.Negate {
		t.Fatalf("Terms[1] = %+v, want Negate=true", neg)
	}
}

// TestShape_ParensSplitIdentifiers pins how a bare `c(1)` behaves now that
// parens are Reserved: it is NOT one identifier. The parser reads Ident `c`,
// then hits `(1)` with no separating whitespace (Step demands SP between
// terms), so the whole parse fails LOUDLY: a syntax error at offset 1 (the
// `(`) whose farthest-failure diagnostic is "expected whitespace" — never a
// silent one-token or two-token mis-parse. The quoted spelling `"c(1)"` is
// the opaque-reference escape hatch for that content.
func TestShape_ParensSplitIdentifiers(t *testing.T) {
	_, err := Parse(`c(1)`)
	if err == nil {
		t.Fatalf("Parse(%q) succeeded; want a syntax error (parens are reserved)", `c(1)`)
	}
	var se *SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("error = %v (%T), want *SyntaxError", err, err)
	}
	if se.Offset != 1 {
		t.Fatalf("SyntaxError.Offset = %d, want 1 (the `(` right after Ident `c`)", se.Offset)
	}
	if !strings.Contains(err.Error(), "expected whitespace") {
		t.Fatalf("error = %v, want the missing-term-separator diagnostic", err)
	}

	// Spaced, the same runes are two terms: identifier `c` + qualifier `(1)`.
	q := mustParse(t, `c (1)`)
	if got := len(q.Path.Steps[0].Terms); got != 2 {
		t.Fatalf("Parse(%q): len(Terms) = %d, want 2 (Ident, Qualifier)", `c (1)`, got)
	}
	if _, ok := q.Path.Steps[0].Terms[1].Basic.(QualifierBasicTerm); !ok {
		t.Fatalf("Terms[1] = %T, want QualifierBasicTerm", q.Path.Steps[0].Terms[1].Basic)
	}

	// Quoted, the parens are opaque content of a single QuotedRef.
	q = mustParse(t, `"c(1)"`)
	if got := len(q.Path.Steps[0].Terms); got != 1 {
		t.Fatalf("Parse(%q): len(Terms) = %d, want 1", `"c(1)"`, got)
	}
	ref, ok := q.Path.Steps[0].Terms[0].Basic.(QuotedRefBasicTerm)
	if !ok {
		t.Fatalf("Basic = %T, want QuotedRefBasicTerm", q.Path.Steps[0].Terms[0].Basic)
	}
	if ref.Ref.Value != "c(1)" {
		t.Fatalf("QuotedRef.Value = %q, want %q", ref.Ref.Value, "c(1)")
	}
}

func TestParse_MalformedQualifiers(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"empty qualifier", `()`},
		{"unterminated qualifier", `(tags`},
		{"qualifier with interior whitespace", `(a b)`},
		{"nested qualifier", `((a))`},
		{"empty qualifier value", `k=()`},
		{"bare close paren", `)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, err := Parse(tc.src)
			if err == nil {
				t.Fatalf("Parse(%q) = %+v, nil; want a syntax error", tc.src, q)
			}
			var se *SyntaxError
			if !errors.As(err, &se) {
				t.Fatalf("Parse(%q) error = %v (%T), want *SyntaxError", tc.src, err, err)
			}
		})
	}
}
