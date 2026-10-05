package organize

import "testing"

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
	for _, c := range cases {
		got, err := parseSelection(c.arg)
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
		if _, err := parseSelection(arg); err == nil {
			t.Errorf("parseSelection(%q): expected an error", arg)
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
