package cutting_garden_plugins

import (
	"reflect"
	"testing"
)

// TestParseURITemplateRejects pins the Level 1 grammar guards (RFC 0018
// §2): operators, modifiers, adjacent variables, duplicates, and an
// unclosed brace all fail the parse rather than producing a template that
// matches unpredictably.
func TestParseURITemplateRejects(t *testing.T) {
	for _, tc := range []struct {
		name string
		tmpl string
	}{
		{"reserved-operator", "fj://{+host}/x"},
		{"fragment-operator", "fj://{#host}/x"},
		{"query-operator", "fj://x{?a}"},
		{"path-operator", "fj://x{/a}"},
		{"explode", "fj://{host*}/x"},
		{"prefix", "fj://{host:3}/x"},
		{"adjacent-vars", "fj://{a}{b}"},
		{"duplicate-var", "fj://{a}/{a}"},
		{"unclosed", "fj://{a/x"},
		{"empty-name", "fj://{}/x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseURITemplate(tc.tmpl); err == nil {
				t.Fatalf("ParseURITemplate(%q) = nil error; want reject",
					tc.tmpl)
			}
		})
	}
}

// TestParseURITemplateAccepts confirms the real-scheme shapes parse,
// including a sub-segment split (newsblur's {feed}:{hash}) and a
// variable-free fixed URI.
func TestParseURITemplateAccepts(t *testing.T) {
	for _, tmpl := range []string{
		"fj://{host}/{owner}/{repo}/issues/{number}",
		"newsblur://story/{feed}:{hash}",
		"caldav://{account}/{calendar}/{object}",
		"caldav://singleton",
		"",
	} {
		if _, err := ParseURITemplate(tmpl); err != nil {
			t.Errorf("ParseURITemplate(%q) = %v; want accept", tmpl, err)
		}
	}
}

// TestExpandMatchRoundTrip is the RFC 0018 §3 bidirectional guarantee:
// Match(Expand(bindings)) == bindings, including a value carrying a '/'
// that must survive as %2F.
func TestExpandMatchRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name     string
		tmpl     string
		bindings map[string]string
	}{
		{
			"fj-issue",
			"fj://{host}/{owner}/{repo}/issues/{number}",
			map[string]string{
				"host": "forge.example", "owner": "acme",
				"repo": "web", "number": "42",
			},
		},
		{
			"newsblur-story-subsegment",
			"newsblur://story/{feed}:{hash}",
			map[string]string{"feed": "1328462", "hash": "69f0cd"},
		},
		{
			"value-with-slash-encodes",
			"caldav://{account}/{calendar}/{object}",
			map[string]string{
				"account": "work", "calendar": "cal",
				"object": "foo/bar",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpl, err := ParseURITemplate(tc.tmpl)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			uri, err := tmpl.Expand(tc.bindings)
			if err != nil {
				t.Fatalf("expand: %v", err)
			}

			got, ok := tmpl.Match(uri)
			if !ok {
				t.Fatalf("Match(%q) = not ok; want the expanded URI to match",
					uri)
			}
			if !reflect.DeepEqual(got, tc.bindings) {
				t.Fatalf("round trip via %q: got %v, want %v",
					uri, got, tc.bindings)
			}
		})
	}
}

// TestExpandEncodesSlash proves the §3 single-segment rule at the byte
// level: a '/' in a value becomes %2F so it does not spill into a new
// segment.
func TestExpandEncodesSlash(t *testing.T) {
	tmpl, err := ParseURITemplate("caldav://{account}/{calendar}/{object}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	uri, err := tmpl.Expand(map[string]string{
		"account": "work", "calendar": "cal", "object": "foo/bar",
	})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}

	const want = "caldav://work/cal/foo%2Fbar"
	if uri != want {
		t.Fatalf("Expand = %q, want %q", uri, want)
	}
}

// TestExpandUnboundVariableErrors: a missing binding is an error, not an
// empty substitution (RFC 0018 §3).
func TestExpandUnboundVariableErrors(t *testing.T) {
	tmpl, err := ParseURITemplate("fj://{host}/{owner}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if _, err := tmpl.Expand(map[string]string{"host": "x"}); err == nil {
		t.Fatal("Expand with an unbound variable = nil error; want error")
	}
}

// TestMatchAnchoredNoPartial: a URI that only prefix-matches, has an extra
// trailing segment, or belongs to another scheme yields no match — Match
// is anchored end to end (RFC 0018 §3).
func TestMatchAnchoredNoPartial(t *testing.T) {
	tmpl, err := ParseURITemplate("caldav://{account}/{calendar}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	for _, uri := range []string{
		"caldav://work",            // too few segments
		"caldav://work/cal/object", // extra trailing segment
		"other://work/cal",         // wrong scheme literal
		"caldav://work/cal/",       // trailing slash → empty last seg
	} {
		if _, ok := tmpl.Match(uri); ok {
			t.Errorf("Match(%q) = ok; want no match", uri)
		}
	}
}

// TestMatchGreedyLastDelimiter documents the greedy sub-segment rule: with
// two colons the first variable binds up to the LAST delimiter (RFC 0018
// §3, "maximal run").
func TestMatchGreedyLastDelimiter(t *testing.T) {
	tmpl, err := ParseURITemplate("x://{a}:{b}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got, ok := tmpl.Match("x://p:q:r")
	if !ok {
		t.Fatal("Match = not ok")
	}
	if got["a"] != "p:q" || got["b"] != "r" {
		t.Fatalf("greedy split: got a=%q b=%q, want a=%q b=%q",
			got["a"], got["b"], "p:q", "r")
	}
}

// TestFixedTemplateMatchesOnlyItself: a variable-free template matches its
// exact string and nothing else.
func TestFixedTemplateMatchesOnlyItself(t *testing.T) {
	tmpl, err := ParseURITemplate("caldav://singleton")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if got, ok := tmpl.Match("caldav://singleton"); !ok ||
		len(got) != 0 {
		t.Fatalf("Match(exact) = (%v, %v); want (empty, true)", got, ok)
	}
	if _, ok := tmpl.Match("caldav://other"); ok {
		t.Fatal("Match(other) = ok; want no match")
	}
}

// TestSpecificityAccessors pins the values the resolver's most-specific
// rule keys on (RFC 0018 §4).
func TestSpecificityAccessors(t *testing.T) {
	tmpl, err := ParseURITemplate("fj://{host}/{owner}/issues/{n}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// literals: "fj://" (5) + "/" (1) + "/issues/" (8) = 14
	if got := tmpl.LiteralCount(); got != 14 {
		t.Errorf("LiteralCount = %d, want 14", got)
	}
	if got := tmpl.VarCount(); got != 3 {
		t.Errorf("VarCount = %d, want 3", got)
	}
	if got := tmpl.PrefixLen(); got != 5 {
		t.Errorf("PrefixLen = %d, want 5", got)
	}
}
