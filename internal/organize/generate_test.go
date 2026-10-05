package organize

import (
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/command_components"
)

// TestProvenanceWrapsCommandInBackticks pins cutting-garden#243: the generated
// `% generated:` note wraps the echoed command in backticks so it renders as
// code and copy-pastes unambiguously, for both the query and no-query spellings.
// The command is the positional form (RFC 0020 §4.1): one trellis expression,
// then the group-by, each shell-quoted only when it must be.
func TestProvenanceWrapsCommandInBackticks(t *testing.T) {
	cases := []struct{ groupBy, query, origin, want string }{
		{
			"status=", "", "caldav:task",
			"generated: `cg organize caldav:task status=`",
		},
		{
			"status=", "_terminal=no", "caldav:task",
			"generated: `cg organize 'caldav:task -> _terminal=no' status=`",
		},
		{
			"(tags)", `component=VTODO due<"2026-08-01"`, `"caldav://h/me@example.com/"`,
			"generated: `cg organize '\"caldav://h/me@example.com/\" -> " +
				"component=VTODO due<\"2026-08-01\"' '(tags)'`",
		},
	}
	for _, c := range cases {
		if got := provenance(c.groupBy, c.query, c.origin); got != c.want {
			t.Errorf("provenance(%q, %q, %q)\n got %q\nwant %q",
				c.groupBy, c.query, c.origin, got, c.want)
		}
	}
}

// TestParseSelection pins organize's positional selection (RFC 0020 §4.1): one
// trellis expression carrying its own origin, with a whitespace-free argument
// that is not a trellis term taken as a literal URI.
func TestParseSelection(t *testing.T) {
	cases := []struct{ arg, origin, originSource, query string }{
		{"caldav:task", "caldav:task", "caldav:task", ""},
		{"caldav:task -> status=needs-action", "caldav:task", "caldav:task", "status=needs-action"},
		{
			`"caldav://h/me@example.com/" -> component=VTODO`,
			"caldav://h/me@example.com/", `"caldav://h/me@example.com/"`, "component=VTODO",
		},
		// A raw URL with a reserved rune is not a trellis term, but with no
		// whitespace it can only be an origin: taken literally, echoed quoted.
		{
			"caldav://h/me@example.com/", "caldav://h/me@example.com/",
			`"caldav://h/me@example.com/"`, "",
		},
	}
	noNames := func() map[string][]command_components.NamedRoot { return nil }
	for _, c := range cases {
		got, err := parseSelection(c.arg, noNames)
		if err != nil {
			t.Errorf("parseSelection(%q): %v", c.arg, err)
			continue
		}
		if got.Origin != c.origin || got.OriginSource != c.originSource || got.Query != c.query {
			t.Errorf("parseSelection(%q) = %+v, want origin %q source %q query %q",
				c.arg, got, c.origin, c.originSource, c.query)
		}
	}

	for _, arg := range []string{
		"caldav:task ->> status=x",          // only `->` bridges an origin
		"caldav:task status=x",              // two terms: not a lone origin
		"caldav://h/me@example.com/ -> x=y", // reserved rune AND whitespace: must be quoted
		"caldav:task -> [unclosed",
	} {
		if _, err := parseSelection(arg, noNames); err == nil {
			t.Errorf("parseSelection(%q): expected an error", arg)
		}
	}
}

// TestParseSelection_BoundType pins RFC 0020 §3.4 / §4.2: a selection may open
// with `!<name>`, a configured root's name, and what follows selects within
// that root — as further terms of the same step or as a step after `->`.
func TestParseSelection_BoundType(t *testing.T) {
	names := func() map[string][]command_components.NamedRoot {
		return map[string][]command_components.NamedRoot{
			"task": {{URL: "caldav://h/cal/task/", Schemes: []string{"caldav"}}},
			"shared": {
				{URL: "caldav://h/cal/shared/", Schemes: []string{"caldav"}},
				{URL: "fastmail://shared/", Schemes: []string{"fastmail"}},
			},
		}
	}

	cases := []struct{ arg, query string }{
		{"!task", ""},
		{"!task priority=0_must", "priority=0_must"},
		{"!task -> priority=0_must", "priority=0_must"},
		{"  !task   status=needs-action  due<\"2026-08-01\" ", `status=needs-action  due<"2026-08-01"`},
		{"!task -> [a, b] -> x", "[a, b] -> x"},
	}
	for _, c := range cases {
		got, err := parseSelection(c.arg, names)
		if err != nil {
			t.Errorf("parseSelection(%q): %v", c.arg, err)
			continue
		}
		if got.Origin != "caldav://h/cal/task/" || got.OriginSource != "!task" ||
			got.Query != c.query {
			t.Errorf("parseSelection(%q) = %+v, want the task root, source !task, query %q",
				c.arg, got, c.query)
		}
	}

	rejects := []struct{ arg, wantSub string }{
		{"!nosuch", "not a configured root name"},
		{"!caldav-object-vtodo-v1 status=x", "not a configured root name"},
		{"!shared", "ambiguous"},
		{"^!task", "cannot be negated"},
		{"!task ->", "expected a step"},
		{"!task [unclosed", "syntax error"},
	}
	for _, c := range rejects {
		_, err := parseSelection(c.arg, names)
		if err == nil {
			t.Errorf("parseSelection(%q): expected an error", c.arg)
			continue
		}
		if !strings.Contains(err.Error(), c.wantSub) {
			t.Errorf("parseSelection(%q): error %q does not contain %q",
				c.arg, err.Error(), c.wantSub)
		}
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"status=":      "status=",
		"caldav:task":  "caldav:task",
		"(tags)":       "'(tags)'",
		"a b":          "'a b'",
		"it's":         `'it'\''s'`,
		"":             "''",
		"date=(month)": "'date=(month)'",
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
