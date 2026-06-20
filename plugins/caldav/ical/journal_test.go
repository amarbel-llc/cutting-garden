package ical

import (
	"strings"
	"testing"
)

func TestParseVJOURNAL(t *testing.T) {
	raw := "BEGIN:VCALENDAR\nVERSION:2.0\n" +
		"BEGIN:VJOURNAL\nUID:note1\nSUMMARY:Trip log\n" +
		"DESCRIPTION:Day one notes\nSTATUS:FINAL\nDTSTART:20260224\n" +
		"CATEGORIES:travel,personal\nEND:VJOURNAL\nEND:VCALENDAR\n"

	j, err := ParseVJOURNAL(raw)
	if err != nil {
		t.Fatalf("ParseVJOURNAL: %v", err)
	}
	if j.UID != "note1" || j.Summary != "Trip log" || j.Description != "Day one notes" {
		t.Errorf("parsed = %+v", j)
	}
	if j.Status != "FINAL" || j.DtStart != "20260224" {
		t.Errorf("status/dtstart = %q/%q", j.Status, j.DtStart)
	}
	if len(j.Categories) != 2 || j.Categories[0] != "travel" || j.Categories[1] != "personal" {
		t.Errorf("categories = %v", j.Categories)
	}
	if !j.HasDescription {
		t.Error("HasDescription should be true")
	}
}

func TestParseVJOURNAL_MissingUID(t *testing.T) {
	raw := "BEGIN:VCALENDAR\nBEGIN:VJOURNAL\nSUMMARY:x\nEND:VJOURNAL\nEND:VCALENDAR\n"
	if _, err := ParseVJOURNAL(raw); err == nil {
		t.Error("a VJOURNAL without a UID must error")
	}
}

func TestJournalToIcal_RoundTrip(t *testing.T) {
	in := &Journal{
		UID:         "n1",
		Summary:     "Standup notes",
		Description: "discussed the release",
		Status:      "FINAL",
		Categories:  []string{"work"},
	}
	out := JournalToIcal(in)
	if !strings.Contains(out, "BEGIN:VJOURNAL") || !strings.Contains(out, "SUMMARY:Standup notes") {
		t.Fatalf("serialized = %q", out)
	}

	parsed, err := ParseVJOURNAL(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if parsed.UID != in.UID || parsed.Summary != in.Summary ||
		parsed.Description != in.Description || parsed.Status != in.Status {
		t.Errorf("round-trip lost fields: %+v", parsed)
	}
}

func TestJournalToMetadata(t *testing.T) {
	j := &Journal{UID: "n", Summary: "S", Status: "DRAFT", HasDescription: true}
	m := j.ToMetadata()
	if m.UID != "n" || m.Summary != "S" || m.Status != "DRAFT" || !m.HasDescription {
		t.Errorf("metadata = %+v", m)
	}
}
