package trellis

import (
	stderrors "errors"
	"reflect"
	"strings"
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The ground box literal (native tags design G9/G13): ParseLiteral projects a
// trellis Group to {id, type, tags, atoms}; WriteLiteral spells it back through
// the one quoting rule (QuoteIfNeeded). Every parse case below is also a
// write→re-parse round trip.

func TestLiteral_RoundTrip(t *testing.T) {
	cases := []struct {
		name     string
		interior string
		want     Literal
		// spelled is WriteLiteral's output when it differs from interior (the
		// canonical single-space spelling); empty means identical.
		spelled string
	}{
		{
			name:     "id only",
			interior: `task1.ics`,
			want:     Literal{ID: "task1.ics"},
		},
		{
			name:     "id and type",
			interior: `task1.ics !caldav-object-v1`,
			want:     Literal{ID: "task1.ics", Type: "caldav-object-v1"},
		},
		{
			name:     "id and atoms (organize's field boxes)",
			interior: `field1.ics location=Bank status=NEEDS-ACTION priority=1`,
			want: Literal{ID: "field1.ics", Atoms: []Atom{
				{"location", "Bank"}, {"status", "NEEDS-ACTION"}, {"priority", "1"},
			}},
		},
		{
			name:     "type then atoms",
			interior: `dentist.ics !caldav-object-vevent-v1 date_start=2026-08-15 time_start=09-30 location=HQ`,
			want: Literal{ID: "dentist.ics", Type: "caldav-object-vevent-v1", Atoms: []Atom{
				{"date_start", "2026-08-15"}, {"time_start", "09-30"}, {"location", "HQ"},
			}},
		},
		{
			name:     "bare tokens are tags, even one named like a field (G9)",
			interior: `field2.ics work-x status location=Bank`,
			want: Literal{
				ID: "field2.ics", Tags: []string{"work-x", "status"},
				Atoms: []Atom{{"location", "Bank"}},
			},
		},
		{
			name:     "quoted tag decodes and re-quotes (G9)",
			interior: `t3.ics "_ inbox" work`,
			want:     Literal{ID: "t3.ics", Tags: []string{"_ inbox", "work"}},
		},
		{
			name:     "quoted id and quoted atom value",
			interior: `"one/uno.zettel?x=1" location="New York"`,
			want: Literal{
				ID:    "one/uno.zettel?x=1",
				Atoms: []Atom{{"location", "New York"}},
			},
		},
		{
			name:     "escapes decode and re-encode",
			interior: `t.ics "a\nb" note="say \"hi\""`,
			want: Literal{
				ID: "t.ics", Tags: []string{"a\nb"},
				Atoms: []Atom{{"note", `say "hi"`}},
			},
		},
		{
			name:     "extra whitespace collapses on write",
			interior: `  task1.ics   !x   tag   k=v  `,
			want:     Literal{ID: "task1.ics", Type: "x", Tags: []string{"tag"}, Atoms: []Atom{{"k", "v"}}},
			spelled:  `task1.ics !x tag k=v`,
		},
		{
			name:     "identifier-interior sigils stay bare",
			interior: `caldav:https://host/cal/x.ics k=12.7`,
			want:     Literal{ID: "caldav:https://host/cal/x.ics", Atoms: []Atom{{"k", "12.7"}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseLiteral(tc.interior)
			if err != nil {
				t.Fatalf("ParseLiteral(%q): %v", tc.interior, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseLiteral(%q) = %+v, want %+v", tc.interior, got, tc.want)
			}
			var b strings.Builder
			WriteLiteral(&b, got)
			wantSpelled := tc.spelled
			if wantSpelled == "" {
				wantSpelled = tc.interior
			}
			if b.String() != wantSpelled {
				t.Fatalf("WriteLiteral = %q, want %q", b.String(), wantSpelled)
			}
			again, err := ParseLiteral(b.String())
			if err != nil {
				t.Fatalf("re-parse %q: %v", b.String(), err)
			}
			if !reflect.DeepEqual(again, got) {
				t.Fatalf("re-parse = %+v, want %+v", again, got)
			}
		})
	}
}

// TestLiteral_NotGround pins the groundness bar: every non-ground form is a
// LOUD bad request naming the offending term (never a silent drop, which the
// hand parser this replaced did for unrecognized tokens).
func TestLiteral_NotGround(t *testing.T) {
	cases := []struct {
		interior string
		wantTerm string // substring the message must carry (the offending term)
	}{
		{`field1.ics status*=y`, "status*=y"},
		{`field1.ics status!=y`, "status!=y"},
		{`x.ics k=[a, b]`, "k=[a, b]"},
		{`x.ics k=(month)`, "k=(month)"},
		{`x.ics (tags)`, "(tags)"},
		{`x.ics ^work`, "^work"},
		{`x.ics =work`, "=work"},
		{`x.ics @blake2b256-9ft3x`, "@blake2b256-9ft3x"},
		{`x.ics todo:`, "todo:"},
		{`x.ics !a !b`, "!b"},
		{`x.ics !a:`, "!a:"},
		{`x.ics [-> y]`, "[…]"},
		{`x.ics k=@blake2b256-9ft3x`, "k=@blake2b256-9ft3x"},
		// A non-id first term.
		{`!type x.ics`, "!type"},
		{`k=v x.ics`, "k=v"},
	}
	for _, tc := range cases {
		t.Run(tc.interior, func(t *testing.T) {
			_, err := ParseLiteral(tc.interior)
			if err == nil {
				t.Fatalf("ParseLiteral(%q) succeeded; want a bad request", tc.interior)
			}
			if !errors.Is400BadRequest(err) {
				t.Fatalf("ParseLiteral(%q) error = %v; want a bad request", tc.interior, err)
			}
			if !strings.Contains(err.Error(), "not ground") {
				t.Fatalf("error = %v; want a groundness diagnostic", err)
			}
			if !strings.Contains(err.Error(), tc.wantTerm) {
				t.Fatalf("error = %v; want it to name %q", err, tc.wantTerm)
			}
		})
	}

	// Group-level rejections (no single term to name).
	for _, interior := range []string{``, `a, b`, `-> x`, `+ x`, `x.ics k=`} {
		_, err := ParseLiteral(interior)
		if err == nil || !errors.Is400BadRequest(err) {
			t.Fatalf("ParseLiteral(%q) = %v; want a bad request", interior, err)
		}
	}
}

func TestQuoteIfNeeded(t *testing.T) {
	cases := []struct{ in, want string }{
		{"work", "work"},
		{"-client", "-client"},
		{"task1.ics", "task1.ics"},
		{"caldav:fastmail", "caldav:fastmail"},
		{"2026-08-15", "2026-08-15"},
		{"", `""`},
		{"_ inbox", `"_ inbox"`},
		{"a\nb", `"a\nb"`},
		{"todo:", `"todo:"`},
		{"c(1)", `"c(1)"`},
		{"k=v", `"k=v"`},
		{`say "hi"`, `"say \"hi\""`},
		// A backslash is identifier content (not Reserved), so it stays bare;
		// only inside a String is it an escape.
		{`back\slash`, `back\slash`},
		{`a "b\c`, `"a \"b\\c"`},
		{"!type", `"!type"`},
	}
	for _, tc := range cases {
		if got := QuoteIfNeeded(tc.in); got != tc.want {
			t.Errorf("QuoteIfNeeded(%q) = %s, want %s", tc.in, got, tc.want)
		}
		// Whatever the spelling, it reads back as one plain term with the
		// original content.
		term, err := ParseTerm(QuoteIfNeeded(tc.in))
		if err != nil {
			t.Errorf("ParseTerm(%s): %v", QuoteIfNeeded(tc.in), err)
			continue
		}
		if got, ok := plainIdent(term.Basic); !ok || got != tc.in {
			t.Errorf("ParseTerm(%s) = %+v, want plain %q", QuoteIfNeeded(tc.in), term, tc.in)
		}
	}
}

func TestParseTerm(t *testing.T) {
	cases := []struct {
		src  string
		want Term
	}{
		{`@blake2b256-9ft3x`, Term{Basic: DigestBasicTerm{Digest: DigestTerm{Digest: "blake2b256-9ft3x"}}}},
		{`!caldav-object-vtodo-v1`, Term{Basic: TypeBasicTerm{Type: TypeTerm{Name: "caldav-object-vtodo-v1"}}}},
		{`=COMPLETED`, Term{Exact: true, Basic: IdentBasicTerm{Ident: Ident{Name: "COMPLETED"}}}},
		{`"_ inbox"`, Term{Basic: QuotedRefBasicTerm{Ref: QuotedRef{Value: "_ inbox"}}}},
		{`  -client  `, Term{Basic: IdentBasicTerm{Ident: Ident{Name: "-client"}}}},
	}
	for _, tc := range cases {
		got, err := ParseTerm(tc.src)
		if err != nil {
			t.Errorf("ParseTerm(%q): %v", tc.src, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ParseTerm(%q) = %+v, want %+v", tc.src, got, tc.want)
		}
	}
	for _, src := range []string{``, `a b`, `a -> b`, `-> a`, `@blake2b256-9bt3`} {
		if _, err := ParseTerm(src); err == nil || !errors.Is400BadRequest(err) {
			t.Errorf("ParseTerm(%q) = %v; want a bad request", src, err)
		}
	}
}

// TestParseLiteralPrefix pins the organize entry point: the leading group is
// read by the real parser — a quoted tag holding an escaped quote AND a `]`
// neither ends the string nor the box — and the remainder is handed back
// verbatim for the caller's trailer.
func TestParseLiteralPrefix(t *testing.T) {
	src := `[t.ics "say \"hi\"] x" k=v] Read book`
	lit, rest, err := ParseLiteralPrefix(src)
	if err != nil {
		t.Fatalf("ParseLiteralPrefix(%q): %v", src, err)
	}
	want := Literal{ID: "t.ics", Tags: []string{`say "hi"] x`}, Atoms: []Atom{{"k", "v"}}}
	if !reflect.DeepEqual(lit, want) {
		t.Fatalf("Literal = %+v, want %+v", lit, want)
	}
	if rest != " Read book" {
		t.Fatalf("rest = %q, want %q", rest, " Read book")
	}
	var b strings.Builder
	WriteLiteral(&b, lit)
	if got := b.String(); got != `t.ics "say \"hi\"] x" k=v` {
		t.Fatalf("WriteLiteral = %q", got)
	}
	again, _, err := ParseLiteralPrefix("[" + b.String() + "]")
	if err != nil || !reflect.DeepEqual(again, lit) {
		t.Fatalf("re-parse = %+v, %v; want %+v", again, err, lit)
	}

	// No leading group: a bad request that still carries the SyntaxError.
	for _, src := range []string{`t.ics] no open bracket`, ``, `[t.ics`} {
		_, _, err := ParseLiteralPrefix(src)
		if err == nil || !errors.Is400BadRequest(err) {
			t.Errorf("ParseLiteralPrefix(%q) = %v; want a bad request", src, err)
			continue
		}
		var se *SyntaxError
		if !stderrors.As(err, &se) {
			t.Errorf("ParseLiteralPrefix(%q) error %v does not wrap *SyntaxError", src, err)
		}
	}
}
