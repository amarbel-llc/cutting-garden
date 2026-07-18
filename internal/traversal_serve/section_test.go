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

	if err := ValidateStanzas(nil, []PluginStanza{valid}); err != nil {
		t.Fatalf("valid stanza rejected: %v", err)
	}
	if err := ValidateStanzas([]PluginStanza{valid}, nil); err != nil {
		t.Fatalf("valid stanza (general table) rejected: %v", err)
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
		{
			"unknown protocol",
			[]PluginStanza{{
				Name: "a", Command: []string{"x"}, Schemes: []string{"s"},
				Protocols: []string{"bogus"},
			}},
			"unknown protocol",
		},
	}

	for _, c := range cases {
		err := ValidateStanzas(nil, c.stanzas)
		if err == nil {
			t.Errorf("%s: no error", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.wantSub) {
			t.Errorf("%s: error %q missing %q", c.name, err, c.wantSub)
		}
	}
}

// TestValidateStanzas_CrossTableCollision pins cutting-garden#146
// decision 2: a name or scheme collision is caught even when the two
// stanzas come from different tables (the general `[[plugins]]` slice
// vs the legacy `[[traversal_plugins]]` alias slice) — the combined
// namespace, not two independent ones.
func TestValidateStanzas_CrossTableCollision(t *testing.T) {
	general := []PluginStanza{
		{Name: "fj", Command: []string{"fj-cg"}, Schemes: []string{"fj"}},
	}
	legacyNameClash := []PluginStanza{
		{Name: "fj", Command: []string{"other"}, Schemes: []string{"other"}},
	}
	if err := ValidateStanzas(general, legacyNameClash); err == nil ||
		!strings.Contains(err.Error(), "duplicate name") {
		t.Fatalf("cross-table name clash: got %v, want duplicate name error", err)
	}

	legacySchemeClash := []PluginStanza{
		{Name: "other", Command: []string{"other"}, Schemes: []string{"fj"}},
	}
	if err := ValidateStanzas(general, legacySchemeClash); err == nil ||
		!strings.Contains(err.Error(), "claimed by both") {
		t.Fatalf("cross-table scheme clash: got %v, want claimed-by-both error", err)
	}
}

// TestPluginStanzaEffectiveProtocols pins the default-to-traversal
// behavior (cutting-garden#146 decision 2): an empty Protocols acts as
// [ProtocolTraversal], which is how a [[traversal_plugins]]
// compatibility-alias stanza is always treated.
func TestPluginStanzaEffectiveProtocols(t *testing.T) {
	var s PluginStanza
	if got := s.EffectiveProtocols(); len(got) != 1 || got[0] != ProtocolTraversal {
		t.Errorf("EffectiveProtocols() with none declared = %v, want [%s]", got, ProtocolTraversal)
	}
	if !s.HasProtocol(ProtocolTraversal) {
		t.Error("HasProtocol(traversal) = false for a stanza with no protocols declared")
	}
	if s.HasProtocol(ProtocolCapture) {
		t.Error("HasProtocol(capture) = true for a stanza with no protocols declared")
	}

	s.Protocols = []string{ProtocolCapture}
	if got := s.EffectiveProtocols(); len(got) != 1 || got[0] != ProtocolCapture {
		t.Errorf("EffectiveProtocols() with explicit capture = %v, want [%s]", got, ProtocolCapture)
	}
	if s.HasProtocol(ProtocolTraversal) {
		t.Error("HasProtocol(traversal) = true for a capture-only stanza")
	}

	s.Protocols = []string{ProtocolCapture, ProtocolTraversal}
	if !s.HasProtocol(ProtocolCapture) || !s.HasProtocol(ProtocolTraversal) {
		t.Error("HasProtocol should report true for both declared protocols")
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
