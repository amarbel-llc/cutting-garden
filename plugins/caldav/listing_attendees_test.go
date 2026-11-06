package caldav

import (
	"slices"
	"testing"
)

// An event's attendees and the presence of a description ride in the
// listing, so "who is in this meeting, and is there more to read" needs no
// read_node (cutting-garden#177 item 3). Both are omitted when absent.
func TestListingFieldsOf_AttendeesAndHasDescription(t *testing.T) {
	view, ok := parseObjectView("BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VEVENT\nUID:e1\n" +
		"SUMMARY:Sync\nDTSTART:20260224T150000Z\nDESCRIPTION:Agenda in the doc\n" +
		"ATTENDEE;CN=A:mailto:a@example.com\nATTENDEE:mailto:b@example.com\n" +
		"END:VEVENT\nEND:VCALENDAR\n")
	if !ok {
		t.Fatal("parseObjectView failed on the event fixture")
	}
	fields := listingFieldsOf(view)
	attendees, _ := fields[listingFieldAttendees].([]string)
	if !slices.Equal(attendees, []string{"mailto:a@example.com", "mailto:b@example.com"}) {
		t.Errorf("attendees = %#v", fields[listingFieldAttendees])
	}
	if fields[listingFieldHasDescription] != true {
		t.Errorf("has_description = %#v, want true", fields[listingFieldHasDescription])
	}

	bare, ok := parseObjectView("BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VTODO\nUID:t1\n" +
		"SUMMARY:Plain\nEND:VTODO\nEND:VCALENDAR\n")
	if !ok {
		t.Fatal("parseObjectView failed on the task fixture")
	}
	for _, key := range []string{listingFieldAttendees, listingFieldHasDescription} {
		if v, present := listingFieldsOf(bare)[key]; present {
			t.Errorf("%s present on a task without it: %#v", key, v)
		}
	}
}
