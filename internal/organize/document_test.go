package organize

import (
	"sort"
	"strings"
	"testing"
)

// spelling2Doc is a representative single-type (envelope `_type`) document.
func spelling2Doc() document {
	return document{
		Provenance: "generated: cg organize -group-by status caldav://host/dav/cal/",
		// A blech32-only stub: `_base` parses through trellis's DigestTerm,
		// whose data slot is charset-strict (no `b`, `i`, `o`, `1`).
		BaseDigest: "blake2b256-acdef9",
		Anchor:     "caldav://host/dav/cal/",
		Type:       "caldav-object-v1",
		Ungrouped:  []objectLine{{ID: "task2.ics", Desc: "Walk dog"}},
		Sections: []section{
			{Depth: 1, Term: "status="},
			{Depth: 2, Term: "=NEEDS-ACTION"},
			{Depth: 2, Term: "=COMPLETED", Lines: []objectLine{{ID: "task1.ics", Desc: "Buy milk"}}},
		},
	}
}

// TestRenderParseRoundTrip pins that render → parseDocument preserves the envelope
// fields and every bucket assignment — the invariant the three-way merge relies on.
func TestRenderParseRoundTrip(t *testing.T) {
	doc := spelling2Doc()
	got, err := parseDocument(render(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Anchor != doc.Anchor || got.Type != doc.Type ||
		got.BaseDigest != doc.BaseDigest || got.Provenance != doc.Provenance {
		t.Fatalf("envelope not preserved: %+v", got)
	}
	if got.groupedDimension() != "status" {
		t.Errorf("groupedDimension = %q, want status", got.groupedDimension())
	}

	want, _ := doc.assignments()
	have, err := got.assignments()
	if err != nil {
		t.Fatalf("assignments: %v", err)
	}
	if len(have) != len(want) {
		t.Fatalf("assignment count = %d, want %d", len(have), len(want))
	}
	for id, value := range want {
		if have[id] != value {
			t.Errorf("assignment[%s] = %q, want %q", id, have[id], value)
		}
	}
}

// TestRenderBlankLineDiscipline pins the newline rule: a single blank separates a
// heading from its objects, but sibling object lines run together — blanks mark
// heading/group boundaries, never individual objects.
func TestRenderBlankLineDiscipline(t *testing.T) {
	doc := document{
		Anchor: "caldav://h/c/",
		Type:   "caldav-object-v1",
		Sections: []section{
			{Depth: 1, Term: "status="},
			{Depth: 2, Term: "=NEEDS-ACTION", Lines: []objectLine{
				{ID: "a.ics", Desc: "A"},
				{ID: "b.ics", Desc: "B"},
			}},
		},
	}
	out := render(doc)
	if !strings.Contains(out, "## =NEEDS-ACTION\n\n- [a.ics] A") {
		t.Errorf("a heading must be followed by one blank line then its objects, got:\n%s", out)
	}
	if !strings.Contains(out, "- [a.ics] A\n- [b.ics] B") {
		t.Errorf("sibling object lines must run together (no blank between), got:\n%s", out)
	}
}

// TestRenderCanonicalOmitsBasePin pins that the canonical (stored-base) form omits
// the `- _base` pin while the emitted form carries it — the digest is computed
// over the pin-less bytes (RFC 0015 §250).
func TestRenderCanonicalOmitsBasePin(t *testing.T) {
	doc := spelling2Doc()
	if strings.Contains(renderCanonical(doc), "_base") {
		t.Error("canonical form must not contain the _base pin")
	}
	if !strings.Contains(render(doc), "- _base = @blake2b256-acdef9") {
		t.Error("emitted form must contain the _base pin")
	}
	// Both are valid hyphence envelopes with the type line.
	if !strings.Contains(renderCanonical(doc), "! organize-base-v1") {
		t.Error("canonical form must carry the ! organize-base-v1 type")
	}
}

// TestParseEditedMove pins that a hand-edited envelope document (a box moved under
// a `## =` bucket) parses to the moved assignment — the read side of apply.
func TestParseEditedMove(t *testing.T) {
	edited := `---
% generated: cg organize -group-by status caldav://host/dav/cal/
- _base = @blake2b256-deadfeed
- _anchor = caldav://host/dav/cal/
- _type = !caldav-object-v1
! organize-base-v1
---

- [task2.ics] Walk dog

# status=

## =NEEDS-ACTION

## =COMPLETED

- [task1.ics] Buy milk
`
	doc, err := parseDocument(edited)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc.BaseDigest != "blake2b256-deadfeed" {
		t.Errorf("base digest = %q", doc.BaseDigest)
	}
	if doc.Anchor != "caldav://host/dav/cal/" || doc.Type != "caldav-object-v1" {
		t.Errorf("envelope fields = anchor %q type %q", doc.Anchor, doc.Type)
	}
	asg, err := doc.assignments()
	if err != nil {
		t.Fatalf("assignments: %v", err)
	}
	if got := asg["task1.ics"]; got != "COMPLETED" {
		t.Errorf("task1 bucket = %q, want COMPLETED", got)
	}
	if got := asg["task2.ics"]; got != "" {
		t.Errorf("task2 bucket = %q, want unbucketed", got)
	}
}

// TestParseSpelling1 pins that the type-as-heading spelling parses: the `# !type`
// heading and inline `!type` boxes, with the dimension at depth 2.
func TestParseSpelling1(t *testing.T) {
	edited := `---
- _base = @blake2b256-x
- _anchor = caldav://host/dav/cal/
! organize-base-v1
---

# !caldav-object-v1

## status=

### =COMPLETED

- [task1.ics !caldav-object-v1] Buy milk
`
	doc, err := parseDocument(edited)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc.Type != "" {
		t.Errorf("spelling 1 should have no envelope _type, got %q", doc.Type)
	}
	if doc.groupedDimension() != "status" {
		t.Errorf("groupedDimension = %q", doc.groupedDimension())
	}
	asg, _ := doc.assignments()
	if asg["task1.ics"] != "COMPLETED" {
		t.Errorf("task1 bucket = %q, want COMPLETED", asg["task1.ics"])
	}
}

// TestParseObjectLine pins espalier box parsing: id, `!`-type, description trailer.
func TestParseObjectLine(t *testing.T) {
	ln, err := parseObjectLine("[task1.ics !caldav-object-v1] Buy the milk")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ln.ID != "task1.ics" || ln.Type != "caldav-object-v1" || ln.Desc != "Buy the milk" {
		t.Errorf("parsed box = %+v", ln)
	}
	bare, err := parseObjectLine("[task1.ics]")
	if err != nil {
		t.Fatalf("parse bare: %v", err)
	}
	if bare.ID != "task1.ics" || bare.Type != "" || bare.Desc != "" {
		t.Errorf("parsed bare box = %+v", bare)
	}
	if _, err := parseObjectLine("task1.ics] no open bracket"); err == nil {
		t.Error("expected an error for a box with no opening bracket")
	}
}

// TestAssignmentsRejectDuplicate pins that an object under two positions is a loud
// malformed-edit error.
func TestAssignmentsRejectDuplicate(t *testing.T) {
	doc := document{Sections: []section{
		{Depth: 1, Term: "status="},
		{Depth: 2, Term: "=A", Lines: []objectLine{{ID: "x.ics"}}},
		{Depth: 2, Term: "=B", Lines: []objectLine{{ID: "x.ics"}}},
	}}
	if _, err := doc.assignments(); err == nil {
		t.Error("expected an error for an object under two buckets")
	}
}

// TestMemberships_MultiValued pins the cardinality-aware projection for a
// multi-valued dimension: one object legally appears under several distinct
// buckets (accumulated), but the same bucket twice for one id is malformed.
func TestMemberships_MultiValued(t *testing.T) {
	doc := document{Sections: []section{
		{Depth: 1, Term: "categories="},
		{Depth: 2, Term: "=work", Lines: []objectLine{{ID: "t1.ics"}}},
		{Depth: 2, Term: "=urgent", Lines: []objectLine{{ID: "t1.ics"}}},
	}}
	m, err := doc.memberships(true)
	if err != nil {
		t.Fatalf("multi membership must be legal: %v", err)
	}
	got := append([]string(nil), m["t1.ics"]...)
	sort.Strings(got)
	if len(got) != 2 || got[0] != "urgent" || got[1] != "work" {
		t.Errorf("t1 memberships = %v, want [urgent work]", got)
	}

	dup := document{Sections: []section{
		{Depth: 1, Term: "categories="},
		{Depth: 2, Term: "=work", Lines: []objectLine{{ID: "t1.ics"}, {ID: "t1.ics"}}},
	}}
	if _, err := dup.memberships(true); err == nil {
		t.Error("same bucket twice for one id must reject")
	}
}

// TestMemberships_SingleValuedRejectsTwoBuckets pins that a single-valued
// dimension keeps the "appears twice" rejection — a second distinct bucket for
// one id is a malformed edit.
func TestMemberships_SingleValuedRejectsTwoBuckets(t *testing.T) {
	doc := document{Sections: []section{
		{Depth: 1, Term: "status="},
		{Depth: 2, Term: "=A", Lines: []objectLine{{ID: "x.ics"}}},
		{Depth: 2, Term: "=B", Lines: []objectLine{{ID: "x.ics"}}},
	}}
	if _, err := doc.memberships(false); err == nil {
		t.Error("single-valued object under two buckets must reject")
	}
}

// TestMemberships_Ungrouped pins that an object above the first heading yields an
// empty membership set (present in the map, no value), never an error.
func TestMemberships_Ungrouped(t *testing.T) {
	doc := document{
		Ungrouped: []objectLine{{ID: "u.ics", Desc: "loose"}},
		Sections: []section{
			{Depth: 1, Term: "categories="},
			{Depth: 2, Term: "=work", Lines: []objectLine{{ID: "t1.ics"}}},
		},
	}
	m, err := doc.memberships(true)
	if err != nil {
		t.Fatalf("ungrouped membership must be legal: %v", err)
	}
	got, present := m["u.ics"]
	if !present {
		t.Error("ungrouped object must be present in the membership map")
	}
	if len(got) != 0 {
		t.Errorf("ungrouped memberships = %v, want empty", got)
	}
}

// TestMemberships_UngroupedPlusBucketedRejects pins that an id appearing BOTH
// ungrouped and under a bucket is a malformed edit in both cardinality modes —
// the occupancy check must fire even though the ungrouped placement carries no
// bucket payload.
func TestMemberships_UngroupedPlusBucketedRejects(t *testing.T) {
	mk := func() document {
		return document{
			Ungrouped: []objectLine{{ID: "t1.ics"}},
			Sections: []section{
				{Depth: 1, Term: "categories="},
				{Depth: 2, Term: "=work", Lines: []objectLine{{ID: "t1.ics"}}},
			},
		}
	}
	if _, err := mk().memberships(true); err == nil {
		t.Error("multi: object both ungrouped and bucketed must reject")
	}
	if _, err := mk().memberships(false); err == nil {
		t.Error("single: object both ungrouped and bucketed must reject")
	}
}

// TestMemberships_DuplicateUngroupedRejects pins that two identical ungrouped
// lines for one id reject rather than silently dedupe.
func TestMemberships_DuplicateUngroupedRejects(t *testing.T) {
	doc := document{Ungrouped: []objectLine{{ID: "t1.ics"}, {ID: "t1.ics"}}}
	if _, err := doc.memberships(true); err == nil {
		t.Error("duplicate ungrouped line for one id must reject")
	}
}

// TestRelativeID pins form-independent id shortening: same-spelling prefix,
// cross-spelling (caldav:https:// anchor vs caldav:// node URI), and unrelated.
func TestRelativeID(t *testing.T) {
	if got := relativeID("caldav:http://h/dav/cal/task1.ics", "caldav:http://h/dav/cal/"); got != "task1.ics" {
		t.Errorf("relativeID same-form = %q, want task1.ics", got)
	}
	if got := relativeID(
		"caldav://caldav.fastmail.com/dav/cal/x.ics",
		"caldav:https://caldav.fastmail.com/dav/cal/",
	); got != "x.ics" {
		t.Errorf("relativeID cross-form = %q, want x.ics", got)
	}
	if got := relativeID("caldav://other/y.ics", "caldav://host/cal/"); got != "caldav://other/y.ics" {
		t.Errorf("relativeID unrelated = %q, want full URI", got)
	}
}
