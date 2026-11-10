package fastmail

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/fastmail/fastmailtestserver"
)

// The bulk tests run against newMutateFixture's tree: T1 (two members, e1 and
// e2, listed under payee/-one_medical) and T3 (one member, e3, in the Inbox).

func newBulkFixture(t *testing.T) *fastmailtestserver.Server {
	t.Helper()
	srv := startWired(t)
	seedMutateFixture(srv)
	return srv
}

var (
	bulkT1 = func() *url.URL { return threadURI(testAccount, []string{"payee", "-one_medical"}, "T1") }
	bulkT3 = func() *url.URL { return threadURI(testAccount, []string{"Inbox"}, "T3") }
)

func bulkPatch(uri *url.URL, body string) cutting_garden_plugins.BulkOp {
	return cutting_garden_plugins.BulkOp{
		Kind: cutting_garden_plugins.BulkPatch, URI: uri, Body: []byte(body),
	}
}

// bulkMutate runs a best-effort changeset through the plugin's BulkMutator,
// failing the test when the plugin is not one.
func bulkMutate(
	t *testing.T, ops ...cutting_garden_plugins.BulkOp,
) cutting_garden_plugins.BulkResult {
	t.Helper()
	return bulkMutateRequest(t, cutting_garden_plugins.BulkRequest{
		Ops: ops, Atomicity: cutting_garden_plugins.BulkBestEffort,
	})
}

func bulkMutateRequest(
	t *testing.T, req cutting_garden_plugins.BulkRequest,
) cutting_garden_plugins.BulkResult {
	t.Helper()
	bm, ok := any(Plugin{}).(cutting_garden_plugins.BulkMutator)
	if !ok {
		t.Fatal("fastmail Plugin is not a BulkMutator")
	}
	result, err := bm.BulkMutate(context.Background(), req)
	if err != nil {
		t.Fatalf("BulkMutate: %v", err)
	}
	if len(result.Outcomes) != len(result.AppliedNodes)+len(result.PatchedNothing)+len(result.Failed) {
		t.Errorf("outcomes %d != applied %d + patched_nothing %d + failed %d",
			len(result.Outcomes), len(result.AppliedNodes), len(result.PatchedNothing), len(result.Failed))
	}
	return result
}

func outcomeAt(
	t *testing.T, result cutting_garden_plugins.BulkResult, i int,
) cutting_garden_plugins.BulkOutcome {
	t.Helper()
	if i >= len(result.Outcomes) {
		t.Fatalf("outcome %d: only %d outcomes: %+v", i, len(result.Outcomes), result.Outcomes)
	}
	return result.Outcomes[i]
}

func assertOutcome(
	t *testing.T, what string, got cutting_garden_plugins.BulkOutcome,
	uri *url.URL, status cutting_garden_plugins.BulkOutcomeStatus, fields []string,
) {
	t.Helper()
	if got.URI == nil || got.URI.String() != uri.String() {
		t.Errorf("%s: uri = %v, want %v", what, got.URI, uri)
	}
	if got.Status != status {
		t.Errorf("%s: status = %q (err %q), want %q", what, got.Status, got.Err, status)
	}
	if fields != nil {
		assertStrings(t, what+" fields", got.Fields, fields)
	}
}

func assertErrContains(t *testing.T, what, err string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(err, want) {
			t.Errorf("%s: err = %q, want it to contain %q", what, err, want)
		}
	}
}

// N thread patches cost ONE Email/set (cutting-garden#292), and two ops naming
// one new tag create its mailbox once.
func TestBulkMutate_BatchesThreadPatchesIntoOneEmailSet(t *testing.T) {
	srv := newBulkFixture(t)

	result := bulkMutate(t,
		bulkPatch(bulkT3(), `{"tags":["_flagged","_inbox","payee-acme"]}`),
		bulkPatch(bulkT1(), `{"tags":["_inbox","_unread","payee-acme","payee-one_medical"]}`),
	)

	if got := srv.EmailSetCalls(); got != 1 {
		t.Errorf("Email/set calls = %d, want 1", got)
	}
	want := []string{"tags", "mailbox:payee/-acme"}
	assertOutcome(t, "T3", outcomeAt(t, result, 0), bulkT3(), cutting_garden_plugins.OutcomeApplied, want)
	assertOutcome(t, "T1", outcomeAt(t, result, 1), bulkT1(), cutting_garden_plugins.OutcomeApplied, want)

	assertStrings(t, "T3 tags", liveThreadTags(t, testAccount, "T3"),
		[]string{"_flagged", "_inbox", "payee-acme"})
	assertStrings(t, "T1 tags", liveThreadTags(t, testAccount, "T1"),
		[]string{"_inbox", "_unread", "payee-acme", "payee-one_medical"})

	tree, err := mutateClient(t, testAccount).loadMailboxTree(context.Background())
	if err != nil {
		t.Fatalf("loadMailboxTree: %v", err)
	}
	acme := 0
	for _, mbox := range tree.byID {
		if mbox.Name == "-acme" {
			acme++
		}
	}
	if acme != 1 {
		t.Errorf("mailboxes named -acme = %d, want 1 (one create shared by both ops)", acme)
	}
}

// A move and a tag edit batch together, and each reports its own fields; the
// move reports the thread's new URI.
func TestBulkMutate_MoveAndTagEdit(t *testing.T) {
	srv := newBulkFixture(t)

	result := bulkMutate(t,
		bulkPatch(bulkT1(), `{"mailbox":"Archive"}`),
		bulkPatch(bulkT3(), `{"tags":["_flagged"]}`),
	)

	if got := srv.EmailSetCalls(); got != 1 {
		t.Errorf("Email/set calls = %d, want 1", got)
	}
	move := outcomeAt(t, result, 0)
	assertOutcome(t, "T1 move", move, bulkT1(), cutting_garden_plugins.OutcomeApplied, []string{"mailbox"})
	if want := threadURI(testAccount, []string{"Archive"}, "T1"); move.NewURI == nil ||
		move.NewURI.String() != want.String() {
		t.Errorf("T1 move NewURI = %v, want %v", move.NewURI, want)
	}
	assertOutcome(t, "T3 tags", outcomeAt(t, result, 1), bulkT3(),
		cutting_garden_plugins.OutcomeApplied, []string{"tags"})

	c := mutateClient(t, testAccount)
	assertStrings(t, "e1 mailboxIds", mailboxIDsOf(t, c, "e1"), []string{"mb-archive", "mb-inbox"})
	assertStrings(t, "e3 mailboxIds", mailboxIDsOf(t, c, "e3"), []string{"mb-archive"})
}

// Two identical ops on one thread merge and share the result; a later op on
// the same thread asking for something else fails, as the batch has no order
// for one thread's records.
func TestBulkMutate_DuplicateThreadOps(t *testing.T) {
	srv := newBulkFixture(t)

	result := bulkMutate(t,
		bulkPatch(bulkT3(), `{"tags":["_flagged"]}`),
		bulkPatch(bulkT3(), `{"tags": ["_flagged"]}`),
		bulkPatch(bulkT3(), `{"tags":["_inbox"]}`),
	)

	if got := srv.EmailSetCalls(); got != 1 {
		t.Errorf("Email/set calls = %d, want 1", got)
	}
	assertOutcome(t, "first", outcomeAt(t, result, 0), bulkT3(), cutting_garden_plugins.OutcomeApplied, []string{"tags"})
	assertOutcome(t, "identical", outcomeAt(t, result, 1), bulkT3(), cutting_garden_plugins.OutcomeApplied, []string{"tags"})
	conflict := outcomeAt(t, result, 2)
	assertOutcome(t, "conflicting", conflict, bulkT3(), cutting_garden_plugins.OutcomeFailed, nil)
	assertErrContains(t, "conflicting", conflict.Err, "T3", "patched twice")

	assertStrings(t, "T3 tags", liveThreadTags(t, testAccount, "T3"), []string{"_flagged"})
}

// A member the server refuses fails its own op, naming the member and the
// SetError; the other op in the batch lands.
func TestBulkMutate_IsolatesARefusedMember(t *testing.T) {
	srv := newBulkFixture(t)
	srv.RefuseEmailUpdate("e3")

	result := bulkMutate(t,
		bulkPatch(bulkT1(), `{"tags":["_flagged","_inbox","_unread","payee-one_medical"]}`),
		bulkPatch(bulkT3(), `{"tags":["_inbox"]}`),
	)

	assertOutcome(t, "T1", outcomeAt(t, result, 0), bulkT1(), cutting_garden_plugins.OutcomeApplied, []string{"tags"})
	refused := outcomeAt(t, result, 1)
	assertOutcome(t, "T3", refused, bulkT3(), cutting_garden_plugins.OutcomeFailed, nil)
	assertErrContains(t, "T3", refused.Err, "e3", "forbidden")

	assertStrings(t, "T1 tags", liveThreadTags(t, testAccount, "T1"),
		[]string{"_flagged", "_inbox", "_unread", "payee-one_medical"})
	assertStrings(t, "T3 tags", liveThreadTags(t, testAccount, "T3"), []string{"_flagged", "_inbox"})
}

// A mailbox create the server refuses fails every op that needed it, naming
// the mailbox and the SetError; an op that did not need it lands.
func TestBulkMutate_RefusedCreateFailsItsOps(t *testing.T) {
	srv := newBulkFixture(t)
	srv.RefuseMailboxName("-acme")

	result := bulkMutate(t,
		bulkPatch(bulkT3(), `{"tags":["_flagged","_inbox","payee-acme"]}`),
		bulkPatch(bulkT1(), `{"tags":["_flagged","_inbox","_unread","payee-one_medical"]}`),
	)

	refused := outcomeAt(t, result, 0)
	assertOutcome(t, "T3", refused, bulkT3(), cutting_garden_plugins.OutcomeFailed, nil)
	assertErrContains(t, "T3", refused.Err, "payee/-acme", "forbidden")
	assertOutcome(t, "T1", outcomeAt(t, result, 1), bulkT1(), cutting_garden_plugins.OutcomeApplied, []string{"tags"})

	assertStrings(t, "T3 tags", liveThreadTags(t, testAccount, "T3"), []string{"_flagged", "_inbox"})
	assertStrings(t, "T1 tags", liveThreadTags(t, testAccount, "T1"),
		[]string{"_flagged", "_inbox", "_unread", "payee-one_medical"})
}

// The Email state moving once between the read and the write (new mail
// arriving anywhere in the account) costs one re-read and re-plan, and the
// batch lands. The re-plan reuses the mailbox the first attempt created.
func TestBulkMutate_OneStateMismatchIsReplannedOnce(t *testing.T) {
	srv := newBulkFixture(t)
	srv.SimulateConcurrentEmailChanges(1)

	result := bulkMutate(t,
		bulkPatch(bulkT3(), `{"tags":["_flagged","_inbox","payee-acme"]}`),
		bulkPatch(bulkT1(), `{"mailbox":"Archive"}`),
	)

	assertOutcome(t, "T3", outcomeAt(t, result, 0), bulkT3(),
		cutting_garden_plugins.OutcomeApplied, []string{"tags", "mailbox:payee/-acme"})
	assertOutcome(t, "T1", outcomeAt(t, result, 1), bulkT1(),
		cutting_garden_plugins.OutcomeApplied, []string{"mailbox"})
	if got := srv.EmailSetCalls(); got != 2 {
		t.Errorf("Email/set calls = %d, want 2 (the refused one and the re-planned one)", got)
	}
	if got := srv.MailboxSetCalls(); got != 1 {
		t.Errorf("Mailbox/set calls = %d, want 1 (the re-plan reuses the created mailbox)", got)
	}
	assertStrings(t, "T3 tags", liveThreadTags(t, testAccount, "T3"),
		[]string{"_flagged", "_inbox", "payee-acme"})
}

// A second mismatch, after the one re-plan, fails the whole batch with nothing
// written, and the error says to re-plan and retry.
func TestBulkMutate_StateMismatchFailsTheBatch(t *testing.T) {
	srv := newBulkFixture(t)
	srv.SimulateConcurrentEmailChanges(2)

	result := bulkMutate(t,
		bulkPatch(bulkT1(), `{"mailbox":"Archive"}`),
		bulkPatch(bulkT3(), `{"tags":["_flagged"]}`),
	)

	for i, uri := range []*url.URL{bulkT1(), bulkT3()} {
		got := outcomeAt(t, result, i)
		assertOutcome(t, uri.String(), got, uri, cutting_garden_plugins.OutcomeFailed, nil)
		assertErrContains(t, uri.String(), got.Err, "stateMismatch", "re-plan")
	}
	c := mutateClient(t, testAccount)
	assertStrings(t, "e1 mailboxIds", mailboxIDsOf(t, c, "e1"), []string{"mb-inbox", "mb-one_medical"})
	assertStrings(t, "e3 mailboxIds", mailboxIDsOf(t, c, "e3"), []string{"mb-inbox"})
}

// A stateMismatch does not roll back the mailbox creates that ran before the
// Email/set in the same request, so a retry re-plans against them: it
// succeeds and creates nothing new.
func TestBulkMutate_RetryAfterStateMismatchCreatesNothingNew(t *testing.T) {
	srv := newBulkFixture(t)
	srv.SimulateConcurrentEmailChanges(2)
	op := bulkPatch(bulkT3(), `{"tags":["_flagged","_inbox","payee-acme"]}`)

	first := bulkMutate(t, op)
	assertOutcome(t, "first", outcomeAt(t, first, 0), bulkT3(), cutting_garden_plugins.OutcomeFailed, nil)
	createsBefore := srv.MailboxSetCalls()

	retry := bulkMutate(t, op)
	assertOutcome(t, "retry", outcomeAt(t, retry, 0), bulkT3(),
		cutting_garden_plugins.OutcomeApplied, []string{"tags"})
	if got := srv.MailboxSetCalls(); got != createsBefore {
		t.Errorf("Mailbox/set calls went from %d to %d on the retry, want no new create", createsBefore, got)
	}
	assertStrings(t, "T3 tags", liveThreadTags(t, testAccount, "T3"),
		[]string{"_flagged", "_inbox", "payee-acme"})
}

// A patch naming no recognized key, and one restating the live set, apply
// nothing, report their ignored keys, and cost no Email/set; an op that is not
// a thread patch takes the single-node path and reports its refusal. Outcomes
// stay in request order across the two paths.
func TestBulkMutate_NothingToWriteAndNonThreadOps(t *testing.T) {
	srv := newBulkFixture(t)

	inbox := mailboxURI(testAccount, []string{"Inbox"})
	result := bulkMutate(t,
		bulkPatch(bulkT3(), `{"subject":"x"}`),
		bulkPatch(inbox, `{"tags":["_flagged"]}`),
		bulkPatch(bulkT1(), `{"tags":["_inbox","_unread","payee-one_medical"],"from":"x"}`),
	)

	if got := srv.EmailSetCalls(); got != 0 {
		t.Errorf("Email/set calls = %d, want 0", got)
	}
	nothing := outcomeAt(t, result, 0)
	assertOutcome(t, "unrecognized", nothing, bulkT3(), cutting_garden_plugins.OutcomePatchedNothing, []string{})
	assertStrings(t, "unrecognized ignored", nothing.Ignored, []string{"subject"})

	notThread := outcomeAt(t, result, 1)
	assertOutcome(t, "mailbox node", notThread, inbox, cutting_garden_plugins.OutcomeFailed, nil)
	assertErrContains(t, "mailbox node", notThread.Err, "only thread nodes")

	restated := outcomeAt(t, result, 2)
	assertOutcome(t, "restated", restated, bulkT1(), cutting_garden_plugins.OutcomePatchedNothing, []string{})
	assertStrings(t, "restated ignored", restated.Ignored, []string{"from"})
}

// A sweep resolves its matches with the enriched listing and batches the op
// over them.
func TestBulkMutate_Sweep(t *testing.T) {
	srv := newBulkFixture(t)

	result := bulkMutateRequest(t, cutting_garden_plugins.BulkRequest{
		Atomicity: cutting_garden_plugins.BulkBestEffort,
		Sweep: &cutting_garden_plugins.BulkSweep{
			Root:   mailboxURI(testAccount, []string{"Inbox"}),
			Filter: cutting_garden_plugins.FacetFilter{{Dimension: facetTags, Value: stateTagFlagged}},
			Op:     cutting_garden_plugins.BulkOp{Kind: cutting_garden_plugins.BulkPatch, Body: []byte(`{"mailbox":"Archive"}`)},
		},
	})

	if got := srv.EmailSetCalls(); got != 1 {
		t.Errorf("Email/set calls = %d, want 1", got)
	}
	if len(result.Outcomes) != 1 {
		t.Fatalf("outcomes = %+v, want one (T3, the only flagged thread in the Inbox)", result.Outcomes)
	}
	assertOutcome(t, "T3", result.Outcomes[0], bulkT3(), cutting_garden_plugins.OutcomeApplied, []string{"mailbox"})
	assertStrings(t, "e3 mailboxIds", mailboxIDsOf(t, mutateClient(t, testAccount), "e3"), []string{"mb-archive"})
}

// Atomic is refused: JMAP /set applies per record (RFC 8620 §5.3), so the
// batch cannot be all-or-nothing.
func TestBulkMutate_RefusesAtomic(t *testing.T) {
	srv := newBulkFixture(t)
	bm, ok := any(Plugin{}).(cutting_garden_plugins.BulkMutator)
	if !ok {
		t.Fatal("fastmail Plugin is not a BulkMutator")
	}
	_, err := bm.BulkMutate(context.Background(), cutting_garden_plugins.BulkRequest{
		Atomicity: cutting_garden_plugins.BulkAtomic,
		Ops:       []cutting_garden_plugins.BulkOp{bulkPatch(bulkT3(), `{"tags":["_flagged"]}`)},
	})
	if !errors.Is(err, cutting_garden_plugins.ErrBulkAtomicUnsupported) {
		t.Errorf("atomic err = %v, want ErrBulkAtomicUnsupported", err)
	}
	if got := srv.EmailSetCalls(); got != 0 {
		t.Errorf("Email/set calls = %d, want 0", got)
	}
}
