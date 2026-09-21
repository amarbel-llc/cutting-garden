package fastmail

import (
	"context"
	"fmt"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/fastmail/fastmailtestserver"
)

func facetCounts(
	t *testing.T, node string, filter cutting_garden_plugins.FacetFilter,
) (cutting_garden_plugins.FacetResult, bool) {
	t.Helper()
	res, ok, err := (Plugin{}).FacetCounts(context.Background(), mustParseURL(t, node), filter)
	if err != nil {
		t.Fatalf("FacetCounts(%q): %v", node, err)
	}
	return res, ok
}

func count(s cutting_garden_plugins.FacetSummary, dim, key string) int64 {
	return s[dim][key]
}

func TestFacetCounts_Derivation(t *testing.T) {
	account := newFixture(t)
	res, ok := facetCounts(t, receiptsURI(account).String(), nil)
	if !ok {
		t.Fatal("FacetCounts(receipts) ok=false, want true")
	}
	if !res.Complete {
		t.Error("FacetCounts(receipts) Complete=false, want true")
	}
	s := res.Summary

	// tags: both threads carry the leaf label tag (membership counts); the
	// state tags derive from the members — T1 is in the inbox and fully
	// read, T2 has an unseen member and a flagged one, and neither is in
	// the trash. archive contributes nothing (D2).
	if got := count(s, facetTags, "receipts"); got != 2 {
		t.Errorf("tags[receipts] = %d, want 2", got)
	}
	if got := count(s, facetTags, stateTagInbox); got != 1 {
		t.Errorf("tags[_inbox] = %d, want 1", got)
	}
	if got := count(s, facetTags, stateTagUnread); got != 1 {
		t.Errorf("tags[_unread] = %d, want 1", got)
	}
	if got := count(s, facetTags, stateTagFlagged); got != 1 {
		t.Errorf("tags[_flagged] = %d, want 1", got)
	}
	if got := count(s, facetTags, stateTagTrash); got != 0 {
		t.Errorf("tags[_trash] = %d, want 0", got)
	}
	if _, present := s[facetTags]["area/finance/receipts"]; present {
		t.Error("tags still carries the legacy slash path; want the dodder-hyphen join")
	}
	// from: acme in both, bob in T2 only.
	if got := count(s, facetFrom, "billing@acme.example"); got != 2 {
		t.Errorf("from[acme] = %d, want 2", got)
	}
	if got := count(s, facetFrom, "bob@example.test"); got != 1 {
		t.Errorf("from[bob] = %d, want 1", got)
	}
	// date: each thread's newest receivedAt as an ISO day bucket.
	if count(s, facetDate, "2026-07-14") != 1 || count(s, facetDate, "2026-06-03") != 1 {
		t.Errorf("date = %v, want 2026-07-14=1 2026-06-03=1", s[facetDate])
	}
	for _, retired := range []string{"read", "flagged", "folder", "year", "tag"} {
		if _, present := s[retired]; present {
			t.Errorf("retired dimension %q still counted: %v", retired, s[retired])
		}
	}
	// has_attachment: T1 yes, T2 no.
	if count(s, facetHasAttachment, attachmentYes) != 1 || count(s, facetHasAttachment, attachmentNo) != 1 {
		t.Errorf("has_attachment = %v", s[facetHasAttachment])
	}
}

func TestFacetCounts_Filter(t *testing.T) {
	account := newFixture(t)
	// Only the thread with an unseen member (T2) should survive.
	res, ok := facetCounts(t, receiptsURI(account).String(),
		cutting_garden_plugins.FacetFilter{{Dimension: facetTags, Value: stateTagUnread}})
	if !ok {
		t.Fatal("ok=false")
	}
	if got := count(res.Summary, facetDate, "2026-06-03"); got != 1 {
		t.Errorf("filtered date[2026-06-03] = %d, want 1 (T2 only)", got)
	}
	if got := count(res.Summary, facetTags, stateTagInbox); got != 0 {
		t.Errorf("filtered tags[_inbox] = %d, want 0 (T1 excluded)", got)
	}
	if got := count(res.Summary, facetFrom, "bob@example.test"); got != 1 {
		t.Errorf("filtered from[bob] = %d, want 1", got)
	}
}

func TestFacetCounts_NonMailbox(t *testing.T) {
	account := newFixture(t)
	if _, ok := facetCounts(t, accountRootURI(account).String(), nil); ok {
		t.Error("FacetCounts(account root) ok=true, want false")
	}
}

func TestFacetVersion(t *testing.T) {
	account := newFixture(t)
	token, ok, err := (Plugin{}).FacetVersion(context.Background(), receiptsURI(account))
	if err != nil {
		t.Fatalf("FacetVersion: %v", err)
	}
	if !ok || token == "" {
		t.Errorf("FacetVersion ok=%v token=%q, want ok & non-empty", ok, token)
	}
	// A non-mailbox node has no token.
	if _, ok, _ := (Plugin{}).FacetVersion(context.Background(), accountRootURI(account)); ok {
		t.Error("FacetVersion(account root) ok=true, want false")
	}
}

func TestFacetCounts_FromTopNCap(t *testing.T) {
	srv := startWired(t)
	srv.AddMailbox("mb-bulk", "bulk", "", "")
	// Seed more distinct senders than the cap, one single-message thread each.
	for i := 0; i < fromTopN+5; i++ {
		id := fmt.Sprintf("b%d", i)
		srv.AddEmail(fastmailtestserver.Email{
			ID: id, ThreadID: "T" + id,
			MailboxIDs: []string{"mb-bulk"},
			Keywords:   []string{"$seen"},
			From:       []fastmailtestserver.Address{{Email: fmt.Sprintf("sender%02d@example.test", i)}},
			Subject:    "bulk", ReceivedAt: fmt.Sprintf("2026-01-%02dT00:00:00Z", (i%27)+1),
			BlobID: "blob-" + id,
		})
	}

	res, ok := facetCounts(t, mailboxURI(testAccount, []string{"bulk"}).String(), nil)
	if !ok {
		t.Fatal("ok=false")
	}
	if len(res.Summary[facetFrom]) != fromTopN {
		t.Errorf("from histogram size = %d, want cap %d", len(res.Summary[facetFrom]), fromTopN)
	}
	if res.Complete {
		t.Error("Complete=true after from cap, want false")
	}
}
