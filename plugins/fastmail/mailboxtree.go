package fastmail

import (
	"sort"
	"strings"
)

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
	// tagIndex is the reverse of tagOf: tag → the id of the mailbox that
	// joins to it. Two mailboxes may legally join to one name in Fastmail;
	// the FIRST in listing order wins (a known v1 limitation). Both indexes
	// are filled by the root-anchored listing walk, so an orphan mailbox
	// (unknown parent) is unreachable and absent from them — intentional;
	// consumers must not assume completeness.
	tagIndex map[string]string
	// roleIDByRole maps EVERY role seen (not only allowedRoles) to its
	// mailbox id; first in listing order when a role is (illegally)
	// duplicated. Same orphan caveat as tagIndex.
	roleIDByRole map[string]string
}

func newMailboxTree(mailboxes []Mailbox) *mailboxTree {
	t := &mailboxTree{
		byID:         make(map[string]Mailbox, len(mailboxes)),
		children:     map[string][]Mailbox{},
		pathByID:     make(map[string][]string, len(mailboxes)),
		tagIndex:     map[string]string{},
		roleIDByRole: map[string]string{},
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
	t.walkListingOrder("", map[string]bool{}, func(m Mailbox) {
		if m.Role != "" {
			if _, dup := t.roleIDByRole[m.Role]; !dup {
				t.roleIDByRole[m.Role] = m.ID
			}
			return
		}
		tag, ok := t.tagOf(m.ID)
		if !ok {
			return
		}
		if _, dup := t.tagIndex[tag]; !dup {
			t.tagIndex[tag] = m.ID
		}
	})
	return t
}

// walkListingOrder visits every mailbox under parentID depth-first in the
// stable listing order (children sorted by name then id). seen guards
// against a cyclic parentId chain from the server, mirroring computePath.
func (t *mailboxTree) walkListingOrder(
	parentID string, seen map[string]bool, visit func(Mailbox),
) {
	for _, m := range t.children[parentID] {
		if seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		visit(m)
		t.walkListingOrder(m.ID, seen, visit)
	}
}

// ergonomicRootName is the label-tree root that exists only to group tags
// in Fastmail's UI (fastmail tags slice 1, D1): it never renders as a tag
// and its children are plain tags.
const ergonomicRootName = "_"

// isContinuationSegment reports whether a label segment continues its
// parent's tag name rather than starting a new one (dodder-hyphen join):
// `proj-x` + `-msft` → `proj-x-msft`.
func isContinuationSegment(seg string) bool {
	return strings.HasPrefix(seg, "-")
}

// tagOf returns the tag a user-label mailbox contributes (D1): the join of
// its path from the NEAREST BARE ancestor onward, where a segment is bare
// iff it does not start with `-`. Bare ancestors above that point are tags
// on the tag, not part of its name. The ergonomic root `_` yields no tag
// itself and is skipped when it is the nearest bare ancestor. Role
// mailboxes, unknown ids, and a chain with no bare segment at all (a
// top-level `-foo`, or `_/-foo`) yield no tag — the latter would be a
// hyphen-leading, malformed dodder-hyphen literal.
func (t *mailboxTree) tagOf(id string) (string, bool) {
	m, ok := t.byID[id]
	if !ok || m.Role != "" {
		return "", false
	}
	path := t.path(id)
	start := -1
	for i := len(path) - 1; i >= 0; i-- {
		if !isContinuationSegment(path[i]) {
			start = i
			break
		}
	}
	if start < 0 {
		return "", false
	}
	if path[start] == ergonomicRootName {
		start++
		if start >= len(path) || isContinuationSegment(path[start]) {
			return "", false
		}
	}
	return strings.Join(path[start:], ""), true
}

// roleID returns the id of the mailbox holding role (inbox, trash, …).
func (t *mailboxTree) roleID(role string) (string, bool) {
	id, ok := t.roleIDByRole[role]
	return id, ok
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
