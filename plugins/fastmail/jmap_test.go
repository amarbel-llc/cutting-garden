package fastmail

import (
	"context"
	"sort"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/plugins/fastmail/fastmailtestserver"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// newWriteFixture starts a bare in-memory JMAP server seeded with the write
// tests' tree and returns a client pointed straight at it. It deliberately
// bypasses the account/config plumbing (startWired) — these tests exercise
// the JMAP transport, not URI classification.
//
//	Inbox (role inbox)  Archive (role archive)  Trash (role trash)
//	payee/
//	  -acme
//
// Thread T1 has two members: e1 (inbox + payee/-acme, $seen) and e2 (inbox
// only, unread). e2's single mailbox makes it the archive-invariant probe.
func newWriteFixture(t *testing.T) (*client, *fastmailtestserver.Server) {
	t.Helper()
	srv := fastmailtestserver.Start("acct-1")
	t.Cleanup(srv.Close)

	srv.AddMailbox("mb-inbox", "Inbox", "", "inbox")
	srv.AddMailbox("mb-archive", "Archive", "", "archive")
	srv.AddMailbox("mb-trash", "Trash", "", "trash")
	srv.AddMailbox("mb-payee", "payee", "", "")
	srv.AddMailbox("mb-acme", "-acme", "mb-payee", "")

	srv.AddEmail(fastmailtestserver.Email{
		ID: "e1", ThreadID: "T1",
		MailboxIDs: []string{"mb-inbox", "mb-acme"},
		Keywords:   []string{"$seen"},
		Subject:    "Your July receipt", ReceivedAt: "2026-07-14T09:12:03Z",
		BlobID: "blob-e1",
	})
	srv.AddEmail(fastmailtestserver.Email{
		ID: "e2", ThreadID: "T1",
		MailboxIDs: []string{"mb-inbox"},
		Subject:    "Re: Your July receipt", ReceivedAt: "2026-07-15T09:00:00Z",
		BlobID: "blob-e2",
	})

	return newClient(srv.SessionURL(), "secret-token"), srv
}

// mailboxIDsOf reads one email back through the wire and returns its
// mailbox ids, sorted.
func mailboxIDsOf(t *testing.T, c *client, id string) []string {
	t.Helper()
	return setKeys(t, c, id, func(e Email) map[string]bool { return e.MailboxIDs })
}

// keywordsOf reads one email back through the wire and returns its keywords,
// sorted.
func keywordsOf(t *testing.T, c *client, id string) []string {
	t.Helper()
	return setKeys(t, c, id, func(e Email) map[string]bool { return e.Keywords })
}

func setKeys(
	t *testing.T, c *client, id string, pick func(Email) map[string]bool,
) []string {
	t.Helper()
	emails, err := c.emailGet(context.Background(), []string{id}, emailFacetProps)
	if err != nil {
		t.Fatalf("emailGet(%q): %v", id, err)
	}
	if len(emails) != 1 {
		t.Fatalf("emailGet(%q) returned %d emails, want 1", id, len(emails))
	}
	out := make([]string, 0, len(pick(emails[0])))
	for k, on := range pick(emails[0]) {
		if on {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func assertStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// TestClient_CallMany_ReturnsEveryMethodResponse pins the multi-call
// generalization: one request carrying two method calls comes back with two
// responses, each addressable by its call id.
func TestClient_CallMany_ReturnsEveryMethodResponse(t *testing.T) {
	c, _ := newWriteFixture(t)

	sess, err := c.ensureSession(context.Background())
	if err != nil {
		t.Fatalf("ensureSession: %v", err)
	}
	responses, err := c.callMany(context.Background(), []jmapMethodCall{
		{"Mailbox/get", mailboxGetArgs{AccountID: sess.AccountID()}, "a"},
		{"Thread/get", threadGetArgs{AccountID: sess.AccountID(), IDs: []string{"T1"}}, "b"},
	})
	if err != nil {
		t.Fatalf("callMany: %v", err)
	}
	if len(responses) != 2 {
		t.Fatalf("callMany returned %d responses, want 2", len(responses))
	}

	var mailboxes mailboxGetResult
	if err := decodeResponseFor(responses, "Mailbox/get", "a", &mailboxes); err != nil {
		t.Fatalf("decode Mailbox/get: %v", err)
	}
	if len(mailboxes.List) != 5 {
		t.Errorf("Mailbox/get returned %d mailboxes, want 5", len(mailboxes.List))
	}

	var threads threadGetResult
	if err := decodeResponseFor(responses, "Thread/get", "b", &threads); err != nil {
		t.Fatalf("decode Thread/get: %v", err)
	}
	if len(threads.List) != 1 || len(threads.List[0].EmailIDs) != 2 {
		t.Errorf("Thread/get = %#v, want one thread with two members", threads.List)
	}
}

// TestClient_EmailSet_PatchesTwoMembersInOneCall is the plan's first vector:
// both members of a thread get a mailbox membership moved and a keyword set
// in ONE Email/set.
func TestClient_EmailSet_PatchesTwoMembersInOneCall(t *testing.T) {
	c, _ := newWriteFixture(t)

	patch := map[string]any{
		"mailboxIds/mb-inbox":   nil,
		"mailboxIds/mb-archive": true,
		"keywords/$seen":        true,
	}
	if err := c.emailSet(context.Background(), map[string]map[string]any{
		"e1": patch,
		"e2": patch,
	}); err != nil {
		t.Fatalf("emailSet: %v", err)
	}

	assertStrings(t, "e1 mailboxIds",
		mailboxIDsOf(t, c, "e1"), []string{"mb-acme", "mb-archive"})
	assertStrings(t, "e2 mailboxIds",
		mailboxIDsOf(t, c, "e2"), []string{"mb-archive"})
	assertStrings(t, "e1 keywords", keywordsOf(t, c, "e1"), []string{"$seen"})
	assertStrings(t, "e2 keywords", keywordsOf(t, c, "e2"), []string{"$seen"})
}

// TestClient_EmailSet_ReplacesWholeProperty pins the whole-object form of
// both patchable properties (RFC 8620 §5.3: a bare property name replaces).
func TestClient_EmailSet_ReplacesWholeProperty(t *testing.T) {
	c, _ := newWriteFixture(t)

	if err := c.emailSet(context.Background(), map[string]map[string]any{
		"e1": {
			"mailboxIds": map[string]any{"mb-trash": true},
			"keywords":   map[string]any{"$flagged": true},
		},
	}); err != nil {
		t.Fatalf("emailSet: %v", err)
	}

	assertStrings(t, "e1 mailboxIds", mailboxIDsOf(t, c, "e1"), []string{"mb-trash"})
	assertStrings(t, "e1 keywords", keywordsOf(t, c, "e1"), []string{"$flagged"})
}

// TestClient_ApplyThreadPatch_ReferencesACreationFromTheSameRequest is the
// plan's second vector: a Mailbox/set create and an Email/set that names the
// new mailbox by its `#<creationId>` back-reference, in one request.
func TestClient_ApplyThreadPatch_ReferencesACreationFromTheSameRequest(t *testing.T) {
	c, _ := newWriteFixture(t)

	res, err := c.applyThreadPatch(context.Background(), threadPatchRequest{
		Creates: map[string]MailboxCreate{
			"c1": {Name: "-tyrwhitt", ParentID: "mb-payee"},
		},
		Updates: map[string]map[string]any{
			"e1": {"mailboxIds/#c1": true},
			"e2": {"mailboxIds/#c1": true},
		},
	})
	if err != nil {
		t.Fatalf("applyThreadPatch: %v", err)
	}

	created := res.CreatedIDs["c1"]
	if created == "" {
		t.Fatalf("applyThreadPatch CreatedIDs = %#v, want an id for c1", res.CreatedIDs)
	}

	assertStrings(t, "e1 mailboxIds",
		mailboxIDsOf(t, c, "e1"), sortedStrings("mb-acme", "mb-inbox", created))
	assertStrings(t, "e2 mailboxIds",
		mailboxIDsOf(t, c, "e2"), sortedStrings("mb-inbox", created))

	mailboxes, err := c.mailboxGetAll(context.Background())
	if err != nil {
		t.Fatalf("mailboxGetAll: %v", err)
	}
	found := false
	for _, m := range mailboxes {
		if m.ID != created {
			continue
		}
		found = true
		if m.Name != "-tyrwhitt" || m.ParentID != "mb-payee" {
			t.Errorf("created mailbox = %#v, want name -tyrwhitt under mb-payee", m)
		}
	}
	if !found {
		t.Errorf("created mailbox %q absent from Mailbox/get", created)
	}
}

// TestClient_ApplyThreadPatch_NoopIssuesNothing pins that an empty patch
// request short-circuits: Task 4's empty-diff path must not POST.
func TestClient_ApplyThreadPatch_NoopIssuesNothing(t *testing.T) {
	c, _ := newWriteFixture(t)

	res, err := c.applyThreadPatch(context.Background(), threadPatchRequest{})
	if err != nil {
		t.Fatalf("applyThreadPatch: %v", err)
	}
	if len(res.CreatedIDs) != 0 {
		t.Errorf("CreatedIDs = %#v, want empty", res.CreatedIDs)
	}
}

// TestClient_EmailSet_NotUpdatedIsABadRequest pins the plan's third vector:
// a server refusal names the message AND the server's reason.
func TestClient_EmailSet_NotUpdatedIsABadRequest(t *testing.T) {
	c, _ := newWriteFixture(t)

	err := c.emailSet(context.Background(), map[string]map[string]any{
		"e-nope": {"keywords/$seen": true},
	})
	if err == nil {
		t.Fatal("emailSet on an unknown id = nil error, want a bad request")
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("error must classify as a CALLER fault: %v", err)
	}
	for _, want := range []string{"Email/set", "e-nope", "notFound"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// TestClient_EmailSet_RejectsAnUpdateLeavingNoMailbox pins the constraint
// Task 4's archive invariant exists to avoid: JMAP requires a message to be
// in at least one mailbox, and the refusal leaves the message untouched.
func TestClient_EmailSet_RejectsAnUpdateLeavingNoMailbox(t *testing.T) {
	c, _ := newWriteFixture(t)

	err := c.emailSet(context.Background(), map[string]map[string]any{
		"e2": {"mailboxIds/mb-inbox": nil},
	})
	if err == nil {
		t.Fatal("emailSet emptying mailboxIds = nil error, want a bad request")
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("error must classify as a CALLER fault: %v", err)
	}
	for _, want := range []string{"e2", "invalidProperties", "mailbox"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	assertStrings(t, "e2 mailboxIds after refusal",
		mailboxIDsOf(t, c, "e2"), []string{"mb-inbox"})
}

// TestClient_EmailSet_RejectsAnUnsupportedPatchKey pins that the fixture is
// honest about the patch pointers it implements rather than silently
// dropping one.
func TestClient_EmailSet_RejectsAnUnsupportedPatchKey(t *testing.T) {
	c, _ := newWriteFixture(t)

	err := c.emailSet(context.Background(), map[string]map[string]any{
		"e1": {"subject": "rewritten"},
	})
	if err == nil {
		t.Fatal("emailSet with an unsupported patch key = nil error, want a bad request")
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("error must classify as a CALLER fault: %v", err)
	}
	for _, want := range []string{"e1", "invalidPatch", "subject"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// TestClient_EmailSet_RejectsAPrefixConflictingPatch pins RFC 8620 §5.3's
// rule that no patch pointer may be a prefix of another in one PatchObject.
func TestClient_EmailSet_RejectsAPrefixConflictingPatch(t *testing.T) {
	c, _ := newWriteFixture(t)

	err := c.emailSet(context.Background(), map[string]map[string]any{
		"e1": {
			"mailboxIds":          map[string]any{"mb-archive": true},
			"mailboxIds/mb-inbox": nil,
		},
	})
	if err == nil {
		t.Fatal("emailSet with a prefix-conflicting patch = nil error, want a bad request")
	}
	if !strings.Contains(err.Error(), "invalidPatch") {
		t.Errorf("error %q does not name invalidPatch", err)
	}
	assertStrings(t, "e1 mailboxIds after refusal",
		mailboxIDsOf(t, c, "e1"), []string{"mb-acme", "mb-inbox"})
}

// TestClient_EmailSet_RejectsAnUnknownMailboxID pins that a membership patch
// naming a mailbox that does not exist is refused, not silently recorded.
func TestClient_EmailSet_RejectsAnUnknownMailboxID(t *testing.T) {
	c, _ := newWriteFixture(t)

	err := c.emailSet(context.Background(), map[string]map[string]any{
		"e1": {"mailboxIds/mb-ghost": true},
	})
	if err == nil {
		t.Fatal("emailSet naming an unknown mailbox = nil error, want a bad request")
	}
	for _, want := range []string{"e1", "mb-ghost"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// TestClient_MailboxSet_NotCreatedIsABadRequest pins the creation half of
// the refusal contract: an unresolvable parent names the creation id and the
// server's reason.
func TestClient_MailboxSet_NotCreatedIsABadRequest(t *testing.T) {
	c, _ := newWriteFixture(t)

	_, err := c.mailboxSet(context.Background(), map[string]MailboxCreate{
		"c1": {Name: "-orphan", ParentID: "#never-created"},
	})
	if err == nil {
		t.Fatal("mailboxSet with an unresolvable parent = nil error, want a bad request")
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("error must classify as a CALLER fault: %v", err)
	}
	for _, want := range []string{"Mailbox/set", "c1", "invalidProperties"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// TestClient_MailboxSet_CreatesATopLevelMailbox pins the D4(c) wire shape: a
// creation with no parent marshals parentId as JSON null.
func TestClient_MailboxSet_CreatesATopLevelMailbox(t *testing.T) {
	c, _ := newWriteFixture(t)

	ids, err := c.mailboxSet(context.Background(), map[string]MailboxCreate{
		"c1": {Name: "misc-thing"},
	})
	if err != nil {
		t.Fatalf("mailboxSet: %v", err)
	}
	created := ids["c1"]
	if created == "" {
		t.Fatalf("mailboxSet returned %#v, want an id for c1", ids)
	}

	mailboxes, err := c.mailboxGetAll(context.Background())
	if err != nil {
		t.Fatalf("mailboxGetAll: %v", err)
	}
	for _, m := range mailboxes {
		if m.ID == created {
			if m.ParentID != "" || m.Name != "misc-thing" {
				t.Errorf("created mailbox = %#v, want top-level misc-thing", m)
			}
			return
		}
	}
	t.Errorf("created mailbox %q absent from Mailbox/get", created)
}

func sortedStrings(in ...string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
