package ical

import (
	"strings"
	"testing"
)

func TestParseVTODO(t *testing.T) {
	raw := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:+//IDN tasks.org//android-120500//EN
BEGIN:VTODO
DTSTAMP:20220303T182218Z
UID:3071754526080170849
CREATED:20220303T182204Z
LAST-MODIFIED:20220303T182215Z
SUMMARY:Test task
DESCRIPTION:This is a test description
CATEGORIES:Test tag,Work
PRIORITY:5
STATUS:NEEDS-ACTION
DUE;VALUE=DATE:20220303
X-APPLE-SORT-ORDER:559033419
RELATED-TO;RELTYPE=PARENT:9999999
BEGIN:VALARM
TRIGGER;RELATED=END:PT0S
ACTION:DISPLAY
DESCRIPTION:Default Tasks.org description
END:VALARM
END:VTODO
END:VCALENDAR`

	task, err := ParseVTODO(raw)
	if err != nil {
		t.Fatalf("ParseVTODO: %v", err)
	}

	if task.UID != "3071754526080170849" {
		t.Errorf("UID = %q, want %q", task.UID, "3071754526080170849")
	}
	if task.Summary != "Test task" {
		t.Errorf("Summary = %q, want %q", task.Summary, "Test task")
	}
	if task.Description != "This is a test description" {
		t.Errorf("Description = %q, want %q", task.Description, "This is a test description")
	}
	if task.Status != "NEEDS-ACTION" {
		t.Errorf("Status = %q, want %q", task.Status, "NEEDS-ACTION")
	}
	if task.Priority != 5 {
		t.Errorf("Priority = %d, want %d", task.Priority, 5)
	}
	if task.Due != "20220303" {
		t.Errorf("Due = %q, want %q", task.Due, "20220303")
	}
	if len(task.Categories) != 2 || task.Categories[0] != "Test tag" || task.Categories[1] != "Work" {
		t.Errorf("Categories = %v, want [Test tag, Work]", task.Categories)
	}
	if task.ParentUID != "9999999" {
		t.Errorf("ParentUID = %q, want %q", task.ParentUID, "9999999")
	}
	if task.SortOrder != 559033419 {
		t.Errorf("SortOrder = %d, want %d", task.SortOrder, 559033419)
	}
	if len(task.Alarms) != 1 {
		t.Fatalf("len(Alarms) = %d, want 1", len(task.Alarms))
	}
	if task.Alarms[0].Action != "DISPLAY" {
		t.Errorf("Alarm.Action = %q, want %q", task.Alarms[0].Action, "DISPLAY")
	}
	if task.Alarms[0].Trigger != "PT0S" {
		t.Errorf("Alarm.Trigger = %q, want %q", task.Alarms[0].Trigger, "PT0S")
	}
	if !task.HasDescription {
		t.Error("HasDescription = false, want true")
	}
	if task.DescriptionTokens != 6 {
		t.Errorf("DescriptionTokens = %d, want 6", task.DescriptionTokens)
	}
}

func TestParseVTODO_Minimal(t *testing.T) {
	raw := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VTODO
UID:abc123
SUMMARY:Minimal task
END:VTODO
END:VCALENDAR`

	task, err := ParseVTODO(raw)
	if err != nil {
		t.Fatalf("ParseVTODO: %v", err)
	}

	if task.UID != "abc123" {
		t.Errorf("UID = %q, want %q", task.UID, "abc123")
	}
	if task.HasDescription {
		t.Error("HasDescription = true, want false")
	}
}

func TestParseVTODO_NoVTODO(t *testing.T) {
	raw := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:event1
SUMMARY:An event
END:VEVENT
END:VCALENDAR`

	_, err := ParseVTODO(raw)
	if err == nil {
		t.Error("expected error for missing VTODO, got nil")
	}
}

func TestParseVTODO_FoldedLines(t *testing.T) {
	raw := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VTODO\r\nUID:fold-test\r\nSUMMARY:This is a\r\n  folded summary\r\nEND:VTODO\r\nEND:VCALENDAR"

	task, err := ParseVTODO(raw)
	if err != nil {
		t.Fatalf("ParseVTODO: %v", err)
	}

	if task.Summary != "This is a folded summary" {
		t.Errorf("Summary = %q, want %q", task.Summary, "This is a folded summary")
	}
}

func TestTaskToIcal(t *testing.T) {
	task := &Task{
		UID:         "test-uid-123",
		Summary:     "Buy groceries",
		Description: "Milk, eggs, bread",
		Status:      "NEEDS-ACTION",
		Priority:    5,
		Due:         "2024-01-15",
		Categories:  []string{"Shopping", "Errands"},
		ParentUID:   "parent-123",
		SortOrder:   42,
		Alarms: []Alarm{
			{Trigger: "PT0S", Action: "DISPLAY", Description: "Reminder"},
		},
	}

	result := TaskToIcal(task)

	if !strings.Contains(result, "BEGIN:VCALENDAR") {
		t.Error("missing VCALENDAR begin")
	}
	if !strings.Contains(result, "BEGIN:VTODO") {
		t.Error("missing VTODO begin")
	}
	if !strings.Contains(result, "UID:test-uid-123") {
		t.Error("missing UID")
	}
	if !strings.Contains(result, "SUMMARY:Buy groceries") {
		t.Error("missing SUMMARY")
	}
	if !strings.Contains(result, "DUE;VALUE=DATE:20240115") {
		t.Error("missing or wrong DUE format")
	}
	if !strings.Contains(result, "CATEGORIES:Shopping,Errands") {
		t.Error("missing CATEGORIES")
	}
	if !strings.Contains(result, "RELATED-TO;RELTYPE=PARENT:parent-123") {
		t.Error("missing RELATED-TO")
	}
	if !strings.Contains(result, "X-APPLE-SORT-ORDER:42") {
		t.Error("missing X-APPLE-SORT-ORDER")
	}
	if !strings.Contains(result, "BEGIN:VALARM") {
		t.Error("missing VALARM")
	}
}

func TestTaskToIcal_RoundTrip(t *testing.T) {
	original := &Task{
		UID:         "roundtrip-1",
		Summary:     "Round trip test",
		Description: "Testing serialize then parse",
		Status:      "IN-PROCESS",
		Priority:    3,
		Due:         "20240315",
		Categories:  []string{"test", "roundtrip"},
		ParentUID:   "parent-rt",
		RRule:       "FREQ=WEEKLY;COUNT=4",
		Location:    "Office",
		SortOrder:   100,
	}

	icalStr := TaskToIcal(original)
	parsed, err := ParseVTODO(icalStr)
	if err != nil {
		t.Fatalf("ParseVTODO: %v", err)
	}

	if parsed.UID != original.UID {
		t.Errorf("UID = %q, want %q", parsed.UID, original.UID)
	}
	if parsed.Summary != original.Summary {
		t.Errorf("Summary = %q, want %q", parsed.Summary, original.Summary)
	}
	if parsed.Status != original.Status {
		t.Errorf("Status = %q, want %q", parsed.Status, original.Status)
	}
	if parsed.Priority != original.Priority {
		t.Errorf("Priority = %d, want %d", parsed.Priority, original.Priority)
	}
	if parsed.Due != original.Due {
		t.Errorf("Due = %q, want %q", parsed.Due, original.Due)
	}
	if parsed.ParentUID != original.ParentUID {
		t.Errorf("ParentUID = %q, want %q", parsed.ParentUID, original.ParentUID)
	}
	if parsed.RRule != original.RRule {
		t.Errorf("RRule = %q, want %q", parsed.RRule, original.RRule)
	}
	if parsed.Location != original.Location {
		t.Errorf("Location = %q, want %q", parsed.Location, original.Location)
	}
	if parsed.SortOrder != original.SortOrder {
		t.Errorf("SortOrder = %d, want %d", parsed.SortOrder, original.SortOrder)
	}
}

func TestParseVTODO_TasksOrgMetadata(t *testing.T) {
	raw := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:+//IDN tasks.org//android-120500//EN
BEGIN:VTODO
UID:tasksorg-meta-test
SUMMARY:Task with tasks.org extensions
CATEGORIES:groceries,errand
PRIORITY:5
LOCATION:Whole Foods
X-APPLE-STRUCTURED-LOCATION;VALUE=URI;X-APPLE-RADIUS=70;X-TITLE=Who
 le Foods:geo:37.7749,-122.4194
ATTACH;FMTTYPE=image/jpeg:https://example.com/receipt.jpg
ATTACH:https://example.com/notes.pdf
X-APPLE-SORT-ORDER:100
END:VTODO
END:VCALENDAR`

	task, err := ParseVTODO(raw)
	if err != nil {
		t.Fatalf("ParseVTODO: %v", err)
	}

	// CATEGORIES already works
	if len(task.Categories) != 2 || task.Categories[0] != "groceries" {
		t.Errorf("Categories = %v, want [groceries, errand]", task.Categories)
	}

	// ATTACH should be parsed
	if len(task.Attachments) != 2 {
		t.Fatalf("len(Attachments) = %d, want 2", len(task.Attachments))
	}
	if task.Attachments[0] != "https://example.com/receipt.jpg" {
		t.Errorf("Attachments[0] = %q, want receipt URL", task.Attachments[0])
	}
	if task.Attachments[1] != "https://example.com/notes.pdf" {
		t.Errorf("Attachments[1] = %q, want notes URL", task.Attachments[1])
	}

	// X-APPLE-STRUCTURED-LOCATION should be parsed
	if task.StructuredLocation == "" {
		t.Error("StructuredLocation should not be empty")
	}
	if task.StructuredLocation != "geo:37.7749,-122.4194" {
		t.Errorf("StructuredLocation = %q, want geo URI", task.StructuredLocation)
	}
}

func TestParseVTODO_AttachmentsInMetadata(t *testing.T) {
	task := &Task{
		UID:         "meta-attach",
		Summary:     "Task with attachments",
		Attachments: []string{"https://example.com/file.pdf"},
	}

	meta := task.ToMetadata()
	if meta.AttachmentCount != 1 {
		t.Errorf("AttachmentCount = %d, want 1", meta.AttachmentCount)
	}
}

func TestParseVTODO_LocationInMetadata(t *testing.T) {
	task := &Task{
		UID:                "meta-loc",
		Summary:            "Task with location",
		Location:           "Whole Foods",
		StructuredLocation: "geo:37.7749,-122.4194",
	}

	meta := task.ToMetadata()
	if meta.Location != "Whole Foods" {
		t.Errorf("Location = %q, want %q", meta.Location, "Whole Foods")
	}
}

func TestCapDescription(t *testing.T) {
	short := "short"
	if got := CapDescription(short, 100); got != short {
		t.Errorf("CapDescription(%q, 100) = %q, want %q", short, got, short)
	}

	long := strings.Repeat("a", 5000)
	got := CapDescription(long, 4000)
	if len(got) != 4003 { // 4000 + "..."
		t.Errorf("len(CapDescription) = %d, want 4003", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Error("capped description should end with ...")
	}
}

func TestTaskToMetadata(t *testing.T) {
	task := &Task{
		UID:               "uid-1",
		Summary:           "Test",
		Status:            "NEEDS-ACTION",
		Priority:          3,
		Due:               "20240101",
		Categories:        []string{"tag1"},
		HasDescription:    true,
		DescriptionTokens: 50,
		ParentUID:         "parent-1",
		Description:       "long description here",
		Location:          "office",
	}

	meta := task.ToMetadata()
	if meta.UID != "uid-1" {
		t.Errorf("UID = %q", meta.UID)
	}
	if meta.Summary != "Test" {
		t.Errorf("Summary = %q", meta.Summary)
	}
	if !meta.HasDescription {
		t.Error("HasDescription should be true")
	}
	if meta.DescriptionTokens != 50 {
		t.Errorf("DescriptionTokens = %d", meta.DescriptionTokens)
	}
}
