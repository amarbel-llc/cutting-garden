package fastmail

import (
	"sort"
	"strings"
)

// State tags (fastmail tags slice 1, D2): `_`-prefixed tags a thread's
// tag set carries alongside its label tags, derived from the members'
// role-mailbox membership and JMAP keywords. All four are writable.
const (
	stateTagInbox   = "_inbox"   // any member in the inbox role mailbox
	stateTagUnread  = "_unread"  // any member lacking $seen
	stateTagFlagged = "_flagged" // any member with $flagged
	stateTagTrash   = "_trash"   // any member in the trash role mailbox
)

// stateTagPrefix marks the reserved tag namespace: every tag starting with
// it is either one of the writable state tags or refused outright (`_sent`,
// `_junk`, `_archive`, …) — sent and junk are never emitted as tags.
const stateTagPrefix = "_"

// The JMAP keywords (RFC 8621 §4.1) the `_unread` / `_flagged` state tags
// read and write. Note the inversion on $seen: `_unread` is present when a
// member LACKS it.
const (
	keywordSeen    = "$seen"
	keywordFlagged = "$flagged"
)

var writableStateTags = map[string]bool{
	stateTagInbox:   true,
	stateTagUnread:  true,
	stateTagFlagged: true,
	stateTagTrash:   true,
}

// isStateTag reports whether tag is one of the four writable state tags.
func isStateTag(tag string) bool { return writableStateTags[tag] }

// isReservedTag reports whether tag sits in the `_` namespace without being
// a writable state tag: typing one into a box is a bad request.
func isReservedTag(tag string) bool {
	return strings.HasPrefix(tag, stateTagPrefix) && !isStateTag(tag)
}

// threadTags derives a thread's presented tag set (D1/D2): the union over
// its members of every user-label mailbox's tag (tree.tagOf — role
// mailboxes and untaggable chains contribute nothing) plus the state tags.
// Lexically sorted for a deterministic Format; the framework re-sorts by the
// interpreter's SortKey at render time. Never nil.
func threadTags(members []Email, tree *mailboxTree) []string {
	present := map[string]bool{}
	for _, m := range members {
		for mid := range m.MailboxIDs {
			if tag, ok := tree.tagOf(mid); ok {
				present[tag] = true
			}
		}
	}
	for _, tag := range stateTags(members, tree) {
		present[tag] = true
	}
	out := make([]string, 0, len(present))
	for tag := range present {
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

// stateTags derives a thread's state tags from its members: each tag is
// present iff ANY member satisfies its predicate. The result is
// lexically sorted and never nil.
func stateTags(members []Email, tree *mailboxTree) []string {
	inboxID, _ := tree.roleID(roleInbox)
	trashID, _ := tree.roleID(roleTrash)
	present := map[string]bool{}
	for _, m := range members {
		if !m.Keywords[keywordSeen] {
			present[stateTagUnread] = true
		}
		if m.Keywords[keywordFlagged] {
			present[stateTagFlagged] = true
		}
		if inboxID != "" && m.MailboxIDs[inboxID] {
			present[stateTagInbox] = true
		}
		if trashID != "" && m.MailboxIDs[trashID] {
			present[stateTagTrash] = true
		}
	}
	out := make([]string, 0, len(present))
	for tag := range present {
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}
