package fastmail

import "testing"

// syntheticLabelTree builds the D1 vectors as a flat Mailbox list: every
// path segment is its own mailbox, ids are the segment path joined with
// "/", and the role mailboxes sit at the top level.
func syntheticLabelTree() []Mailbox {
	var out []Mailbox
	add := func(path ...string) {
		parent := ""
		id := ""
		for _, seg := range path {
			if id == "" {
				id = seg
			} else {
				id += "/" + seg
			}
			out = append(out, Mailbox{ID: id, Name: seg, ParentID: parent})
			parent = id
		}
	}
	add("area", "-career", "proj-x", "-msft")
	add("payee", "-a-b")
	add("_", "req-others")
	add("zz-archive", "proj", "-24-12-t", "-10x")
	out = append(out,
		Mailbox{ID: "mb-inbox", Name: "Inbox", Role: "inbox"},
		Mailbox{ID: "mb-trash", Name: "Trash", Role: "trash"},
		Mailbox{ID: "mb-sent", Name: "Sent", Role: "sent"},
	)
	// Dedupe: add() re-appends shared prefixes; keep the first occurrence.
	seen := map[string]bool{}
	deduped := out[:0]
	for _, m := range out {
		if seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		deduped = append(deduped, m)
	}
	return deduped
}

func TestTagOf(t *testing.T) {
	tree := newMailboxTree(syntheticLabelTree())
	cases := []struct {
		id      string
		wantTag string
		wantOK  bool
	}{
		{"area/-career/proj-x/-msft", "proj-x-msft", true},
		{"area/-career/proj-x", "proj-x", true},
		{"area/-career", "area-career", true},
		{"area", "area", true},
		{"payee/-a-b", "payee-a-b", true},
		{"_/req-others", "req-others", true},
		{"_", "", false},
		{"zz-archive/proj/-24-12-t/-10x", "proj-24-12-t-10x", true},
		{"mb-inbox", "", false},
		{"mb-trash", "", false},
		{"no-such-id", "", false},
	}
	for _, c := range cases {
		got, ok := tree.tagOf(c.id)
		if got != c.wantTag || ok != c.wantOK {
			t.Errorf("tagOf(%q) = (%q, %v), want (%q, %v)", c.id, got, ok, c.wantTag, c.wantOK)
		}
	}
}

func TestTagIndex_ReverseLookup(t *testing.T) {
	tree := newMailboxTree(syntheticLabelTree())
	cases := map[string]string{
		"proj-x-msft":      "area/-career/proj-x/-msft",
		"payee-a-b":        "payee/-a-b",
		"req-others":       "_/req-others",
		"proj-24-12-t-10x": "zz-archive/proj/-24-12-t/-10x",
	}
	for tag, wantID := range cases {
		if got := tree.tagIndex[tag]; got != wantID {
			t.Errorf("tagIndex[%q] = %q, want %q", tag, got, wantID)
		}
	}
	for _, absent := range []string{"_", "inbox", "Inbox", ""} {
		if got, ok := tree.tagIndex[absent]; ok {
			t.Errorf("tagIndex[%q] = %q, want absent", absent, got)
		}
	}
}

// Two mailboxes may join to the same tag name (legal in Fastmail: e.g. a
// top-level `proj-x` beside `area/-career/proj-x`). The index keeps the
// FIRST in listing order — a known v1 limitation, pinned here so a later
// change to the policy is deliberate.
func TestTagIndex_DuplicateTagKeepsFirstInListingOrder(t *testing.T) {
	mailboxes := []Mailbox{
		{ID: "z-late", Name: "z", ParentID: ""},
		{ID: "z-late/proj-x", Name: "proj-x", ParentID: "z-late"},
		{ID: "a-early", Name: "a", ParentID: ""},
		{ID: "a-early/proj-x", Name: "proj-x", ParentID: "a-early"},
	}
	tree := newMailboxTree(mailboxes)
	if got := tree.tagIndex["proj-x"]; got != "a-early/proj-x" {
		t.Errorf("tagIndex[proj-x] = %q, want a-early/proj-x (first in listing order)", got)
	}
}

func TestRoleID(t *testing.T) {
	tree := newMailboxTree(syntheticLabelTree())
	for role, wantID := range map[string]string{"inbox": "mb-inbox", "trash": "mb-trash", "sent": "mb-sent"} {
		got, ok := tree.roleID(role)
		if !ok || got != wantID {
			t.Errorf("roleID(%q) = (%q, %v), want (%q, true)", role, got, ok, wantID)
		}
	}
	if got, ok := tree.roleID("drafts"); ok {
		t.Errorf("roleID(drafts) = (%q, true), want absent", got)
	}
}
