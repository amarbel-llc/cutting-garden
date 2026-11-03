package organize

import (
	"strings"
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The boxed-heading lane (cutting-garden#377, Go side): a heading may carry a
// box — `# ["id=…"] name` — making it a node of its own. These tests pin the
// parse, the byte-stable render, and what apply does with one (nothing, or a
// refusal that points at #377). Documents without a boxed heading are
// unaffected, which the rest of the suite (and the bats digests) pin.

func parseBoxedBody(t *testing.T, body string) document {
	t.Helper()
	doc, err := parseDocument(tagEnvelope + body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc
}

const boxedBody = `
- [inbox.ics] Inbox

# ["id=tags-trips"] trips

## -26

### ["id=tags-trips-26-01"] -01

- [a.ics] A
`

func TestParseBoxedHeading_BoxAndTerm(t *testing.T) {
	doc := parseBoxedBody(t, boxedBody)
	if len(doc.Sections) != 3 {
		t.Fatalf("sections = %+v, want 3", doc.Sections)
	}
	want := []struct {
		depth int
		term  string
		box   string // "" = unboxed
	}{
		{1, "trips", "id=tags-trips"},
		{2, "-26", ""},
		{3, "-01", "id=tags-trips-26-01"},
	}
	for i, w := range want {
		s := doc.Sections[i]
		if s.Depth != w.depth || s.Term != w.term {
			t.Errorf("section %d = depth %d term %q, want depth %d term %q", i, s.Depth, s.Term, w.depth, w.term)
		}
		switch {
		case w.box == "" && s.Box != nil:
			t.Errorf("section %d: Box = %+v, want nil (a synthetic heading)", i, s.Box)
		case w.box != "" && (s.Box == nil || s.Box.ID != w.box):
			t.Errorf("section %d: Box = %+v, want id %q", i, s.Box, w.box)
		}
	}
	// The box is a node, not a placement: the line keeps its deepest bucket and
	// the headings' ids appear in no membership.
	m, err := doc.memberships(true)
	if err != nil {
		t.Fatalf("memberships: %v", err)
	}
	assertMembership(t, m, "a.ics", "-01")
	assertMembership(t, m, "inbox.ics")
	for _, id := range []string{"id=tags-trips", "id=tags-trips-26-01"} {
		if _, in := m[id]; in {
			t.Errorf("boxed heading %s must not be counted as a member of any bucket: %v", id, m)
		}
	}
}

func TestParseBoxedHeading_PlainHeadingUnchanged(t *testing.T) {
	doc := parseBoxedBody(t, "\n# work\n\n- [a.ics] A\n")
	if len(doc.Sections) != 1 || doc.Sections[0].Box != nil || doc.Sections[0].Term != "work" {
		t.Fatalf("sections = %+v, want one plain heading `work`", doc.Sections)
	}
}

func TestParseBoxedHeading_KeepsTagsAndAtoms(t *testing.T) {
	doc := parseBoxedBody(t, "\n# [t0 chore due=2026-11-01] trips\n")
	b := doc.Sections[0].Box
	if b == nil || b.ID != "t0" || len(b.Tags) != 1 || b.Tags[0] != "chore" ||
		len(b.Fields) != 1 || b.Fields[0].Name != "due" || b.Fields[0].Value != "2026-11-01" {
		t.Fatalf("Box = %+v, want id t0, tag chore, atom due=2026-11-01", b)
	}
	if doc.Sections[0].Term != "trips" {
		t.Errorf("Term = %q, want trips", doc.Sections[0].Term)
	}
}

// Depth stays structure-only and a reset still resets: a boxed heading is
// just a heading to the ladder.
func TestParseBoxedHeading_DepthAndResetUnchanged(t *testing.T) {
	doc := parseBoxedBody(t, "\n## [r] work\n\n- [a.ics] A\n\n#\n\n- [d.ics] D\n")
	if len(doc.Sections) != 1 || doc.Sections[0].Depth != 1 {
		t.Fatalf("sections = %+v, want one boxed section normalized to depth 1", doc.Sections)
	}
	m, err := doc.memberships(true)
	if err != nil {
		t.Fatal(err)
	}
	assertMembership(t, m, "a.ics", "work")
	assertMembership(t, m, "d.ics")
}

func TestParseBoxedHeading_MalformedBoxIsRefused(t *testing.T) {
	for name, tc := range map[string]struct{ body, wantLine string }{
		"unterminated box":         {"\n# [\"id=x\" trips\n", "body line 1"},
		"temp id names no node":    {"\n# [+] trips\n", "body line 1"},
		"box without heading text": {"\n# [\"id=x\"]\n", "body line 1"},
		"empty box":                {"\n\n# [] trips\n", "body line 2"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseDocument(tagEnvelope + tc.body)
			if err == nil || !errors.Is400BadRequest(err) {
				t.Fatalf("parse = %v, want a bad request", err)
			}
			for _, frag := range []string{"boxed heading", tc.wantLine} {
				if !strings.Contains(err.Error(), frag) {
					t.Errorf("error %q lacks %q", err, frag)
				}
			}
		})
	}
}

func TestRenderBoxedHeading_ByteStable(t *testing.T) {
	doc := parseBoxedBody(t, boxedBody)
	out := render(doc)
	if !strings.HasSuffix(out, boxedBody) {
		t.Errorf("render did not reproduce the body byte for byte:\n%s", out)
	}
	again, err := parseDocument(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if render(again) != out {
		t.Errorf("render is not a fixed point:\n%s\n---\n%s", out, render(again))
	}
	if renderCanonical(again) != renderCanonical(doc) {
		t.Errorf("canonical forms differ after a round trip")
	}
}

func TestObjectLinesByID_SeesBoxedHeadings(t *testing.T) {
	byID := objectLinesByID(parseBoxedBody(t, boxedBody))
	got, ok := byID["id=tags-trips"]
	if !ok {
		t.Fatalf("a boxed heading is a node the preview index must know: %v", byID)
	}
	if got.Desc != "trips" {
		t.Errorf("heading entry Desc = %q, want its text `trips`", got.Desc)
	}
	if _, ok := byID["a.ics"]; !ok {
		t.Errorf("ordinary lines must still be indexed: %v", byID)
	}
}

func TestRefuseBoxedHeadingEdits(t *testing.T) {
	base := parseBoxedBody(t, boxedBody)

	cases := []struct {
		name string
		body string // the edited body; "" = unchanged
		want []string
	}{
		{name: "unchanged applies as a no-op"},
		{
			name: "a line moved between buckets is not a heading edit",
			body: strings.Replace(boxedBody, "- [a.ics] A", "- [a.ics] A\n- [b.ics] B", 1),
		},
		{
			name: "heading text edited",
			body: strings.Replace(boxedBody, "] trips", "] voyages", 1),
			want: []string{`id=tags-trips`, "trips", "edited", "#377"},
		},
		{
			name: "boxed heading deleted",
			body: strings.Replace(boxedBody, "### [\"id=tags-trips-26-01\"] -01\n\n", "", 1),
			want: []string{`id=tags-trips-26-01`, "-01", "deleted", "#377"},
		},
		{
			name: "boxed heading moved under another heading",
			body: strings.Replace(boxedBody, "### [\"id=tags-trips-26-01\"] -01", "# [\"id=tags-trips-26-01\"] -01", 1),
			want: []string{`id=tags-trips-26-01`, "-01", "moved", "#377"},
		},
		{
			name: "a heading gains a box (a node cannot be conjured)",
			body: strings.Replace(boxedBody, "## -26", "## [\"id=tags-new\"] -26", 1),
			want: []string{`id=tags-new`, "-26", "#377"},
		},
		{
			name: "a creation under a boxed heading",
			body: strings.Replace(boxedBody, "- [a.ics] A", "- [a.ics] A\n- [+n] New thing", 1),
			want: []string{"creat", "id=tags-trips-26-01", "#377"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			edited := base
			if tc.body != "" {
				edited = parseBoxedBody(t, tc.body)
			}
			err := refuseBoxedHeadingEdits(base, edited)
			if len(tc.want) == 0 {
				if err != nil {
					t.Fatalf("refuseBoxedHeadingEdits = %v, want nil", err)
				}
				return
			}
			if err == nil || !errors.Is400BadRequest(err) {
				t.Fatalf("refuseBoxedHeadingEdits = %v, want a bad request", err)
			}
			for _, frag := range tc.want {
				if !strings.Contains(err.Error(), frag) {
					t.Errorf("error %q lacks %q", err, frag)
				}
			}
		})
	}
}

// A creation OUTSIDE every boxed heading is untouched by the guard.
func TestRefuseBoxedHeadingEdits_CreationElsewhereIsFine(t *testing.T) {
	base := parseBoxedBody(t, boxedBody)
	edited := parseBoxedBody(t, strings.Replace(boxedBody, "- [inbox.ics] Inbox", "- [inbox.ics] Inbox\n- [+n] New thing", 1))
	if err := refuseBoxedHeadingEdits(base, edited); err != nil {
		t.Fatalf("a creation above every heading must pass: %v", err)
	}
}
