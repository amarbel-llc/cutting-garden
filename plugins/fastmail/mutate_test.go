package fastmail

import (
	"bytes"
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// syntheticWriteTree is the D3/D4 planner fixture: the three role mailboxes
// the write path resolves (inbox, archive, trash), plus a label tree whose
// shapes exercise every placement rule —
//
//	payee/-one_medical                        → payee-one_medical
//	_/req-others                              → req-others
//	area/-travel/proj-trips-26-09-kyle_yoga    → proj-trips-26-09-kyle_yoga
//	area/-career                              → area-career
//
// Ids are the name-path joined with "/", so a failing assertion reads as the
// mailbox's location.
func syntheticWriteTree() []Mailbox {
	return []Mailbox{
		{ID: "mb-inbox", Name: "Inbox", Role: roleInbox},
		{ID: "mb-archive", Name: "Archive", Role: roleArchive},
		{ID: "mb-trash", Name: "Trash", Role: roleTrash},
		{ID: "mb-sent", Name: "Sent", Role: roleSent},
		{ID: "payee", Name: "payee"},
		{ID: "payee/-one_medical", Name: "-one_medical", ParentID: "payee"},
		{ID: "_", Name: "_"},
		{ID: "_/req-others", Name: "req-others", ParentID: "_"},
		{ID: "area", Name: "area"},
		{ID: "area/-travel", Name: "-travel", ParentID: "area"},
		{
			ID:   "area/-travel/proj-trips-26-09-kyle_yoga",
			Name: "proj-trips-26-09-kyle_yoga", ParentID: "area/-travel",
		},
		{ID: "area/-career", Name: "-career", ParentID: "area"},
	}
}

// labeledThread is the two-member thread the D3 fan-out vectors work on: one
// member in the inbox AND under payee-one_medical (read), one member under
// the label only (unread). Its presented tag set is
// {_inbox, _unread, payee-one_medical}, and the members deliberately DISAGREE
// so "inbox members only" and "all members" can be told apart.
func labeledThread() threadView {
	return threadView{
		threadID: "T1",
		members: []Email{
			{
				ID: "e1", ThreadID: "T1",
				MailboxIDs: map[string]bool{"mb-inbox": true, "payee/-one_medical": true},
				Keywords:   map[string]bool{keywordSeen: true},
			},
			{
				ID: "e2", ThreadID: "T1",
				MailboxIDs: map[string]bool{"payee/-one_medical": true},
				Keywords:   map[string]bool{},
			},
		},
	}
}

// unlabeledThread is the archive-invariant probe: one member whose ONLY
// mailbox is the inbox, so removing `_inbox` would strand it.
func unlabeledThread() threadView {
	return threadView{
		threadID: "T3",
		members: []Email{{
			ID: "e3", ThreadID: "T3",
			MailboxIDs: map[string]bool{"mb-inbox": true},
			Keywords:   map[string]bool{keywordSeen: true, keywordFlagged: true},
		}},
	}
}

// archivedThread is the "adding _inbox pulls a member out of Archive" probe.
func archivedThread() threadView {
	return threadView{
		threadID: "T5",
		members: []Email{{
			ID: "e5", ThreadID: "T5",
			MailboxIDs: map[string]bool{"mb-archive": true, "payee/-one_medical": true},
			Keywords:   map[string]bool{keywordSeen: true},
		}},
	}
}

func TestPlanThreadPatch(t *testing.T) {
	tree := newMailboxTree(syntheticWriteTree())

	cases := []struct {
		name          string
		thread        threadView
		newTags       []string
		wantCreates   map[string]MailboxCreate
		wantUpdates   map[string]emailPatch
		wantCreations []createdMailbox
	}{
		{
			// D3: `_inbox` comes off the members that ARE in the inbox, and
			// only those; the labeled sibling is untouched and no member is
			// stranded, so Archive stays out of it.
			name:        "remove _inbox on a labeled thread",
			thread:      labeledThread(),
			newTags:     []string{stateTagUnread, "payee-one_medical"},
			wantCreates: map[string]MailboxCreate{},
			wantUpdates: map[string]emailPatch{
				"e1": {"mailboxIds/mb-inbox": nil},
			},
		},
		{
			// D3's archive invariant: the member's only mailbox was the inbox,
			// so it lands in Archive rather than in nothing.
			name:        "remove _inbox on an unlabeled thread archives it",
			thread:      unlabeledThread(),
			newTags:     []string{stateTagFlagged},
			wantCreates: map[string]MailboxCreate{},
			wantUpdates: map[string]emailPatch{
				"e3": {"mailboxIds/mb-inbox": nil, "mailboxIds/mb-archive": true},
			},
		},
		{
			name:        "add _flagged flags every member",
			thread:      labeledThread(),
			newTags:     []string{stateTagFlagged, stateTagInbox, stateTagUnread, "payee-one_medical"},
			wantCreates: map[string]MailboxCreate{},
			wantUpdates: map[string]emailPatch{
				"e1": {"keywords/$flagged": true},
				"e2": {"keywords/$flagged": true},
			},
		},
		{
			// D3 is unconditional across members: e1 already carries $seen and
			// is written anyway, so a thread whose members disagreed converges.
			name:        "remove _unread marks every member seen",
			thread:      labeledThread(),
			newTags:     []string{stateTagInbox, "payee-one_medical"},
			wantCreates: map[string]MailboxCreate{},
			wantUpdates: map[string]emailPatch{
				"e1": {"keywords/$seen": true},
				"e2": {"keywords/$seen": true},
			},
		},
		{
			name:        "add _unread clears $seen on every member",
			thread:      unlabeledThread(),
			newTags:     []string{stateTagFlagged, stateTagInbox, stateTagUnread},
			wantCreates: map[string]MailboxCreate{},
			wantUpdates: map[string]emailPatch{
				"e3": {"keywords/$seen": nil},
			},
		},
		{
			name:        "add _trash files every member in Trash",
			thread:      labeledThread(),
			newTags:     []string{stateTagInbox, stateTagTrash, stateTagUnread, "payee-one_medical"},
			wantCreates: map[string]MailboxCreate{},
			wantUpdates: map[string]emailPatch{
				"e1": {"mailboxIds/mb-trash": true},
				"e2": {"mailboxIds/mb-trash": true},
			},
		},
		{
			// D3: adding `_inbox` is the inverse of archiving, so the member
			// leaves Archive in the same patch.
			name:        "add _inbox pulls a member out of Archive",
			thread:      archivedThread(),
			newTags:     []string{stateTagInbox, "payee-one_medical"},
			wantCreates: map[string]MailboxCreate{},
			wantUpdates: map[string]emailPatch{
				"e5": {"mailboxIds/mb-inbox": true, "mailboxIds/mb-archive": nil},
			},
		},
		{
			name:        "add an existing label tags every member",
			thread:      labeledThread(),
			newTags:     []string{stateTagInbox, stateTagUnread, "payee-one_medical", "req-others"},
			wantCreates: map[string]MailboxCreate{},
			wantUpdates: map[string]emailPatch{
				"e1": {"mailboxIds/_/req-others": true},
				"e2": {"mailboxIds/_/req-others": true},
			},
		},
		{
			// D4(a): `payee` (a proper prefix, 1 shared segment) ties
			// `payee-one_medical` (1), and a tie goes to the continuation.
			name:    "a new continuation tag is created under its prefix",
			thread:  labeledThread(),
			newTags: []string{stateTagInbox, stateTagUnread, "payee-charles_tyrwhitt", "payee-one_medical"},
			wantCreates: map[string]MailboxCreate{
				"c1": {Name: "-charles_tyrwhitt", ParentID: "payee"},
			},
			wantUpdates: map[string]emailPatch{
				"e1": {"mailboxIds/#c1": true},
				"e2": {"mailboxIds/#c1": true},
			},
			wantCreations: []createdMailbox{{"c1", "payee/-charles_tyrwhitt"}},
		},
		{
			// D4(b): no existing tag is a prefix, but
			// proj-trips-26-09-kyle_yoga shares four `-` segments, so the new
			// mailbox is its BARE sibling under area/-travel.
			name:    "a new sibling tag is created beside its closest relative",
			thread:  unlabeledThread(),
			newTags: []string{stateTagFlagged, stateTagInbox, "proj-trips-26-09-yoga_retreat"},
			wantCreates: map[string]MailboxCreate{
				"c1": {Name: "proj-trips-26-09-yoga_retreat", ParentID: "area/-travel"},
			},
			wantUpdates: map[string]emailPatch{
				"e3": {"mailboxIds/#c1": true},
			},
			wantCreations: []createdMailbox{
				{"c1", "area/-travel/proj-trips-26-09-yoga_retreat"},
			},
		},
		{
			// D4(c): nothing shares a segment, so the tag becomes a bare
			// top-level mailbox.
			name:    "a new unrelated tag is created at the root",
			thread:  unlabeledThread(),
			newTags: []string{stateTagFlagged, stateTagInbox, "misc-thing"},
			wantCreates: map[string]MailboxCreate{
				"c1": {Name: "misc-thing", ParentID: ""},
			},
			wantUpdates: map[string]emailPatch{
				"e3": {"mailboxIds/#c1": true},
			},
			wantCreations: []createdMailbox{{"c1", "misc-thing"}},
		},
		{
			// Two new tags in one apply get distinct creation ids, assigned in
			// the tags' sorted order so the request is deterministic.
			name:    "two new tags get distinct creation ids",
			thread:  unlabeledThread(),
			newTags: []string{stateTagFlagged, stateTagInbox, "misc-thing", "payee-acme"},
			wantCreates: map[string]MailboxCreate{
				"c1": {Name: "misc-thing", ParentID: ""},
				"c2": {Name: "-acme", ParentID: "payee"},
			},
			wantUpdates: map[string]emailPatch{
				"e3": {"mailboxIds/#c1": true, "mailboxIds/#c2": true},
			},
			wantCreations: []createdMailbox{
				{"c1", "misc-thing"},
				{"c2", "payee/-acme"},
			},
		},
		{
			// Clearing the set entirely: every member loses every mailbox and
			// so lands in Archive — the invariant's hardest case.
			name:        "clearing every tag archives every member",
			thread:      labeledThread(),
			newTags:     nil,
			wantCreates: map[string]MailboxCreate{},
			wantUpdates: map[string]emailPatch{
				"e1": {
					"mailboxIds/payee/-one_medical": nil,
					"mailboxIds/mb-inbox":           nil,
					"mailboxIds/mb-archive":         true,
					"keywords/$seen":                true,
				},
				"e2": {
					"mailboxIds/payee/-one_medical": nil,
					"mailboxIds/mb-archive":         true,
					"keywords/$seen":                true,
				},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan, err := planThreadPatch(c.thread, tree, c.newTags)
			if err != nil {
				t.Fatalf("planThreadPatch: %v", err)
			}
			if !reflect.DeepEqual(plan.request.Creates, c.wantCreates) {
				t.Errorf("Creates = %#v, want %#v", plan.request.Creates, c.wantCreates)
			}
			if !reflect.DeepEqual(plan.request.Updates, c.wantUpdates) {
				t.Errorf("Updates = %#v, want %#v", plan.request.Updates, c.wantUpdates)
			}
			if !reflect.DeepEqual(plan.creations, c.wantCreations) {
				t.Errorf("creations = %#v, want %#v", plan.creations, c.wantCreations)
			}
			assertNoMemberStranded(t, c.thread, plan)
		})
	}
}

// TestPlanThreadPatch_NoDiffPlansNothing pins that a requested set equal to
// the live one issues NO request at all — the empty-diff contract PatchNode
// reports as an empty applied.
func TestPlanThreadPatch_NoDiffPlansNothing(t *testing.T) {
	tree := newMailboxTree(syntheticWriteTree())
	thread := labeledThread()

	for _, name := range []string{"same order", "shuffled"} {
		tags := threadTags(thread.members, tree)
		if name == "shuffled" {
			sort.Sort(sort.Reverse(sort.StringSlice(tags)))
		}
		plan, err := planThreadPatch(thread, tree, tags)
		if err != nil {
			t.Fatalf("%s: planThreadPatch: %v", name, err)
		}
		if !plan.empty() {
			t.Errorf("%s: plan = %#v, want an empty plan", name, plan.request)
		}
	}
}

// TestPlanThreadPatch_RefusesAnUnwritableTag pins D2's loud refusal: every
// `_`-prefixed tag that is not one of the four writable state tags is a bad
// request NAMING the tag, and nothing is planned. The malformed shapes ride
// along — a tag the dodder-hyphen join could never produce is refused too.
func TestPlanThreadPatch_RefusesAnUnwritableTag(t *testing.T) {
	tree := newMailboxTree(syntheticWriteTree())
	for _, tag := range []string{"_sent", "_junk", "_archive", "_whatever", "_", "-leading", ""} {
		plan, err := planThreadPatch(labeledThread(), tree, []string{"payee-one_medical", tag})
		if err == nil {
			t.Errorf("planThreadPatch with %q = nil error, want a bad request", tag)
			continue
		}
		if !errors.Is400BadRequest(err) {
			t.Errorf("%q: error must classify as a CALLER fault: %v", tag, err)
		}
		if tag != "" && !strings.Contains(err.Error(), tag) {
			t.Errorf("error %q does not name the refused tag %q", err, tag)
		}
		if !plan.empty() {
			t.Errorf("%q: a refused patch planned %#v, want nothing", tag, plan.request)
		}
	}
}

// TestPlanThreadPatch_RefusesAThreadWithNoMembers pins that a memberless
// thread refuses rather than creating mailboxes nothing would ever reference.
func TestPlanThreadPatch_RefusesAThreadWithNoMembers(t *testing.T) {
	tree := newMailboxTree(syntheticWriteTree())
	_, err := planThreadPatch(threadView{threadID: "T9"}, tree, []string{"misc-thing"})
	if err == nil {
		t.Fatal("planThreadPatch on a memberless thread = nil error, want a bad request")
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("error must classify as a CALLER fault: %v", err)
	}
	if !strings.Contains(err.Error(), "T9") {
		t.Errorf("error %q does not name the thread", err)
	}
}

// TestPlanThreadPatch_RefusesAStateTagWithNoRoleMailbox pins that a state tag
// whose role the account lacks refuses up front, rather than emitting a patch
// keyed on an empty mailbox id.
func TestPlanThreadPatch_RefusesAStateTagWithNoRoleMailbox(t *testing.T) {
	roleless := newMailboxTree([]Mailbox{
		{ID: "payee", Name: "payee"},
		{ID: "payee/-one_medical", Name: "-one_medical", ParentID: "payee"},
	})
	thread := threadView{
		threadID: "T1",
		members: []Email{{
			ID:         "e1",
			MailboxIDs: map[string]bool{"payee/-one_medical": true},
			Keywords:   map[string]bool{keywordSeen: true},
		}},
	}
	for _, tag := range []string{stateTagInbox, stateTagTrash} {
		_, err := planThreadPatch(thread, roleless, []string{"payee-one_medical", tag})
		if err == nil {
			t.Errorf("planThreadPatch adding %q with no role mailbox = nil error", tag)
			continue
		}
		if !errors.Is400BadRequest(err) {
			t.Errorf("%q: error must classify as a CALLER fault: %v", tag, err)
		}
		if !strings.Contains(err.Error(), tag) {
			t.Errorf("error %q does not name %q", err, tag)
		}
	}
}

// TestPlanThreadPatch_RefusesStrandingWithNoArchive pins the other half of the
// invariant: with no archive role mailbox there is nowhere to file an emptied
// member, so the patch refuses instead of emitting one JMAP would reject.
func TestPlanThreadPatch_RefusesStrandingWithNoArchive(t *testing.T) {
	noArchive := newMailboxTree([]Mailbox{
		{ID: "mb-inbox", Name: "Inbox", Role: roleInbox},
		{ID: "payee", Name: "payee"},
	})
	thread := threadView{
		threadID: "T1",
		members: []Email{{
			ID:         "e1",
			MailboxIDs: map[string]bool{"payee": true},
			Keywords:   map[string]bool{keywordSeen: true},
		}},
	}
	_, err := planThreadPatch(thread, noArchive, nil)
	if err == nil {
		t.Fatal("planThreadPatch stranding a member with no Archive = nil error")
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("error must classify as a CALLER fault: %v", err)
	}
	if !strings.Contains(err.Error(), "e1") {
		t.Errorf("error %q does not name the message it would have stranded", err)
	}
}

// TestPlanThreadPatch_RemovingATagClearsEveryMailboxJoiningToIt pins the
// inverse of the read side's union: two mailboxes may legally join to one tag,
// and removing the tag must clear BOTH — clearing only the tagIndex winner
// would leave the tag still presented.
func TestPlanThreadPatch_RemovingATagClearsEveryMailboxJoiningToIt(t *testing.T) {
	tree := newMailboxTree([]Mailbox{
		{ID: "mb-inbox", Name: "Inbox", Role: roleInbox},
		{ID: "mb-archive", Name: "Archive", Role: roleArchive},
		{ID: "a", Name: "a"},
		{ID: "a/proj-x", Name: "proj-x", ParentID: "a"},
		{ID: "z", Name: "z"},
		{ID: "z/proj-x", Name: "proj-x", ParentID: "z"},
	})
	thread := threadView{
		threadID: "T1",
		members: []Email{{
			ID:         "e1",
			MailboxIDs: map[string]bool{"a/proj-x": true, "z/proj-x": true},
			Keywords:   map[string]bool{keywordSeen: true},
		}},
	}
	plan, err := planThreadPatch(thread, tree, nil)
	if err != nil {
		t.Fatalf("planThreadPatch: %v", err)
	}
	want := map[string]emailPatch{"e1": {
		"mailboxIds/a/proj-x":   nil,
		"mailboxIds/z/proj-x":   nil,
		"mailboxIds/mb-archive": true,
	}}
	if !reflect.DeepEqual(plan.request.Updates, want) {
		t.Errorf("Updates = %#v, want %#v", plan.request.Updates, want)
	}
}

// placementTree is syntheticWriteTree grown with the shapes D4's ranking
// must survive: a bare interior `zz-archive/proj` (tag `proj`, a one-segment
// prefix of every `proj-…` tag, as on the real account) with an archived
// project family under it, plus `area/-career/proj-26`.
func placementTree(extra ...Mailbox) []Mailbox {
	return append(append(syntheticWriteTree(),
		Mailbox{ID: "area/-career/proj-26", Name: "proj-26", ParentID: "area/-career"},
		Mailbox{ID: "zz-archive", Name: "zz-archive"},
		Mailbox{ID: "zz-archive/proj", Name: "proj", ParentID: "zz-archive"},
		Mailbox{ID: "zz-archive/proj/-24-t", Name: "-24-t", ParentID: "zz-archive/proj"},
		Mailbox{
			ID: "zz-archive/proj/-24-t/-10x", Name: "-10x",
			ParentID: "zz-archive/proj/-24-t",
		},
	), extra...)
}

// TestPlaceNewTag covers D4's placement rules directly: (a) and (b)
// candidates ranked together by shared `-` segments, (a) winning ties.
func TestPlaceNewTag(t *testing.T) {
	tree := newMailboxTree(placementTree())
	cases := []struct {
		tag        string
		wantParent string
		wantName   string
	}{
		// (a) `payee` and (b) `payee-one_medical` both share 1 segment: the
		// tie goes to the continuation.
		{"payee-charles_tyrwhitt", "payee", "-charles_tyrwhitt"},
		// The LONGEST prefix wins: `area` and `area-career` are both prefixes
		// of `area-career-notes`, and the deeper mailbox is the parent.
		{"area-career-notes", "area/-career", "-notes"},
		{"proj-26-review", "area/-career/proj-26", "-review"},
		// (b) `proj-trips-26-09-kyle_yoga` shares 4 segments, beating (a)
		// `proj` (the bare interior zz-archive/proj) at 1: a bare sibling
		// beside the trip, NOT a continuation inside the archive.
		{"proj-trips-26-09-yoga_retreat", "area/-travel", "proj-trips-26-09-yoga_retreat"},
		{"proj-trips-26-10-hike", "area/-travel", "proj-trips-26-10-hike"},
		// (a) `proj-24-t` and (b) `proj-24-t-10x` both share 3 segments: the
		// tie keeps an archived project's family together.
		{"proj-24-t-foo", "zz-archive/proj/-24-t", "-foo"},
		{"req-self", "_", "req-self"},
		{"misc-thing", "", "misc-thing"},
		{"solo", "", "solo"},
	}
	for _, c := range cases {
		parent, name := tree.placeNewTag(c.tag)
		if parent != c.wantParent || name != c.wantName {
			t.Errorf("placeNewTag(%q) = (%q, %q), want (%q, %q)",
				c.tag, parent, name, c.wantParent, c.wantName)
		}
		// Whatever the rule, the created mailbox must join back to the tag
		// that asked for it — the placement is only correct if tagOf agrees.
		created := Mailbox{ID: "new", Name: name, ParentID: parent}
		grown := newMailboxTree(placementTree(created))
		if got, ok := grown.tagOf("new"); !ok || got != c.tag {
			t.Errorf("tagOf(the mailbox placed for %q) = (%q, %v), want (%q, true)",
				c.tag, got, ok, c.tag)
		}
	}
}

// TestPatchNode_RejectsANonThreadURI pins that only threads are patchable —
// and that the refusal is structural, needing no configured account or
// network round-trip.
func TestPatchNode_RejectsANonThreadURI(t *testing.T) {
	for _, u := range []string{
		"fastmail://personal/",
		"fastmail://personal/payee/-one_medical/",
		"fastmail://personal/payee/?thread=T1&email=e1",
		"fastmail://personal/payee/?thread=T1&email=e1&raw=1",
	} {
		_, err := Plugin{}.PatchNode(
			context.Background(), mustParseURL(t, u),
			strings.NewReader(`{"tags":["misc-thing"]}`),
		)
		if err == nil {
			t.Errorf("PatchNode(%q) = nil error, want a bad request", u)
			continue
		}
		if !errors.Is400BadRequest(err) {
			t.Errorf("%s: error must classify as a CALLER fault: %v", u, err)
		}
		if !strings.Contains(err.Error(), "only thread nodes are patchable") {
			t.Errorf("%s: error %q does not say why", u, err)
		}
	}
}

// TestPatchNode_BodyShapes pins the pre-network body contract: an empty body
// and a wrong-typed `tags` are bad requests, while a body naming only
// unrecognized keys applies nothing and reports a non-nil EMPTY applied
// (cutting-garden#182). None of these reaches a client, so an unconfigured
// account is proof the refusal happened first.
func TestPatchNode_BodyShapes(t *testing.T) {
	thread := mustParseURL(t, "fastmail://personal/payee/?thread=T1")

	for _, c := range []struct {
		name string
		body string
	}{
		{"empty body", ""},
		{"whitespace body", "  \n"},
		{"not JSON", "tags: nope"},
		{"tags is a string", `{"tags":"payee-acme"}`},
		{"tags holds a number", `{"tags":[7]}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := Plugin{}.PatchNode(
				context.Background(), thread, strings.NewReader(c.body),
			)
			if err == nil {
				t.Fatal("PatchNode = nil error, want a bad request")
			}
			if !errors.Is400BadRequest(err) {
				t.Errorf("error must classify as a CALLER fault: %v", err)
			}
		})
	}

	t.Run("only unrecognized keys", func(t *testing.T) {
		applied, err := Plugin{}.PatchNode(
			context.Background(), thread,
			bytes.NewReader([]byte(`{"subject":"rewritten","from":"a@b.test"}`)),
		)
		if err != nil {
			t.Fatalf("PatchNode: %v", err)
		}
		if applied == nil || len(applied) != 0 {
			t.Errorf("applied = %#v, want a non-nil empty slice", applied)
		}
	})
}

// assertNoMemberStranded replays a plan's patches against the members it was
// built from and fails if any would end in NO mailbox. The testserver refuses
// such an update, but catching it there only proves the fixture is strict;
// this proves the PLANNER never emits one.
func assertNoMemberStranded(t *testing.T, thread threadView, plan threadPatchPlan) {
	t.Helper()
	for _, member := range thread.members {
		patch, patched := plan.request.Updates[member.ID]
		if !patched {
			continue
		}
		result := map[string]bool{}
		for id, on := range member.MailboxIDs {
			if on {
				result[id] = true
			}
		}
		for key, value := range patch {
			id, isMailbox := strings.CutPrefix(key, "mailboxIds/")
			if !isMailbox {
				continue
			}
			if value == nil {
				delete(result, id)
				continue
			}
			result[id] = true
		}
		if len(result) == 0 {
			t.Errorf("member %q would be left in no mailbox by %#v", member.ID, patch)
		}
	}
}
