package organize

import (
	"strings"
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The `_nest` lever (cutting-garden#353): how deep a `(tree)` grouping renders as
// nested headings — full | 1 | none. It is a PRESENTATION lever, so unlike
// `_tag-atoms`/`_tag-strip` it stays out of the canonical form: changing only
// `_nest` is never an unapplied edit.

// treeDoc is a minimal `(tree)` document with the given extra envelope lines.
func treeDoc(extra string) string {
	return `---
- _base = @blake2b256-acdef9
- _anchor = org://test/_tags.org
- _type = !cutting_garden-org-headline-v1
- _group-by = (tree)
` + extra + `! organize-base-v1
---

- [a] a
`
}

func TestParseNestField_Domain(t *testing.T) {
	for _, v := range []string{"full", "1", "none"} {
		doc, err := parseDocument(treeDoc("- _nest = " + v + "\n"))
		if err != nil {
			t.Fatalf("_nest = %s: %v", v, err)
		}
		if doc.Nest != v {
			t.Errorf("_nest = %s parsed as %q", v, doc.Nest)
		}
	}
	doc, err := parseDocument(treeDoc(""))
	if err != nil || doc.Nest != "" {
		t.Fatalf("absent _nest: Nest %q, err %v; want empty", doc.Nest, err)
	}

	_, err = parseDocument(treeDoc("- _nest = deep\n"))
	if err == nil || !errors.Is400BadRequest(err) {
		t.Fatalf("_nest = deep: %v, want a bad request", err)
	}
	for _, frag := range []string{"_nest", "deep", "full", "none"} {
		if !strings.Contains(err.Error(), frag) {
			t.Errorf("error %q lacks %q", err, frag)
		}
	}
}

// A lever that does nothing is a trap: `_nest` on any grouping but `(tree)` is
// refused, naming the grouping it belongs to.
func TestParseNestField_OnlyWithTreeGrouping(t *testing.T) {
	for name, group := range map[string]string{
		"tags grouping":  "- _group-by = (tags)\n",
		"namespace":      "- _group-by = project\n",
		"field grouping": "",
	} {
		t.Run(name, func(t *testing.T) {
			text := "---\n- _base = @blake2b256-acdef9\n- _anchor = x://y/\n" + group +
				"- _nest = none\n! organize-base-v1\n---\n\n- [a] a\n"
			_, err := parseDocument(text)
			if err == nil || !errors.Is400BadRequest(err) {
				t.Fatalf("parse = %v, want a bad request", err)
			}
			for _, frag := range []string{"_nest", "(tree)"} {
				if !strings.Contains(err.Error(), frag) {
					t.Errorf("error %q lacks %q", err, frag)
				}
			}
		})
	}
}

func TestEffectiveNest_DocThenConfigThenFull(t *testing.T) {
	for _, tc := range []struct{ doc, config, want string }{
		{"", "", "full"},
		{"", "none", "none"},
		{"1", "none", "1"},
		{"full", "none", "full"},
		{"none", "", "none"},
	} {
		if got := effectiveNest(tc.doc, tc.config); got != tc.want {
			t.Errorf("effectiveNest(%q, %q) = %q, want %q", tc.doc, tc.config, got, tc.want)
		}
	}
}

// The envelope field is omitted at the built-in default (full) and echoed
// otherwise — a config default that differs from full included — and only a
// `(tree)` grouping carries it at all.
func TestNestEnvelopeValue_OmittedAtDefaultEchoedOtherwise(t *testing.T) {
	for _, tc := range []struct{ groupBy, doc, config, want string }{
		{groupByTree, "", "", ""},
		{groupByTree, "full", "", ""},
		{groupByTree, "", "none", "none"},
		{groupByTree, "", "1", "1"},
		{groupByTree, "full", "none", ""},
		{groupByTree, "1", "none", "1"},
		{"(tags)", "", "none", ""},
		{"status=", "", "1", ""},
	} {
		if got := nestEnvelopeValue(tc.groupBy, tc.doc, tc.config); got != tc.want {
			t.Errorf("nestEnvelopeValue(%q, doc %q, config %q) = %q, want %q",
				tc.groupBy, tc.doc, tc.config, got, tc.want)
		}
	}
}

func TestRenderNest_AfterGroupByBeforeTagAtoms(t *testing.T) {
	doc, err := parseDocument(treeDoc("- _nest = none\n- _tag-atoms = trailing\n"))
	if err != nil {
		t.Fatal(err)
	}
	out := render(doc)
	want := "- _group-by = (tree)\n- _nest = none\n- _tag-atoms = trailing\n"
	if !strings.Contains(out, want) {
		t.Errorf("render lacks %q in order:\n%s", want, out)
	}
	plain, err := parseDocument(treeDoc(""))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(render(plain), "_nest") {
		t.Errorf("an absent _nest must not render:\n%s", render(plain))
	}
}

// THE presentation property: the canonical form (the bytes `_base`
// content-addresses) leaves `_nest` out, so documents that differ only in it
// share a base; every OTHER lever still shows in the canonical form.
func TestRenderCanonical_ExcludesNestOnly(t *testing.T) {
	bare, err := parseDocument(treeDoc(""))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"full", "1", "none"} {
		d, err := parseDocument(treeDoc("- _nest = " + v + "\n"))
		if err != nil {
			t.Fatal(err)
		}
		if renderCanonical(d) != renderCanonical(bare) {
			t.Errorf("_nest = %s leaked into the canonical form:\n%s", v, renderCanonical(d))
		}
	}
	atoms, err := parseDocument(treeDoc("- _tag-atoms = trailing\n"))
	if err != nil {
		t.Fatal(err)
	}
	if renderCanonical(atoms) == renderCanonical(bare) {
		t.Error("`_tag-atoms` must remain part of the canonical form")
	}
}

// fmt-organize's clean-body gate (bodyMatchesBase): an edit of only `_nest` is
// clean, an edit of any other lever is still an unapplied edit.
func TestBodyMatchesBase_NestEditIsClean_OtherLeversAreNot(t *testing.T) {
	base := renderCanonical(mustParse(t, treeDoc("")))

	for _, tc := range []struct {
		name  string
		extra string
		clean bool
	}{
		{"_nest changed", "- _nest = none\n", true},
		{"_nest set to the default", "- _nest = full\n", true},
		{"_tag-atoms changed", "- _tag-atoms = trailing\n", false},
		{"_tag-strip changed", "- _tag-strip = none\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := bodyMatchesBase(mustParse(t, treeDoc(tc.extra)), base)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.clean {
				t.Errorf("bodyMatchesBase = %v, want %v", got, tc.clean)
			}
		})
	}
}

func mustParse(t *testing.T, text string) document {
	t.Helper()
	doc, err := parseDocument(text)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc
}
