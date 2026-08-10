package fastmail

import "sort"

// allowedRoles is the in-scope role-mailbox set (FDR 0024): inbox, archive,
// sent, junk, trash. Role mailboxes outside this set (drafts, scheduled,
// snoozed) and the memos data type are excluded from the v1 tree entirely.
// A user tag mailbox has role "" (null) and is always in scope.
var allowedRoles = map[string]bool{
	"inbox":   true,
	"archive": true,
	"sent":    true,
	"junk":    true,
	"trash":   true,
}

// inScope reports whether a mailbox belongs in the v1 tree: a user tag
// (role == "") or one of the allowed role mailboxes.
func inScope(m Mailbox) bool {
	return m.Role == "" || allowedRoles[m.Role]
}

// mailboxTree indexes a flat Mailbox list by id, by parent (children sorted
// by name then id for a stable listing), and by full name-path — the three
// lookups traversal and facet derivation need.
type mailboxTree struct {
	byID     map[string]Mailbox
	children map[string][]Mailbox
	pathByID map[string][]string
}

func newMailboxTree(mailboxes []Mailbox) *mailboxTree {
	t := &mailboxTree{
		byID:     make(map[string]Mailbox, len(mailboxes)),
		children: map[string][]Mailbox{},
		pathByID: make(map[string][]string, len(mailboxes)),
	}
	for _, m := range mailboxes {
		t.byID[m.ID] = m
		t.children[m.ParentID] = append(t.children[m.ParentID], m)
	}
	for parent := range t.children {
		kids := t.children[parent]
		sort.Slice(kids, func(i, j int) bool {
			if kids[i].Name != kids[j].Name {
				return kids[i].Name < kids[j].Name
			}
			return kids[i].ID < kids[j].ID
		})
	}
	for _, m := range mailboxes {
		t.pathByID[m.ID] = t.computePath(m)
	}
	return t
}

// computePath walks parentId up to the root, producing the mailbox's full
// name-path (e.g. ["area", "finance", "receipts"]). A cycle or a missing
// parent stops the walk defensively.
func (t *mailboxTree) computePath(m Mailbox) []string {
	var reversed []string
	cur := m
	seen := map[string]bool{}
	for {
		reversed = append(reversed, cur.Name)
		if cur.ParentID == "" || seen[cur.ID] {
			break
		}
		seen[cur.ID] = true
		parent, ok := t.byID[cur.ParentID]
		if !ok {
			break
		}
		cur = parent
	}
	path := make([]string, len(reversed))
	for i := range reversed {
		path[i] = reversed[len(reversed)-1-i]
	}
	return path
}

// path returns the mailbox's full name-path, or nil for an unknown id.
func (t *mailboxTree) path(id string) []string { return t.pathByID[id] }

// resolvePath walks a name-path from the top level to the mailbox it names.
// ok is false when any segment does not match a child at that level.
func (t *mailboxTree) resolvePath(segs []string) (Mailbox, bool) {
	parentID := ""
	var cur Mailbox
	ok := false
	for _, seg := range segs {
		ok = false
		for _, child := range t.children[parentID] {
			if child.Name == seg {
				cur, parentID, ok = child, child.ID, true
				break
			}
		}
		if !ok {
			return Mailbox{}, false
		}
	}
	return cur, ok
}

// topLevelInScope returns the in-scope top-level mailboxes (parentId null),
// in stable listing order.
func (t *mailboxTree) topLevelInScope() []Mailbox {
	return filterInScope(t.children[""])
}

// childrenInScope returns the in-scope child mailboxes of id, in stable
// listing order.
func (t *mailboxTree) childrenInScope(id string) []Mailbox {
	return filterInScope(t.children[id])
}

func filterInScope(mailboxes []Mailbox) []Mailbox {
	out := make([]Mailbox, 0, len(mailboxes))
	for _, m := range mailboxes {
		if inScope(m) {
			out = append(out, m)
		}
	}
	return out
}
