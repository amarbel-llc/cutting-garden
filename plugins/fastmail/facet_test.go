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

	// read: T1 read, T2 unread (any member unseen).
	if got := count(s, facetRead, readValueRead); got != 1 {
		t.Errorf("read=read = %d, want 1", got)
	}
	if got := count(s, facetRead, readValueUnread); got != 1 {
		t.Errorf("read=unread = %d, want 1", got)
	}
	// flagged: T2 has a flagged member, T1 none.
	if got := count(s, facetFlagged, flaggedValueYes); got != 1 {
		t.Errorf("flagged = %d, want 1", got)
	}
	// folder: T1 in inbox, T2 in archive (role-mailbox union).
	if count(s, facetFolder, "inbox") != 1 || count(s, facetFolder, "archive") != 1 {
		t.Errorf("folder = %v, want inbox=1 archive=1", s[facetFolder])
	}
	// tag: both threads carry the user-tag path (membership counts).
	if got := count(s, facetTag, "area/finance/receipts"); got != 2 {
		t.Errorf("tag[area/finance/receipts] = %d, want 2", got)
	}
	// from: acme in both, bob in T2 only.
	if got := count(s, facetFrom, "billing@acme.example"); got != 2 {
		t.Errorf("from[acme] = %d, want 2", got)
	}
	if got := count(s, facetFrom, "bob@example.test"); got != 1 {
		t.Errorf("from[bob] = %d, want 1", got)
	}
	// year: both 2026.
	if got := count(s, facetYear, "2026"); got != 2 {
		t.Errorf("year[2026] = %d, want 2", got)
	}
	// has_attachment: T1 yes, T2 no.
	if count(s, facetHasAttachment, attachmentYes) != 1 || count(s, facetHasAttachment, attachmentNo) != 1 {
		t.Errorf("has_attachment = %v", s[facetHasAttachment])
	}
}

func TestFacetCounts_Filter(t *testing.T) {
	account := newFixture(t)
	// Only the unread thread (T2) should survive.
	res, ok := facetCounts(t, receiptsURI(account).String(),
		cutting_garden_plugins.FacetFilter{{Dimension: facetRead, Value: readValueUnread}})
	if !ok {
		t.Fatal("ok=false")
	}
	if got := count(res.Summary, facetYear, "2026"); got != 1 {
		t.Errorf("filtered year[2026] = %d, want 1 (T2 only)", got)
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
