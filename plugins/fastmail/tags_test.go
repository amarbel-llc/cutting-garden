package fastmail

import (
	"reflect"
	"testing"
)

func TestStateTags(t *testing.T) {
	tree := newMailboxTree(syntheticLabelTree())
	inbox := map[string]bool{"mb-inbox": true}
	trash := map[string]bool{"mb-trash": true}
	sent := map[string]bool{"mb-sent": true}
	seen := map[string]bool{"$seen": true}
	seenFlagged := map[string]bool{"$seen": true, "$flagged": true}

	cases := []struct {
		name    string
		members []Email
		want    []string
	}{
		{
			name:    "inbox+unseen",
			members: []Email{{MailboxIDs: inbox, Keywords: map[string]bool{}}},
			want:    []string{stateTagInbox, stateTagUnread},
		},
		{
			name:    "trash+flagged",
			members: []Email{{MailboxIDs: trash, Keywords: seenFlagged}},
			want:    []string{stateTagFlagged, stateTagTrash},
		},
		{
			name:    "sent only",
			members: []Email{{MailboxIDs: sent, Keywords: seen}},
			want:    []string{},
		},
		{
			name: "any member contributes",
			members: []Email{
				{MailboxIDs: sent, Keywords: seen},
				{MailboxIDs: inbox, Keywords: seenFlagged},
				{MailboxIDs: trash, Keywords: map[string]bool{}},
			},
			want: []string{stateTagFlagged, stateTagInbox, stateTagTrash, stateTagUnread},
		},
		{
			name:    "no members",
			members: nil,
			want:    []string{},
		},
	}
	for _, c := range cases {
		got := stateTags(c.members, tree)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: stateTags = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIsStateTag(t *testing.T) {
	for _, tag := range []string{"_inbox", "_unread", "_flagged", "_trash"} {
		if !isStateTag(tag) {
			t.Errorf("isStateTag(%q) = false, want true", tag)
		}
	}
	for _, tag := range []string{"_sent", "_junk", "_archive", "_other", "inbox", "proj-x", ""} {
		if isStateTag(tag) {
			t.Errorf("isStateTag(%q) = true, want false", tag)
		}
	}
}

// Every `_`-prefixed tag that is not a writable state tag is reserved: a
// box edit naming one is refused (Task 4 wires the refusal).
func TestIsReservedTag(t *testing.T) {
	for _, tag := range []string{"_sent", "_junk", "_archive", "_anything", "_"} {
		if !isReservedTag(tag) {
			t.Errorf("isReservedTag(%q) = false, want true", tag)
		}
	}
	for _, tag := range []string{"_inbox", "_unread", "_flagged", "_trash", "proj-x", "sent", ""} {
		if isReservedTag(tag) {
			t.Errorf("isReservedTag(%q) = true, want false", tag)
		}
	}
}
