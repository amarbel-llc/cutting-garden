package ical

import (
	"strings"
	"testing"
)

// TestParseAllVEVENTs_MultipleComponentsShareOneCalendarData pins
// cutting-garden#176/#177's decisive parsing requirement: a <C:expand>
// response packs several occurrence VEVENT components (own DTSTART, own
// RECURRENCE-ID, no RRULE) sharing one UID into a SINGLE calendar-data
// blob. ParseAllVEVENTs must recover every one, in document order —
// unlike ParseVEVENT, which (by design, for capture identity) only ever
// sees the first.
func TestParseAllVEVENTs_MultipleComponentsShareOneCalendarData(t *testing.T) {
	raw := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:therapy
SUMMARY:Therapy
DTSTART:20260730T132000Z
RECURRENCE-ID:20260730T132000Z
END:VEVENT
BEGIN:VEVENT
UID:therapy
SUMMARY:Therapy
DTSTART:20260806T132000Z
RECURRENCE-ID:20260806T132000Z
END:VEVENT
BEGIN:VEVENT
UID:therapy
SUMMARY:Therapy
DTSTART:20260813T132000Z
RECURRENCE-ID:20260813T132000Z
END:VEVENT
END:VCALENDAR`

	events, err := ParseAllVEVENTs(raw)
	if err != nil {
		t.Fatalf("ParseAllVEVENTs: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3: %+v", len(events), events)
	}
	wantStarts := []string{"20260730T132000Z", "20260806T132000Z", "20260813T132000Z"}
	for i, e := range events {
		if e.UID != "therapy" {
			t.Errorf("event[%d].UID = %q, want therapy", i, e.UID)
		}
		if e.RRule != "" {
			t.Errorf("event[%d].RRule = %q, want empty (expand strips it)", i, e.RRule)
		}
		if e.DtStart != wantStarts[i] {
			t.Errorf("event[%d].DtStart = %q, want %q", i, e.DtStart, wantStarts[i])
		}
		if e.RecurrenceID != wantStarts[i] {
			t.Errorf("event[%d].RecurrenceID = %q, want %q", i, e.RecurrenceID, wantStarts[i])
		}
	}
}

// TestParseAllVEVENTs_NoVEVENTIsEmptyNotError pins the "contributes
// nothing" degenerate case: a body with no VEVENT component (e.g. an
// all-VTODO calendar-data projection) returns an empty, non-error slice.
func TestParseAllVEVENTs_NoVEVENTIsEmptyNotError(t *testing.T) {
	events, err := ParseAllVEVENTs("BEGIN:VCALENDAR\nEND:VCALENDAR\n")
	if err != nil {
		t.Fatalf("ParseAllVEVENTs: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("got %d events, want 0: %+v", len(events), events)
	}
}

// TestParseAllVEVENTs_SkipsMalformedComponent pins that one bad
// occurrence (missing UID, ParseVEVENT's own validity gate) does not hide
// the others — a single degraded component in an otherwise-good expand
// response should not make the whole call fail.
func TestParseAllVEVENTs_SkipsMalformedComponent(t *testing.T) {
	raw := `BEGIN:VCALENDAR
BEGIN:VEVENT
SUMMARY:No UID here
DTSTART:20260730T132000Z
END:VEVENT
BEGIN:VEVENT
UID:good
SUMMARY:Fine
DTSTART:20260806T132000Z
END:VEVENT
END:VCALENDAR`

	events, err := ParseAllVEVENTs(raw)
	if err != nil {
		t.Fatalf("ParseAllVEVENTs: %v", err)
	}
	if len(events) != 1 || events[0].UID != "good" {
		t.Fatalf("events = %+v, want exactly the one valid component", events)
	}
}

func TestParseVEVENT(t *testing.T) {
	raw := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//Test//EN
BEGIN:VEVENT
DTSTAMP:20260315T120000Z
UID:event-test-1
CREATED:20260315T100000Z
LAST-MODIFIED:20260315T110000Z
SUMMARY:Team standup
DESCRIPTION:Daily standup meeting
DTSTART;TZID=America/New_York:20260401T093000
DTEND;TZID=America/New_York:20260401T100000
LOCATION:Conference Room A
CATEGORIES:Work,Meetings
STATUS:CONFIRMED
TRANSP:OPAQUE
ORGANIZER:mailto:boss@example.com
ATTENDEE:mailto:alice@example.com
ATTENDEE:mailto:bob@example.com
RRULE:FREQ=DAILY;BYDAY=MO,TU,WE,TH,FR
SEQUENCE:2
BEGIN:VALARM
TRIGGER:-PT15M
ACTION:DISPLAY
DESCRIPTION:Reminder
END:VALARM
END:VEVENT
END:VCALENDAR`

	event, err := ParseVEVENT(raw)
	if err != nil {
		t.Fatalf("ParseVEVENT: %v", err)
	}

	if event.UID != "event-test-1" {
		t.Errorf("UID = %q, want %q", event.UID, "event-test-1")
	}
	if event.Summary != "Team standup" {
		t.Errorf("Summary = %q, want %q", event.Summary, "Team standup")
	}
	if event.Description != "Daily standup meeting" {
		t.Errorf("Description = %q, want %q", event.Description, "Daily standup meeting")
	}
	if event.DtStart != "20260401T093000" {
		t.Errorf("DtStart = %q, want %q", event.DtStart, "20260401T093000")
	}
	if event.DtEnd != "20260401T100000" {
		t.Errorf("DtEnd = %q, want %q", event.DtEnd, "20260401T100000")
	}
	if event.Location != "Conference Room A" {
		t.Errorf("Location = %q, want %q", event.Location, "Conference Room A")
	}
	if event.Status != "CONFIRMED" {
		t.Errorf("Status = %q, want %q", event.Status, "CONFIRMED")
	}
	if event.Transp != "OPAQUE" {
		t.Errorf("Transp = %q, want %q", event.Transp, "OPAQUE")
	}
	if event.Organizer != "mailto:boss@example.com" {
		t.Errorf("Organizer = %q", event.Organizer)
	}
	if len(event.Attendees) != 2 {
		t.Fatalf("len(Attendees) = %d, want 2", len(event.Attendees))
	}
	if event.RRule != "FREQ=DAILY;BYDAY=MO,TU,WE,TH,FR" {
		t.Errorf("RRule = %q", event.RRule)
	}
	if len(event.Categories) != 2 || event.Categories[0] != "Work" {
		t.Errorf("Categories = %v", event.Categories)
	}
	if event.Sequence != 2 {
		t.Errorf("Sequence = %d, want 2", event.Sequence)
	}
	if len(event.Alarms) != 1 {
		t.Fatalf("len(Alarms) = %d, want 1", len(event.Alarms))
	}
	if event.Alarms[0].Trigger != "-PT15M" {
		t.Errorf("Alarm.Trigger = %q", event.Alarms[0].Trigger)
	}
	if !event.HasDescription {
		t.Error("HasDescription = false, want true")
	}
}

func TestParseVEVENT_Minimal(t *testing.T) {
	raw := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:minimal-event
SUMMARY:Quick chat
DTSTART:20260401T140000Z
END:VEVENT
END:VCALENDAR`

	event, err := ParseVEVENT(raw)
	if err != nil {
		t.Fatalf("ParseVEVENT: %v", err)
	}

	if event.UID != "minimal-event" {
		t.Errorf("UID = %q", event.UID)
	}
	if event.Summary != "Quick chat" {
		t.Errorf("Summary = %q", event.Summary)
	}
	if event.HasDescription {
		t.Error("HasDescription = true, want false")
	}
}

func TestParseVEVENT_NoVEVENT(t *testing.T) {
	raw := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VTODO
UID:task1
SUMMARY:A task
END:VTODO
END:VCALENDAR`

	_, err := ParseVEVENT(raw)
	if err == nil {
		t.Error("expected error for missing VEVENT, got nil")
	}
}

func TestParseVEVENT_Duration(t *testing.T) {
	raw := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:duration-event
SUMMARY:Workshop
DTSTART:20260401T090000Z
DURATION:PT2H30M
END:VEVENT
END:VCALENDAR`

	event, err := ParseVEVENT(raw)
	if err != nil {
		t.Fatalf("ParseVEVENT: %v", err)
	}

	if event.Duration != "PT2H30M" {
		t.Errorf("Duration = %q, want %q", event.Duration, "PT2H30M")
	}
}

func TestEventToMetadata(t *testing.T) {
	event := &Event{
		UID:               "uid-1",
		Summary:           "Meeting",
		Status:            "CONFIRMED",
		DtStart:           "20260401T090000Z",
		DtEnd:             "20260401T100000Z",
		Location:          "Room A",
		Categories:        []string{"Work"},
		RRule:             "FREQ=WEEKLY",
		HasDescription:    true,
		DescriptionTokens: 25,
		Description:       "some long description",
		Organizer:         "mailto:boss@example.com",
	}

	meta := event.ToMetadata()
	if meta.UID != "uid-1" {
		t.Errorf("UID = %q", meta.UID)
	}
	if meta.Location != "Room A" {
		t.Errorf("Location = %q", meta.Location)
	}
	if meta.RRule != "FREQ=WEEKLY" {
		t.Errorf("RRule = %q", meta.RRule)
	}
	if !meta.HasDescription {
		t.Error("HasDescription should be true")
	}
}

func TestParseVEVENT_FoldedLines(t *testing.T) {
	raw := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:fold-test\r\nSUMMARY:This is a\r\n  folded summary\r\nDTSTART:20260401T090000Z\r\nEND:VEVENT\r\nEND:VCALENDAR"

	event, err := ParseVEVENT(raw)
	if err != nil {
		t.Fatalf("ParseVEVENT: %v", err)
	}

	if event.Summary != "This is a folded summary" {
		t.Errorf("Summary = %q, want %q", event.Summary, "This is a folded summary")
	}
}

func TestEventToIcal(t *testing.T) {
	event := &Event{
		UID:         "ical-test-1",
		Summary:     "Team lunch",
		Description: "Quarterly team lunch",
		Status:      "CONFIRMED",
		DtStart:     "20260401T120000Z",
		DtEnd:       "20260401T133000Z",
		Location:    "Downtown Grill",
		Categories:  []string{"Work", "Social"},
		RRule:       "FREQ=MONTHLY;BYDAY=1FR",
		Transp:      "OPAQUE",
		Sequence:    1,
		Alarms: []Alarm{
			{Trigger: "-PT30M", Action: "DISPLAY", Description: "Lunch soon"},
		},
	}

	result := EventToIcal(event)

	checks := []string{
		"BEGIN:VCALENDAR",
		"BEGIN:VEVENT",
		"UID:ical-test-1",
		"SUMMARY:Team lunch",
		"DESCRIPTION:Quarterly team lunch",
		"STATUS:CONFIRMED",
		"DTSTART:20260401T120000Z",
		"DTEND:20260401T133000Z",
		"LOCATION:Downtown Grill",
		"CATEGORIES:Work,Social",
		"RRULE:FREQ=MONTHLY;BYDAY=1FR",
		"TRANSP:OPAQUE",
		"SEQUENCE:1",
		"BEGIN:VALARM",
		"TRIGGER:-PT30M",
		"END:VEVENT",
		"END:VCALENDAR",
	}
	for _, want := range checks {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q in output:\n%s", want, result)
		}
	}
}

func TestEventToIcal_RoundTrip(t *testing.T) {
	original := &Event{
		UID:         "roundtrip-evt-1",
		Summary:     "Round trip event",
		Description: "Testing serialize then parse",
		Status:      "CONFIRMED",
		DtStart:     "20260401T090000Z",
		DtEnd:       "20260401T100000Z",
		Location:    "Office",
		Categories:  []string{"test", "roundtrip"},
		RRule:       "FREQ=WEEKLY;COUNT=4",
		Transp:      "OPAQUE",
		Sequence:    2,
	}

	icalStr := EventToIcal(original)
	parsed, err := ParseVEVENT(icalStr)
	if err != nil {
		t.Fatalf("ParseVEVENT: %v", err)
	}

	if parsed.UID != original.UID {
		t.Errorf("UID = %q, want %q", parsed.UID, original.UID)
	}
	if parsed.Summary != original.Summary {
		t.Errorf("Summary = %q, want %q", parsed.Summary, original.Summary)
	}
	if parsed.Description != original.Description {
		t.Errorf("Description = %q, want %q", parsed.Description, original.Description)
	}
	if parsed.Status != original.Status {
		t.Errorf("Status = %q, want %q", parsed.Status, original.Status)
	}
	if parsed.DtStart != original.DtStart {
		t.Errorf("DtStart = %q, want %q", parsed.DtStart, original.DtStart)
	}
	if parsed.DtEnd != original.DtEnd {
		t.Errorf("DtEnd = %q, want %q", parsed.DtEnd, original.DtEnd)
	}
	if parsed.Location != original.Location {
		t.Errorf("Location = %q, want %q", parsed.Location, original.Location)
	}
	if parsed.RRule != original.RRule {
		t.Errorf("RRule = %q, want %q", parsed.RRule, original.RRule)
	}
	if parsed.Transp != original.Transp {
		t.Errorf("Transp = %q, want %q", parsed.Transp, original.Transp)
	}
	if parsed.Sequence != original.Sequence {
		t.Errorf("Sequence = %d, want %d", parsed.Sequence, original.Sequence)
	}
}
