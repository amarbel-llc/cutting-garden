package organize

import (
	"strings"
	"testing"
)

func baseTestDocument(provenance string) document {
	return document{
		Provenance: provenance,
		Anchor:     "caldav://host/dav/cal/",
		Type:       "caldav-object-v1",
		GroupBy:    "(tags)",
		Sections: []section{{
			Depth: 1, Term: "work",
			Lines: []objectLine{{ID: "a.ics", Desc: "A"}},
		}},
	}
}

// TestRenderCanonical_ExcludesProvenance pins RFC 0015 §`%` / `%:` lines: the
// `%` provenance comment is stripped from the base, so a document generated
// with, without, or with a different provenance note has the same base bytes
// (and so the same `_base` digest) — while the emitted document still carries
// the note.
func TestRenderCanonical_ExcludesProvenance(t *testing.T) {
	with := baseTestDocument("generated: `cg organize x (tags)`")
	other := baseTestDocument("generated: `cg organize 'x -> y' '(tags)'`")
	without := baseTestDocument("")

	if renderCanonical(with) != renderCanonical(without) ||
		renderCanonical(other) != renderCanonical(without) {
		t.Errorf("provenance leaked into the base:\nwith:\n%s\nwithout:\n%s",
			renderCanonical(with), renderCanonical(without))
	}
	if strings.Contains(renderCanonical(with), "generated:") {
		t.Errorf("the base blob must not carry the provenance line:\n%s", renderCanonical(with))
	}
	if !strings.Contains(render(with), "% generated: `cg organize x (tags)`\n") {
		t.Errorf("the emitted document must still carry the provenance:\n%s", render(with))
	}
}

// TestRenderCanonical_SameBaseSameLedgerKey pins the consequence the ledger
// sees: the creation idempotency key derives from the `_base` string alone, so
// two spellings of one selection (same base) share a key for one temp id, and a
// real data difference still separates them.
func TestRenderCanonical_SameBaseSameLedgerKey(t *testing.T) {
	a, b := baseTestDocument("one"), baseTestDocument("two")
	if renderCanonical(a) != renderCanonical(b) {
		t.Fatalf("precondition: same data must share a base")
	}
	if creationIdempotencyKey("blake2b256-same", "+x") != creationIdempotencyKey("blake2b256-same", "+x") {
		t.Errorf("a creation key must be a pure function of (base, temp id)")
	}
	if creationIdempotencyKey("blake2b256-one", "+x") == creationIdempotencyKey("blake2b256-two", "+x") {
		t.Errorf("a different base must give a different creation key")
	}
	c := baseTestDocument("one")
	c.Sections[0].Lines[0].Desc = "changed data"
	if renderCanonical(a) == renderCanonical(c) {
		t.Errorf("a data difference must still change the base")
	}
}

// TestRender_MultiLineProvenanceRoundTrips pins that every provenance line is
// its own `%` comment: the old render prefixed only the first, so line two
// re-rendered as a non-comment and broke the reparse.
func TestRender_MultiLineProvenanceRoundTrips(t *testing.T) {
	doc := baseTestDocument("first line\nsecond line")
	out := render(doc)
	if !strings.Contains(out, "% first line\n% second line\n") {
		t.Fatalf("each provenance line needs its own percent marker:\n%s", out)
	}
	got, err := parseDocument(out)
	if err != nil {
		t.Fatalf("reparse: %v\n%s", err, out)
	}
	if got.Provenance != doc.Provenance {
		t.Errorf("provenance = %q, want %q", got.Provenance, doc.Provenance)
	}
	if render(got) != out {
		t.Errorf("must round-trip byte-identically:\n%s\nvs\n%s", out, render(got))
	}
}

// TestBodyMatchesBase pins fmt-organize's clean-body gate: a pre-existing
// pinned document whose stored base blob still carries the provenance line (the
// old canonical form) is NOT falsely refused, while a real body edit is.
func TestBodyMatchesBase(t *testing.T) {
	doc := baseTestDocument("generated: `cg organize x (tags)`")

	legacyBlob := render(doc) // the old canonical form: provenance inside the base
	if !strings.Contains(legacyBlob, "% generated:") {
		t.Fatalf("precondition: the legacy blob carries the provenance line")
	}
	clean, err := bodyMatchesBase(doc, legacyBlob)
	if err != nil || !clean {
		t.Errorf("legacy pinned blob refused: clean=%v err=%v", clean, err)
	}

	current := renderCanonical(doc)
	if clean, err := bodyMatchesBase(doc, current); err != nil || !clean {
		t.Errorf("current base refused: clean=%v err=%v", clean, err)
	}

	edited := baseTestDocument("generated: `cg organize x (tags)`")
	edited.Sections[0].Lines[0].Desc = "edited"
	if clean, err := bodyMatchesBase(edited, legacyBlob); err != nil || clean {
		t.Errorf("an edited body must not match the legacy base: clean=%v err=%v", clean, err)
	}
	if clean, err := bodyMatchesBase(edited, current); err != nil || clean {
		t.Errorf("an edited body must not match the current base: clean=%v err=%v", clean, err)
	}

	if _, err := bodyMatchesBase(doc, "not a document"); err == nil {
		t.Errorf("an unparseable base blob must be an error")
	}
}
