package ical

import (
	"reflect"
	"strings"
	"testing"
)

func TestEscapeText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"Plan, then do", `Plan\, then do`},
		{"a;b", `a\;b`},
		{`back\slash`, `back\\slash`},
		{"line1\nline2", `line1\nline2`},
		{"crlf\r\nline", `crlf\nline`},
		{"bare\rcr", `bare\ncr`},
		{`\;,` + "\n", `\\\;\,\n`},
		{"", ""},
	}
	for _, tc := range cases {
		if got := escapeText(tc.in); got != tc.want {
			t.Errorf("escapeText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestUnescapeText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{`Plan\, then do`, "Plan, then do"},
		{`a\;b`, "a;b"},
		{`back\\slash`, `back\slash`},
		{`line1\nline2`, "line1\nline2"},
		{`line1\Nline2`, "line1\nline2"},
		// In TEXT context `\\` is an escaped backslash and the following bare
		// comma passes through: 5 wire chars decode to the 4 chars `a\,b` —
		// distinct from CATEGORIES context, where that comma separates.
		{`a\\,b`, `a\,b`},
		// Unknown escapes and a trailing lone backslash survive verbatim.
		{`odd\xescape`, `odd\xescape`},
		{`trailing\`, `trailing\`},
		{"", ""},
	}
	for _, tc := range cases {
		if got := unescapeText(tc.in); got != tc.want {
			t.Errorf("unescapeText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestEscapeUnescapeRoundTrip pins unescape(escape(x)) == x for every rune the
// escaping touches, including a real newline.
func TestEscapeUnescapeRoundTrip(t *testing.T) {
	for _, s := range []string{
		"Plan, then do",
		"a;b;c",
		`C:\path\to`,
		"multi\nline\nvalue",
		`all of them: \ ; ,` + "\n" + "together",
	} {
		if got := unescapeText(escapeText(s)); got != s {
			t.Errorf("unescape(escape(%q)) = %q", s, got)
		}
	}
}

func TestParseCategories(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"work", []string{"work"}},
		{"work,errand", []string{"work", "errand"}},
		{"Test tag, Work", []string{"Test tag", "Work"}},
		// An escaped comma is INSIDE one category, not a separator.
		{`planning\, misc`, []string{"planning, misc"}},
		{`planning\, misc,work`, []string{"planning, misc", "work"}},
		// `\\,` is an escaped backslash FOLLOWED BY a separator comma.
		{`a\\,b`, []string{`a\`, "b"}},
		{`sem\;colon`, []string{"sem;colon"}},
	}
	for _, tc := range cases {
		if got := parseCategories(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parseCategories(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestFormatCategories(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"work", "errand"}, "work,errand"},
		{[]string{"planning, misc"}, `planning\, misc`},
		{[]string{"planning, misc", "work"}, `planning\, misc,work`},
		{[]string{`a\`, "b"}, `a\\,b`},
	}
	for _, tc := range cases {
		if got := formatCategories(tc.in); got != tc.want {
			t.Errorf("formatCategories(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestParseVTODO_TextEscaping pins the user-reported bug (live UAT 2026-09-02):
// a stored `SUMMARY:Plan\, then do` must reach the struct as "Plan, then do" —
// the escaping is wire-format only and invisible above the ical layer.
func TestParseVTODO_TextEscaping(t *testing.T) {
	raw := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VTODO\r\n" +
		"UID:esc1\r\n" +
		`SUMMARY:Plan\, then do` + "\r\n" +
		`DESCRIPTION:Line1\nLine2\; done` + "\r\n" +
		`LOCATION:Cafe\\Bar` + "\r\n" +
		`CATEGORIES:planning\, misc,work` + "\r\n" +
		"STATUS:NEEDS-ACTION\r\n" +
		"END:VTODO\r\nEND:VCALENDAR\r\n"

	task, err := ParseVTODO(raw)
	if err != nil {
		t.Fatalf("ParseVTODO: %v", err)
	}
	if task.Summary != "Plan, then do" {
		t.Errorf("Summary = %q, want %q", task.Summary, "Plan, then do")
	}
	if task.Description != "Line1\nLine2; done" {
		t.Errorf("Description = %q, want %q", task.Description, "Line1\nLine2; done")
	}
	if task.Location != `Cafe\Bar` {
		t.Errorf("Location = %q, want %q", task.Location, `Cafe\Bar`)
	}
	if want := []string{"planning, misc", "work"}; !reflect.DeepEqual(task.Categories, want) {
		t.Errorf("Categories = %v, want %v", task.Categories, want)
	}
	// A non-TEXT enum property is untouched.
	if task.Status != "NEEDS-ACTION" {
		t.Errorf("Status = %q, want NEEDS-ACTION", task.Status)
	}
}

// TestTaskToIcal_TextEscaping pins the write side: struct values carrying
// reserved runes serialize with RFC 5545 escaping, and parse(write(x)) == x.
func TestTaskToIcal_TextEscaping(t *testing.T) {
	original := &Task{
		UID:         "esc-rt",
		Summary:     "Plan, then do",
		Description: "Line1\nLine2; done",
		Location:    `Cafe\Bar`,
		Categories:  []string{"planning, misc", "work"},
	}

	icalStr := TaskToIcal(original)
	if !strings.Contains(icalStr, `SUMMARY:Plan\, then do`+"\r\n") {
		t.Errorf("serialized SUMMARY not escaped:\n%s", icalStr)
	}
	if !strings.Contains(icalStr, `DESCRIPTION:Line1\nLine2\; done`+"\r\n") {
		t.Errorf("serialized DESCRIPTION not escaped:\n%s", icalStr)
	}
	if !strings.Contains(icalStr, `LOCATION:Cafe\\Bar`+"\r\n") {
		t.Errorf("serialized LOCATION not escaped:\n%s", icalStr)
	}
	if !strings.Contains(icalStr, `CATEGORIES:planning\, misc,work`+"\r\n") {
		t.Errorf("serialized CATEGORIES not escaped:\n%s", icalStr)
	}

	parsed, err := ParseVTODO(icalStr)
	if err != nil {
		t.Fatalf("ParseVTODO: %v", err)
	}
	if parsed.Summary != original.Summary {
		t.Errorf("round-trip Summary = %q, want %q", parsed.Summary, original.Summary)
	}
	if parsed.Description != original.Description {
		t.Errorf("round-trip Description = %q, want %q", parsed.Description, original.Description)
	}
	if parsed.Location != original.Location {
		t.Errorf("round-trip Location = %q, want %q", parsed.Location, original.Location)
	}
	if !reflect.DeepEqual(parsed.Categories, original.Categories) {
		t.Errorf("round-trip Categories = %v, want %v", parsed.Categories, original.Categories)
	}
}

// TestEventJournalTextEscapingRoundTrip pins the same escape/unescape symmetry
// on the VEVENT and VJOURNAL writers/parsers.
func TestEventJournalTextEscapingRoundTrip(t *testing.T) {
	e := &Event{
		UID:        "esc-ev",
		Summary:    "Meet; discuss, decide",
		Location:   "Room 1, Floor 2",
		Categories: []string{"a, b"},
	}
	pe, err := ParseVEVENT(EventToIcal(e))
	if err != nil {
		t.Fatalf("ParseVEVENT: %v", err)
	}
	if pe.Summary != e.Summary || pe.Location != e.Location ||
		!reflect.DeepEqual(pe.Categories, e.Categories) {
		t.Errorf("VEVENT round-trip = %+v, want %+v", pe, e)
	}

	j := &Journal{
		UID:         "esc-jo",
		Summary:     "Note, with comma",
		Description: "one\ntwo",
		Categories:  []string{"x;y"},
	}
	pj, err := ParseVJOURNAL(JournalToIcal(j))
	if err != nil {
		t.Fatalf("ParseVJOURNAL: %v", err)
	}
	if pj.Summary != j.Summary || pj.Description != j.Description ||
		!reflect.DeepEqual(pj.Categories, j.Categories) {
		t.Errorf("VJOURNAL round-trip = %+v, want %+v", pj, j)
	}
}
