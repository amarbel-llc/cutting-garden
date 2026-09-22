package fastmail

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/fastmail/fastmailtestserver"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// newMutateFixture wires the plugin at an in-memory JMAP server seeded with
// the write vectors' tree — the planner fixture (syntheticWriteTree /
// labeledThread / unlabeledThread) rebuilt as server state so a PatchNode can
// be re-read through the ordinary traversal path:
//
//	Inbox / Archive / Trash (roles)
//	payee/-one_medical      → payee-one_medical
//	area/-travel/proj-trips-26-09-kyle_yoga
//
// T1 (payee-one_medical, _inbox, _unread) has two members that disagree;
// T3 (_flagged, _inbox) has one member whose only mailbox is the inbox — the
// archive-invariant probe the server would REFUSE if the planner stranded it.
func newMutateFixture(t *testing.T) string {
	t.Helper()
	srv := startWired(t)

	srv.AddMailbox("mb-inbox", "Inbox", "", roleInbox)
	srv.AddMailbox("mb-archive", "Archive", "", roleArchive)
	srv.AddMailbox("mb-trash", "Trash", "", roleTrash)
	srv.AddMailbox("mb-payee", "payee", "", "")
	srv.AddMailbox("mb-one_medical", "-one_medical", "mb-payee", "")
	srv.AddMailbox("mb-area", "area", "", "")
	srv.AddMailbox("mb-travel", "-travel", "mb-area", "")
	srv.AddMailbox("mb-kyle", "proj-trips-26-09-kyle_yoga", "mb-travel", "")

	srv.AddEmail(fastmailtestserver.Email{
		ID: "e1", ThreadID: "T1",
		MailboxIDs: []string{"mb-inbox", "mb-one_medical"},
		Keywords:   []string{"$seen"},
		Subject:    "Your July receipt", ReceivedAt: "2026-07-14T09:12:03Z",
		BlobID: "blob-e1",
	})
	srv.AddEmail(fastmailtestserver.Email{
		ID: "e2", ThreadID: "T1",
		MailboxIDs: []string{"mb-one_medical"},
		Subject:    "Re: Your July receipt", ReceivedAt: "2026-07-15T09:00:00Z",
		BlobID: "blob-e2",
	})
	srv.AddEmail(fastmailtestserver.Email{
		ID: "e3", ThreadID: "T3",
		MailboxIDs: []string{"mb-inbox"},
		Keywords:   []string{"$seen", "$flagged"},
		Subject:    "Renew the lease?", ReceivedAt: "2026-07-16T11:00:00Z",
		BlobID: "blob-e3",
	})

	return testAccount
}

// mutateClient is a client for the wired fixture account, for the read-backs
// that check what a PatchNode actually wrote.
func mutateClient(t *testing.T, account string) *client {
	t.Helper()
	c, err := resolveClient(nodeRef{account: account})
	if err != nil {
		t.Fatalf("resolveClient(%q): %v", account, err)
	}
	return c
}

// liveThreadTags re-reads a thread through the SAME derivation the listing
// renders, so a round-trip assertion compares presented tag sets rather than
// JMAP internals.
func liveThreadTags(t *testing.T, account, threadID string) []string {
	t.Helper()
	c := mutateClient(t, account)
	tree, err := c.loadMailboxTree(context.Background())
	if err != nil {
		t.Fatalf("loadMailboxTree: %v", err)
	}
	members, err := c.threadMembers(context.Background(), threadID)
	if err != nil {
		t.Fatalf("threadMembers(%q): %v", threadID, err)
	}
	return threadTags(members, tree)
}

// patchThread runs PatchNode against a thread of the fixture, addressed the
// way the listing addresses it.
func patchThread(
	t *testing.T, account string, mailboxPath []string, threadID, body string,
) ([]string, error) {
	t.Helper()
	return Plugin{}.PatchNode(
		context.Background(),
		threadURI(account, mailboxPath, threadID),
		strings.NewReader(body),
	)
}

// TestPatchNode_ArchivesALabeledThread is the round-trip: dropping `_inbox`
// from a labeled thread leaves it under its label, out of the inbox, and NOT
// in Archive — the member that kept a mailbox was never stranded.
func TestPatchNode_ArchivesALabeledThread(t *testing.T) {
	account := newMutateFixture(t)

	applied, err := patchThread(t, account, []string{"payee", "-one_medical"}, "T1",
		`{"tags":["_unread","payee-one_medical"]}`)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	assertStrings(t, "applied", applied, []string{"tags"})

	assertStrings(t, "T1 tags",
		liveThreadTags(t, account, "T1"), []string{"_unread", "payee-one_medical"})

	c := mutateClient(t, account)
	assertStrings(t, "e1 mailboxIds", mailboxIDsOf(t, c, "e1"), []string{"mb-one_medical"})
	assertStrings(t, "e2 mailboxIds", mailboxIDsOf(t, c, "e2"), []string{"mb-one_medical"})
}

// TestPatchNode_ArchivesAnUnlabeledThread is the archive invariant end to end:
// the server REFUSES an update leaving a message in no mailbox, so a green
// assertion here is proof the planner filed it in Archive itself.
func TestPatchNode_ArchivesAnUnlabeledThread(t *testing.T) {
	account := newMutateFixture(t)

	if _, err := patchThread(t, account, nil, "T3", `{"tags":["_flagged"]}`); err != nil {
		t.Fatalf("PatchNode: %v", err)
	}

	assertStrings(t, "T3 tags", liveThreadTags(t, account, "T3"), []string{"_flagged"})
	assertStrings(t, "e3 mailboxIds",
		mailboxIDsOf(t, mutateClient(t, account), "e3"), []string{"mb-archive"})
}

// TestPatchNode_StateAtomsFanOutOverEveryMember pins D3: one apply marks the
// whole thread seen and flags it, on EVERY member, however they disagreed.
func TestPatchNode_StateAtomsFanOutOverEveryMember(t *testing.T) {
	account := newMutateFixture(t)

	if _, err := patchThread(t, account, []string{"payee", "-one_medical"}, "T1",
		`{"tags":["_flagged","_inbox","payee-one_medical"]}`); err != nil {
		t.Fatalf("PatchNode: %v", err)
	}

	assertStrings(t, "T1 tags", liveThreadTags(t, account, "T1"),
		[]string{"_flagged", "_inbox", "payee-one_medical"})

	c := mutateClient(t, account)
	for _, id := range []string{"e1", "e2"} {
		assertStrings(t, id+" keywords",
			keywordsOf(t, c, id), []string{"$flagged", "$seen"})
	}
}

// TestPatchNode_TrashIsASoftMutation pins D2's `_trash`: the thread moves to
// Trash and KEEPS its label, so the move is reversible.
func TestPatchNode_TrashIsASoftMutation(t *testing.T) {
	account := newMutateFixture(t)

	if _, err := patchThread(t, account, []string{"payee", "-one_medical"}, "T1",
		`{"tags":["_inbox","_trash","_unread","payee-one_medical"]}`); err != nil {
		t.Fatalf("PatchNode: %v", err)
	}

	assertStrings(t, "T1 tags", liveThreadTags(t, account, "T1"),
		[]string{"_inbox", "_trash", "_unread", "payee-one_medical"})
	assertStrings(t, "e2 mailboxIds",
		mailboxIDsOf(t, mutateClient(t, account), "e2"),
		[]string{"mb-one_medical", "mb-trash"})
}

// TestPatchNode_CreatesAMissingTag pins D4 end to end: a tag no mailbox
// realizes is created in the SAME request that applies it, placed by the
// prefix rule, reported in applied by its full path, and present on the
// re-read.
func TestPatchNode_CreatesAMissingTag(t *testing.T) {
	account := newMutateFixture(t)

	applied, err := patchThread(t, account, nil, "T3",
		`{"tags":["_flagged","_inbox","payee-acme"]}`)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	assertStrings(t, "applied", applied, []string{"tags", "mailbox:payee/-acme"})

	assertStrings(t, "T3 tags", liveThreadTags(t, account, "T3"),
		[]string{"_flagged", "_inbox", "payee-acme"})

	c := mutateClient(t, account)
	tree, err := c.loadMailboxTree(context.Background())
	if err != nil {
		t.Fatalf("loadMailboxTree: %v", err)
	}
	created, ok := tree.tagIndex["payee-acme"]
	if !ok {
		t.Fatal("payee-acme is absent from the re-read mailbox tree")
	}
	if got := tree.pathByID[created]; !reflect.DeepEqual(got, []string{"payee", "-acme"}) {
		t.Errorf("created mailbox path = %v, want [payee -acme]", got)
	}
}

// TestPatchNode_EmptyDiffWritesNothing pins that a patch restating the live
// set applies nothing, reports a non-nil EMPTY applied, and issues no JMAP
// write at all — the account's Email state token does not move.
func TestPatchNode_EmptyDiffWritesNothing(t *testing.T) {
	account := newMutateFixture(t)
	c := mutateClient(t, account)

	before, err := c.emailState(context.Background())
	if err != nil {
		t.Fatalf("emailState: %v", err)
	}

	applied, err := patchThread(t, account, []string{"payee", "-one_medical"}, "T1",
		`{"tags":["_unread","_inbox","payee-one_medical"]}`)
	if err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	if applied == nil || len(applied) != 0 {
		t.Errorf("applied = %#v, want a non-nil empty slice", applied)
	}

	after, err := c.emailState(context.Background())
	if err != nil {
		t.Fatalf("emailState: %v", err)
	}
	if before != after {
		t.Errorf("Email state moved from %q to %q; an empty diff must write nothing",
			before, after)
	}
}

// TestPatchNode_RefusesAReservedStateTag pins D2's refusal at the outer edge:
// the error names the tag, and nothing was written.
func TestPatchNode_RefusesAReservedStateTag(t *testing.T) {
	account := newMutateFixture(t)

	_, err := patchThread(t, account, []string{"payee", "-one_medical"}, "T1",
		`{"tags":["_sent","payee-one_medical"]}`)
	if err == nil {
		t.Fatal("PatchNode with _sent = nil error, want a bad request")
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("error must classify as a CALLER fault: %v", err)
	}
	if !strings.Contains(err.Error(), "_sent") {
		t.Errorf("error %q does not name the refused tag", err)
	}
	assertStrings(t, "T1 tags after the refusal", liveThreadTags(t, account, "T1"),
		[]string{"_inbox", "_unread", "payee-one_medical"})
}

// TestPatchNode_RefusesAnInvalidTagSetBeforeFetching pins the ORDERING:
// whether a tag can be written is a pure string question, so it is settled
// before any JMAP round-trip. The account here points at a port nothing is
// listening on, so an error that names the refused tag — rather than a
// transport failure — is proof no request was issued.
func TestPatchNode_RefusesAnInvalidTagSetBeforeFetching(t *testing.T) {
	prev := resolveSessionURL
	resolveSessionURL = func(string) string { return "http://127.0.0.1:1/.well-known/jmap" }
	t.Cleanup(func() { resolveSessionURL = prev })
	t.Setenv(testTokenEnv, "secret-token")
	setAccounts(t, acct(testAccount, testTokenEnv))

	for _, tag := range []string{"_sent", "_archive", "-leading"} {
		_, err := patchThread(t, testAccount, []string{"payee", "-one_medical"}, "T1",
			`{"tags":["`+tag+`","payee-one_medical"]}`)
		if err == nil {
			t.Errorf("PatchNode with %q = nil error, want a bad request", tag)
			continue
		}
		if !errors.Is400BadRequest(err) {
			t.Errorf("%q: error must classify as a CALLER fault: %v", tag, err)
		}
		if !strings.Contains(err.Error(), tag) {
			t.Errorf("error %q does not name %q — the refusal came AFTER a"+
				" network read rather than before it", err, tag)
		}
	}
}

// TestPatchNode_RefusesAnUnknownThread pins that addressing a thread that
// does not exist is a caller fault naming it, not a silent success.
func TestPatchNode_RefusesAnUnknownThread(t *testing.T) {
	account := newMutateFixture(t)

	_, err := patchThread(t, account, nil, "T-nope", `{"tags":["misc-thing"]}`)
	if err == nil {
		t.Fatal("PatchNode on an unknown thread = nil error, want a bad request")
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("error must classify as a CALLER fault: %v", err)
	}
	if !strings.Contains(err.Error(), "T-nope") {
		t.Errorf("error %q does not name the thread", err)
	}
}

// TestBuildMembershipWritePatch_IsTheFullSetTagsBody pins the organize-engine
// entry point: the applier hands PatchNode exactly the full-set `tags` body,
// and a node type with no tag dimension is refused by name.
func TestBuildMembershipWritePatch_IsTheFullSetTagsBody(t *testing.T) {
	write := cgFacetWriteForTags(t)

	body, err := Plugin{}.BuildMembershipWritePatch(
		context.Background(),
		threadNodeForTest(t),
		write,
		[]string{"_flagged", "payee-one_medical"},
	)
	if err != nil {
		t.Fatalf("BuildMembershipWritePatch: %v", err)
	}
	if got, want := string(body), `{"tags":["_flagged","payee-one_medical"]}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}

	// An empty set clears the dimension — the "moved out of every bucket"
	// case, which must still be a full-set body, never an omitted key.
	body, err = Plugin{}.BuildMembershipWritePatch(
		context.Background(), threadNodeForTest(t), write, nil,
	)
	if err != nil {
		t.Fatalf("BuildMembershipWritePatch (empty set): %v", err)
	}
	if got, want := string(body), `{"tags":[]}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}

	mailbox := threadNodeForTest(t)
	mailbox.Type = typeMailbox
	if _, err := (Plugin{}).BuildMembershipWritePatch(
		context.Background(), mailbox, write, []string{"misc-thing"},
	); err == nil {
		t.Error("BuildMembershipWritePatch on a mailbox node = nil error, want a bad request")
	} else if !errors.Is400BadRequest(err) {
		t.Errorf("error must classify as a CALLER fault: %v", err)
	}
}

// cgFacetWriteForTags resolves the thread type's `tags` write mapping from
// the plugin's OWN derived declaration, so the applier test exercises the
// same FacetWrite the organize engine would hand it.
func cgFacetWriteForTags(t *testing.T) cutting_garden_plugins.FacetWrite {
	t.Helper()
	for _, nt := range (Plugin{}).DescribeFacetWrites() {
		if nt.Tag != typeThread {
			continue
		}
		for _, w := range nt.Writes {
			if w.DimensionKey == listingFieldTags {
				return w
			}
		}
	}
	t.Fatalf("the thread type declares no %q write mapping", listingFieldTags)
	return cutting_garden_plugins.FacetWrite{}
}

// threadNodeForTest is a live thread Node as the organize engine would carry
// it into BuildMembershipWritePatch: enriched with the tag set the listing
// presented.
func threadNodeForTest(t *testing.T) cutting_garden_plugins.Node {
	t.Helper()
	return cutting_garden_plugins.Node{
		URI:  threadURI(testAccount, []string{"payee", "-one_medical"}, "T1"),
		Name: "Your July receipt",
		Type: typeThread,
		Fields: map[string]any{
			listingFieldTags: []string{stateTagInbox, "payee-one_medical"},
		},
	}
}
