package ical

import (
	"strings"
	"testing"
)

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
