package ical

import (
	"strings"
	"testing"
)

// TestParseVTODO_TZIDRetained pins #141's parser half: the TZID
// parameter on DUE/DTSTART survives parsing (it was previously
// discarded with all property parameters) and re-attaches on
// serialization, so mutation round-trips no longer strip an object's
// zone.
func TestParseVTODO_TZIDRetained(t *testing.T) {
	raw := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VTODO\r\n" +
		"UID:tz1\r\nSUMMARY:EOD Berlin\r\n" +
		"DUE;TZID=Europe/Berlin:20260718T235900\r\n" +
		"DTSTART;TZID=Europe/Berlin:20260718T090000\r\n" +
		"END:VTODO\r\nEND:VCALENDAR\r\n"

	task, err := ParseVTODO(raw)
	if err != nil {
		t.Fatal(err)
	}
	if task.Due != "20260718T235900" || task.DueTZID != "Europe/Berlin" {
		t.Errorf("Due = %q TZID %q, want value + Europe/Berlin",
			task.Due, task.DueTZID)
	}
	if task.DtStartTZID != "Europe/Berlin" {
		t.Errorf("DtStartTZID = %q, want Europe/Berlin", task.DtStartTZID)
	}

	out := TaskToIcal(task)
	if !strings.Contains(out, "DUE;TZID=Europe/Berlin:20260718T235900\r\n") {
		t.Errorf("serialization dropped DUE TZID:\n%s", out)
	}
	if !strings.Contains(out, "DTSTART;TZID=Europe/Berlin:20260718T090000\r\n") {
		t.Errorf("serialization dropped DTSTART TZID:\n%s", out)
	}
}

// TestParseVTODO_NoTZIDStaysBare pins the common case: zone-free values
// parse with empty TZID fields and serialize without a TZID parameter
// (and date-only values keep VALUE=DATE, where a TZID would be invalid
// per RFC 5545).
func TestParseVTODO_NoTZIDStaysBare(t *testing.T) {
	raw := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VTODO\r\n" +
		"UID:tz2\r\nSUMMARY:Plain\r\nDUE:20260718\r\n" +
		"END:VTODO\r\nEND:VCALENDAR\r\n"

	task, err := ParseVTODO(raw)
	if err != nil {
		t.Fatal(err)
	}
	if task.DueTZID != "" {
		t.Errorf("DueTZID = %q, want empty", task.DueTZID)
	}

	out := TaskToIcal(task)
	if !strings.Contains(out, "DUE;VALUE=DATE:20260718\r\n") {
		t.Errorf("date-only DUE lost VALUE=DATE form:\n%s", out)
	}
	if strings.Contains(out, "TZID") {
		t.Errorf("zone-free serialization emitted a TZID:\n%s", out)
	}
}

// TestParseVEVENT_TZIDRetained pins the event side symmetrically.
func TestParseVEVENT_TZIDRetained(t *testing.T) {
	raw := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\n" +
		"UID:tz3\r\nSUMMARY:Standup\r\n" +
		"DTSTART;TZID=Asia/Tokyo:20260719T093000\r\n" +
		"DTEND;TZID=Asia/Tokyo:20260719T100000\r\n" +
		"END:VEVENT\r\nEND:VCALENDAR\r\n"

	event, err := ParseVEVENT(raw)
	if err != nil {
		t.Fatal(err)
	}
	if event.DtStartTZID != "Asia/Tokyo" || event.DtEndTZID != "Asia/Tokyo" {
		t.Errorf("event TZIDs = %q/%q, want Asia/Tokyo both",
			event.DtStartTZID, event.DtEndTZID)
	}

	out := EventToIcal(event)
	if !strings.Contains(out, "DTSTART;TZID=Asia/Tokyo:20260719T093000\r\n") {
		t.Errorf("serialization dropped DTSTART TZID:\n%s", out)
	}
}
