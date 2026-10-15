package ical

import (
	"slices"
	"testing"
)

// Several CATEGORIES lines accumulate (RFC 5545 §3.8.1.2): Fastmail stores
// one line per category, and a parser that overwrote kept only the last.
func TestParse_RepeatedCategoriesLinesAccumulate(t *testing.T) {
	lines := "CATEGORIES:etsy-layoff\nCATEGORIES:req-phone,work\n"
	want := []string{"etsy-layoff", "req-phone", "work"}

	task, err := ParseVTODO("BEGIN:VCALENDAR\nBEGIN:VTODO\nUID:t\n" + lines + "END:VTODO\nEND:VCALENDAR\n")
	if err != nil || !slices.Equal(task.Categories, want) {
		t.Errorf("VTODO categories = %v (err %v), want %v", task.Categories, err, want)
	}
	event, err := ParseVEVENT("BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:e\n" + lines + "END:VEVENT\nEND:VCALENDAR\n")
	if err != nil || !slices.Equal(event.Categories, want) {
		t.Errorf("VEVENT categories = %v (err %v), want %v", event.Categories, err, want)
	}
	journal, err := ParseVJOURNAL("BEGIN:VCALENDAR\nBEGIN:VJOURNAL\nUID:j\n" + lines + "END:VJOURNAL\nEND:VCALENDAR\n")
	if err != nil || !slices.Equal(journal.Categories, want) {
		t.Errorf("VJOURNAL categories = %v (err %v), want %v", journal.Categories, err, want)
	}
}
