package organize

import (
	"strings"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// The heading-depth lane (native tags design G10, slice 1 T5): depth is
// structure-only — the shallowest level present is the root — and an EMPTY
// heading is a context reset. Every test parses a whole hand-written document
// and reads the resolved placement through memberships (the projection apply
// uses), so the vectors pin exactly what an edit means.

// tagEnvelope is the `(tags)` envelope the hand-written bodies below sit under.
const tagEnvelope = `---
- _base = @blake2b256-acdef9
- _anchor = caldav://host/dav/cal/
- _type = !caldav-object-v1
- _group-by = (tags)
! organize-base-v1
---
`

// parseTagBody parses tagEnvelope + body and returns its multi-valued
// memberships.
func parseTagBody(t *testing.T, body string) map[string][]string {
	t.Helper()
	doc, err := parseDocument(tagEnvelope + body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	m, err := doc.memberships(true)
	if err != nil {
		t.Fatalf("memberships: %v", err)
	}
	return m
}

// assertMembership pins one id's resolved bucket set (order-insensitive).
func assertMembership(t *testing.T, m map[string][]string, id string, want ...string) {
	t.Helper()
	got, present := m[id]
	if !present {
		t.Errorf("%s: absent from memberships, want %v", id, want)
		return
	}
	if !setEqual(got, want) || len(got) != len(want) {
		t.Errorf("%s: memberships = %v, want %v", id, got, want)
	}
}

// TestParseDepthNormalization_ShallowestIsRoot pins that a document written at
// `##` parses identically to the same document at `#` — the depth numbers only
// encode nesting relative to the shallowest level present.
func TestParseDepthNormalization_ShallowestIsRoot(t *testing.T) {
	atHash := parseTagBody(t, `
- [u.ics] Loose

# work

- [a.ics] A

# errand

- [b.ics] B
`)
	atDoubleHash := parseTagBody(t, `
- [u.ics] Loose

## work

- [a.ics] A

## errand

- [b.ics] B
`)
	for _, m := range []map[string][]string{atHash, atDoubleHash} {
		assertMembership(t, m, "u.ics")
		assertMembership(t, m, "a.ics", "work")
		assertMembership(t, m, "b.ics", "errand")
	}
}

// TestParseDepthNormalization_RendersAtRoot pins that the parsed sections carry
// the NORMALIZED depth, so a `##`-rooted document renders back at `#` — and that
// the root is computed over NON-EMPTY headings only: a `#` reset shallower than
// every real heading does not push `work` to depth 2.
func TestParseDepthNormalization_RendersAtRoot(t *testing.T) {
	for _, body := range []string{
		"\n## work\n\n- [a.ics] A\n",
		"\n## work\n\n- [a.ics] A\n\n#\n\n- [d.ics] D\n",
	} {
		doc, err := parseDocument(tagEnvelope + body)
		if err != nil {
			t.Fatalf("parse %q: %v", body, err)
		}
		if len(doc.Sections) != 1 || doc.Sections[0].Depth != 1 {
			t.Fatalf("%q: sections = %+v, want one section at depth 1", body, doc.Sections)
		}
		if out := render(doc); !strings.Contains(out, "\n# work\n") || strings.Contains(out, "##") {
			t.Errorf("%q: a `##`-rooted document must render at `#`:\n%s", body, out)
		}
	}
	// The reset in the second body is shallower than `work`, so it still pops it.
	m := parseTagBody(t, "\n## work\n\n- [a.ics] A\n\n#\n\n- [d.ics] D\n")
	assertMembership(t, m, "a.ics", "work")
	assertMembership(t, m, "d.ics")
}

// TestParseReset_PopsToNearestOpenHeading pins the rule precisely: a reset lands
// the following lines under the nearest OPEN heading shallower than it, not the
// literal depth N−1 — on a non-contiguous ladder `# a` / `### b` / `###` the
// line lands under `a`.
func TestParseReset_PopsToNearestOpenHeading(t *testing.T) {
	m := parseTagBody(t, `
# work

### errand

- [b.ics] B

###

- [c.ics] C
`)
	assertMembership(t, m, "b.ics", "errand")
	assertMembership(t, m, "c.ics", "work")
}

// TestParseDepthNormalization_NestingIsRelative pins that nesting survives
// normalization: `##` / `###` reads as `#` / `##`, so the deeper heading is
// still the child (its value wins for the lines beneath it).
func TestParseDepthNormalization_NestingIsRelative(t *testing.T) {
	m := parseTagBody(t, `
## work

- [a.ics] A

### errand

- [b.ics] B
`)
	assertMembership(t, m, "a.ics", "work")
	assertMembership(t, m, "b.ics", "errand")
}

// TestParseReset_DesignExample pins the design G10 example verbatim: `##` pops
// the `-client`/errand context so the next line lands under `work` ONLY, and
// `#` returns to the ungrouped context.
func TestParseReset_DesignExample(t *testing.T) {
	m := parseTagBody(t, `
# work

- [a.ics] A

## errand

- [b.ics] B

##

- [c.ics] C

#

- [d.ics] D
`)
	assertMembership(t, m, "a.ics", "work")
	assertMembership(t, m, "b.ics", "errand")
	assertMembership(t, m, "c.ics", "work")
	assertMembership(t, m, "d.ics")
}

// TestParseReset_ResolvesToParentSection pins HOW a reset resolves: a line under
// a `##` reset attaches to the parent section itself — exactly as if written
// under that heading — so the reset never becomes a section and a re-render of
// the parsed document carries no empty heading.
func TestParseReset_ResolvesToParentSection(t *testing.T) {
	doc, err := parseDocument(tagEnvelope + `
# work

- [a.ics] A

## errand

- [b.ics] B

##

- [c.ics] C

#

- [d.ics] D
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Sections) != 2 {
		t.Fatalf("sections = %+v, want exactly [work errand] (resets are not sections)", doc.Sections)
	}
	work := doc.Sections[0]
	if work.Term != "work" || len(work.Lines) != 2 || work.Lines[0].ID != "a.ics" || work.Lines[1].ID != "c.ics" {
		t.Errorf("work section = %+v, want lines [a.ics c.ics]", work)
	}
	if len(doc.Ungrouped) != 1 || doc.Ungrouped[0].ID != "d.ics" {
		t.Errorf("ungrouped = %+v, want [d.ics]", doc.Ungrouped)
	}
	for _, line := range strings.Split(render(doc), "\n") {
		if strings.Trim(line, "#") == "" && strings.HasPrefix(line, "#") {
			t.Errorf("re-rendered document must carry no empty heading, got %q", line)
		}
	}
}

// TestParseReset_DeeperThanCurrentIsNoop pins that a reset deeper than the
// current context pops nothing: `##` directly under `# work` (context depth 1)
// leaves the following line under work, and so does a `###`.
func TestParseReset_DeeperThanCurrentIsNoop(t *testing.T) {
	m := parseTagBody(t, `
# work

- [a.ics] A

##

- [b.ics] B

###

- [c.ics] C
`)
	assertMembership(t, m, "a.ics", "work")
	assertMembership(t, m, "b.ics", "work")
	assertMembership(t, m, "c.ics", "work")
}

// TestParseReset_TopResetFromNested pins that `#` pops EVERY open heading at
// once (not just one level): from under `# work` / `## errand`, the next line is
// ungrouped.
func TestParseReset_TopResetFromNested(t *testing.T) {
	m := parseTagBody(t, `
# work

## errand

- [b.ics] B

#

- [d.ics] D
`)
	assertMembership(t, m, "b.ics", "errand")
	assertMembership(t, m, "d.ics")
}

// TestParseReset_ReenterNeedsNewHeading pins that after a reset, a bucket is
// only re-entered by a new non-empty heading: `#` then `# work` files the line
// under work again, and the two `work` sections read as one membership.
func TestParseReset_ReenterNeedsNewHeading(t *testing.T) {
	m := parseTagBody(t, `
# work

- [a.ics] A

#

- [d.ics] D

# work

- [e.ics] E
`)
	assertMembership(t, m, "a.ics", "work")
	assertMembership(t, m, "d.ics")
	assertMembership(t, m, "e.ics", "work")
}

// TestParseReset_NormalizedDepth pins that resets are read AFTER depth
// normalization: in a `##`-rooted document, `##` (normalized `#`) is the
// top-level reset returning to ungrouped, and `###` pops one level.
func TestParseReset_NormalizedDepth(t *testing.T) {
	m := parseTagBody(t, `
## work

- [a.ics] A

### errand

- [b.ics] B

###

- [c.ics] C

##

- [d.ics] D
`)
	assertMembership(t, m, "a.ics", "work")
	assertMembership(t, m, "b.ics", "errand")
	assertMembership(t, m, "c.ics", "work")
	assertMembership(t, m, "d.ics")
}

// TestParseReset_Spelling1TypeHeading pins the interplay with a spelling-1
// `# !<type>` heading, which is just another stack frame: under `# !type` /
// `## status=` / `### =A`, a `###` reset pops to the dimension heading and a
// `##` reset pops to the type heading (both value-less: the object reads as
// unbucketed), while `#` pops past the type heading to the document's
// ungrouped set. The field grouping's assignments see the same placement.
func TestParseReset_Spelling1TypeHeading(t *testing.T) {
	doc, err := parseDocument(`---
- _base = @blake2b256-acdef9
- _anchor = caldav://host/dav/cal/
! organize-base-v1
---

# !caldav-object-v1

## status=

### =A

- [a.ics !caldav-object-v1] A

###

- [b.ics !caldav-object-v1] B

##

- [c.ics !caldav-object-v1] C

#

- [d.ics !caldav-object-v1] D
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc.groupedDimension() != "status" {
		t.Fatalf("groupedDimension = %q, want status", doc.groupedDimension())
	}
	asg, err := doc.assignments()
	if err != nil {
		t.Fatalf("assignments: %v", err)
	}
	for id, want := range map[string]string{"a.ics": "A", "b.ics": "", "c.ics": "", "d.ics": ""} {
		if got := asg[id]; got != want {
			t.Errorf("assignment[%s] = %q, want %q", id, got, want)
		}
	}
	// b sits under the dimension heading, c under the type heading, d ungrouped.
	var dim, typ *section
	for i := range doc.Sections {
		switch doc.Sections[i].Term {
		case "status=":
			dim = &doc.Sections[i]
		case "!caldav-object-v1":
			typ = &doc.Sections[i]
		}
	}
	if dim == nil || len(dim.Lines) != 1 || dim.Lines[0].ID != "b.ics" {
		t.Errorf("dimension section lines = %+v, want [b.ics]", dim)
	}
	if typ == nil || len(typ.Lines) != 1 || typ.Lines[0].ID != "c.ics" {
		t.Errorf("type section lines = %+v, want [c.ics]", typ)
	}
	if len(doc.Ungrouped) != 1 || doc.Ungrouped[0].ID != "d.ics" {
		t.Errorf("ungrouped = %+v, want [d.ics]", doc.Ungrouped)
	}
}

// TestParseFieldDoc_DepthNormalizationPreservesLadder pins the conformance bar
// for the field dialect: a `# status=` / `## =value` document parses and
// re-renders byte-identically (the root is already `#`), and the same document
// hand-written one level deeper reads the same assignments.
func TestParseFieldDoc_DepthNormalizationPreservesLadder(t *testing.T) {
	out := render(spelling2Doc())
	reparsed, err := parseDocument(out)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if render(reparsed) != out {
		t.Errorf("field document must round-trip byte-identically:\n%s\nvs\n%s", out, render(reparsed))
	}

	deeper := strings.NewReplacer("\n## =", "\n### =", "\n# status=", "\n## status=").Replace(out)
	deepDoc, err := parseDocument(deeper)
	if err != nil {
		t.Fatalf("parse deeper: %v", err)
	}
	if render(deepDoc) != out {
		t.Errorf("a `##`-rooted field document must normalize back to the `#` ladder:\n%s\nvs\n%s",
			render(deepDoc), out)
	}
}

// TestGenerateNeverEmitsResetHeading pins that generation never produces an
// empty heading, for both the tag (hoisted, depth 1) and field dialects and for
// both type spellings — resets are a hand-edit affordance only.
func TestGenerateNeverEmitsResetHeading(t *testing.T) {
	nodes := []cgp.Node{
		{
			URI: mustURL(t, "caldav://h/c/a.ics"), Type: "caldav-object-v1",
			Facets: map[string][]cgp.FacetValue{"categories": {{Key: "work"}}, "status": {{Key: "A"}}},
		},
		{
			URI: mustURL(t, "caldav://h/c/b.ics"), Type: "caldav-object-vevent-v1",
			Facets: map[string][]cgp.FacetValue{"categories": {{Key: "errand"}}},
		},
	}
	for _, spec := range []groupSpec{
		{Dim: "categories", Kind: groupKindTagWhole},
		{Dim: "status", Kind: groupKindField},
	} {
		for _, set := range [][]cgp.Node{nodes[:1], nodes} {
			doc, err := buildDocument(set, "caldav://h/c/", "", spec, &fakeLister{}, nil)
			if err != nil {
				t.Fatalf("buildDocument(%v): %v", spec, err)
			}
			for _, s := range doc.Sections {
				if s.Term == "" {
					t.Errorf("buildDocument(%v) emitted an empty heading section: %+v", spec, doc.Sections)
				}
			}
			for _, line := range strings.Split(render(doc), "\n") {
				if strings.HasPrefix(line, "#") && strings.TrimLeft(line, "#") == "" {
					t.Errorf("render(%v) emitted a reset heading %q", spec, line)
				}
			}
		}
	}
}

// TestGenerate_TagBucketsAtMinimalDepth pins the generator side of G10: a
// single-type `(tags)` document's buckets sit at depth 1 (`# work`), a
// multi-type one nests them at depth 2 under `# !<type>`, and a field grouping
// keeps `# dim=` at 1 / `## =value` at 2.
func TestGenerate_TagBucketsAtMinimalDepth(t *testing.T) {
	single := []cgp.Node{{
		URI: mustURL(t, "caldav://h/c/a.ics"), Type: "caldav-object-v1",
		Facets: map[string][]cgp.FacetValue{"categories": {{Key: "work"}}, "status": {{Key: "A"}}},
	}}
	multi := append(single, cgp.Node{
		URI: mustURL(t, "caldav://h/c/b.ics"), Type: "caldav-object-vevent-v1",
		Facets: map[string][]cgp.FacetValue{"categories": {{Key: "errand"}}},
	})
	tags := groupSpec{Dim: "categories", Kind: groupKindTagWhole}
	field := groupSpec{Dim: "status", Kind: groupKindField}

	depths := func(set []cgp.Node, spec groupSpec) map[string]int {
		t.Helper()
		doc, err := buildDocument(set, "caldav://h/c/", "", spec, &fakeLister{}, nil)
		if err != nil {
			t.Fatalf("buildDocument: %v", err)
		}
		out := map[string]int{}
		for _, s := range doc.Sections {
			out[s.Term] = s.Depth
		}
		return out
	}

	if d := depths(single, tags); d["work"] != 1 {
		t.Errorf("single-type (tags) bucket depth = %d, want 1: %v", d["work"], d)
	}
	if d := depths(multi, tags); d["!caldav-object-v1"] != 1 || d["work"] != 2 || d["errand"] != 2 {
		t.Errorf("multi-type (tags) depths = %v, want type 1 / buckets 2", d)
	}
	if d := depths(single, field); d["status="] != 1 || d["=A"] != 2 {
		t.Errorf("field depths = %v, want `status=` 1 / `=A` 2", d)
	}
}
