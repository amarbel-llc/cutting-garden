package traversal_serve

import (
	"strings"
	"testing"
)

// TestSectionTOML_WrapperStripping pins the RFC 0013 §initialize
// contract on the shapes a plugin section takes: bare-table scalars
// hoisted to top level, sub-arrays re-headed, deeper sub-tables
// re-headed with the prefix removed, quoting and comments preserved,
// and foreign sections excluded.
func TestSectionTOML_WrapperStripping(t *testing.T) {
	raw := []byte(`top_scalar = 1

[fj]
token_env = "FJ_TOKEN" # keep me
host = "forge.example"

[[fj.roots]]
uri = "fj://forge.example/friedenberg"

[[fj.roots]]
uri = "fj://forge.example/amarbel-llc"

[fj.limits."odd.key"]
per_page = 50

[caldav]
ignored = true

[[traversal_plugins]]
name = "fj"
command = ["fj-cg", "traversal-serve"]
schemes = ["fj"]
`)

	got, err := SectionTOML(raw, "fj")
	if err != nil {
		t.Fatal(err)
	}

	want := `token_env = "FJ_TOKEN" # keep me
host = "forge.example"

[[roots]]
uri = "fj://forge.example/friedenberg"

[[roots]]
uri = "fj://forge.example/amarbel-llc"

[limits."odd.key"]
per_page = 50

`
	if got != want {
		t.Errorf("SectionTOML = %q, want %q", got, want)
	}
}

func TestSectionTOML_AbsentSectionIsEmpty(t *testing.T) {
	got, err := SectionTOML([]byte("[caldav]\nx = 1\n"), "fj")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("absent section: got %q, want empty", got)
	}
}

func TestSectionTOML_TopLevelArraySectionRejected(t *testing.T) {
	_, err := SectionTOML([]byte("[[fj]]\nx = 1\n"), "fj")
	if err == nil {
		t.Fatal("[[fj]] as config_section must be rejected")
	}
	if !strings.Contains(err.Error(), "sub-array") {
		t.Errorf("error %q should point at the sub-array alternative", err)
	}
}

func TestSectionTOML_BadSectionNameRejected(t *testing.T) {
	if _, err := SectionTOML(nil, `a.b`); err == nil {
		t.Fatal("dotted section name must be rejected")
	}
}

// TestSectionTOML_QuotedBracketInKey pins the quote-aware scanning: a
// quoted key containing ']' or '#' does not truncate the header, and a
// quoted first segment matches the section.
func TestSectionTOML_QuotedBracketInKey(t *testing.T) {
	raw := []byte("[\"fj\".sub]\nx = 1\n\n[fj.\"a]b\"]\ny = 2\n")

	got, err := SectionTOML(raw, "fj")
	if err != nil {
		t.Fatal(err)
	}

	want := "[sub]\nx = 1\n\n[\"a]b\"]\ny = 2\n"
	if got != want {
		t.Errorf("SectionTOML = %q, want %q", got, want)
	}
}

func TestValidateStanzas(t *testing.T) {
	valid := PluginStanza{
		Name:    "fj",
		Command: []string{"fj-cg", "traversal-serve"},
		Schemes: []string{"fj"},
	}

	if err := ValidateStanzas([]PluginStanza{valid}); err != nil {
		t.Fatalf("valid stanza rejected: %v", err)
	}

	cases := []struct {
		name    string
		stanzas []PluginStanza
		wantSub string
	}{
		{
			"empty name",
			[]PluginStanza{{Command: []string{"x"}, Schemes: []string{"s"}}},
			"empty name",
		},
		{
			"dotted name",
			[]PluginStanza{{
				Name: "a.b", Command: []string{"x"}, Schemes: []string{"s"},
			}},
			"bare TOML key",
		},
		{
			"empty command",
			[]PluginStanza{{Name: "a", Schemes: []string{"s"}}},
			"empty command",
		},
		{
			"no schemes",
			[]PluginStanza{{Name: "a", Command: []string{"x"}}},
			"no schemes",
		},
		{
			"duplicate name",
			[]PluginStanza{
				{Name: "a", Command: []string{"x"}, Schemes: []string{"s"}},
				{Name: "a", Command: []string{"y"}, Schemes: []string{"t"}},
			},
			"duplicate name",
		},
		{
			"duplicate scheme",
			[]PluginStanza{
				{Name: "a", Command: []string{"x"}, Schemes: []string{"s"}},
				{Name: "b", Command: []string{"y"}, Schemes: []string{"s"}},
			},
			"claimed by both",
		},
	}

	for _, c := range cases {
		err := ValidateStanzas(c.stanzas)
		if err == nil {
			t.Errorf("%s: no error", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.wantSub) {
			t.Errorf("%s: error %q missing %q", c.name, err, c.wantSub)
		}
	}
}

func TestPluginStanzaSection(t *testing.T) {
	s := PluginStanza{Name: "fj"}
	if got := s.Section(); got != "fj" {
		t.Errorf("Section() = %q, want name default", got)
	}
	s.ConfigSection = "forgejo"
	if got := s.Section(); got != "forgejo" {
		t.Errorf("Section() = %q, want explicit override", got)
	}
}
