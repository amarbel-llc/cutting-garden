package fastmail

import (
	"context"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

func listRoots(t *testing.T, raw string) []cutting_garden_plugins.Node {
	t.Helper()
	nodes, err := (Plugin{}).ListRoots(context.Background(), mustParseURL(t, raw))
	if err != nil {
		t.Fatalf("ListRoots(%q): %v", raw, err)
	}
	return nodes
}

func nodeByName(nodes []cutting_garden_plugins.Node, name string) (cutting_garden_plugins.Node, bool) {
	for _, n := range nodes {
		if n.Name == name {
			return n, true
		}
	}
	return cutting_garden_plugins.Node{}, false
}

func TestListRoots_AccountRoot_TopLevelInScope(t *testing.T) {
	account := newFixture(t)
	nodes := listRoots(t, accountRootURI(account).String())

	names := map[string]string{} // name -> type
	for _, n := range nodes {
		names[n.Name] = n.Type
	}
	for _, want := range []string{"Inbox", "Archive", "area", "payee"} {
		if names[want] != typeMailbox {
			t.Errorf("account root missing top-level mailbox %q (got type %q)", want, names[want])
		}
	}
	if _, ok := names["Drafts"]; ok {
		t.Error("account root leaked the excluded Drafts mailbox")
	}
}

func TestListRoots_MailboxDirectCounts(t *testing.T) {
	account := newFixture(t)

	// Inbox holds exactly the single-message thread T1 (e1).
	inbox, ok := nodeByName(listRoots(t, accountRootURI(account).String()), "Inbox")
	if !ok {
		t.Fatal("no Inbox mailbox")
	}
	if got := inbox.Fields[listingFieldEmails]; got != 1 {
		t.Errorf("Inbox emails = %v, want 1", got)
	}
	if got := inbox.Fields[listingFieldThreads]; got != 1 {
		t.Errorf("Inbox threads = %v, want 1", got)
	}

	// receipts holds 3 messages (e1,e2,e3) across 2 threads (T1,T2).
	area, _ := nodeByName(listRoots(t, accountRootURI(account).String()), "area")
	finance, _ := nodeByName(listRoots(t, area.URIString()), "finance")
	receipts, ok := nodeByName(listRoots(t, finance.URIString()), "receipts")
	if !ok {
		t.Fatal("no receipts mailbox")
	}
	if got := receipts.Fields[listingFieldEmails]; got != 3 {
		t.Errorf("receipts emails = %v, want 3", got)
	}
	if got := receipts.Fields[listingFieldThreads]; got != 2 {
		t.Errorf("receipts threads = %v, want 2", got)
	}
}

func TestListRoots_Descent_ToRaw(t *testing.T) {
	account := newFixture(t)

	// account -> area -> finance -> receipts
	area, ok := nodeByName(listRoots(t, accountRootURI(account).String()), "area")
	if !ok {
		t.Fatal("no area mailbox")
	}
	finance, ok := nodeByName(listRoots(t, area.URIString()), "finance")
	if !ok {
		t.Fatal("no finance child under area")
	}
	receipts, ok := nodeByName(listRoots(t, finance.URIString()), "receipts")
	if !ok {
		t.Fatal("no receipts child under finance")
	}

	// receipts -> two thread nodes, newest first (T1 July, then T2 June).
	threads := listRoots(t, receipts.URIString())
	if len(threads) != 2 {
		t.Fatalf("want 2 threads under receipts, got %d", len(threads))
	}
	if threads[0].Type != typeThread || threads[0].Name != "Your July receipt" {
		t.Errorf("first thread = %q (%s), want July receipt", threads[0].Name, threads[0].Type)
	}
	if threads[1].Name != "June receipt" {
		t.Errorf("second thread = %q, want June receipt", threads[1].Name)
	}

	// July thread -> one email; June thread -> two emails (oldest-first).
	july := listRoots(t, threads[0].URIString())
	if len(july) != 1 || july[0].Type != typeEmail {
		t.Fatalf("July thread children = %d, want 1 email", len(july))
	}
	june := listRoots(t, threads[1].URIString())
	if len(june) != 2 {
		t.Fatalf("June thread children = %d, want 2 emails", len(june))
	}

	// email -> raw leaf only.
	rawChildren := listRoots(t, july[0].URIString())
	if len(rawChildren) != 1 || rawChildren[0].Type != typeEmailRaw {
		t.Fatalf("email children = %v, want one raw leaf", rawChildren)
	}
	// raw leaf has no children.
	if leaves := listRoots(t, rawChildren[0].URIString()); len(leaves) != 0 {
		t.Errorf("raw leaf has %d children, want 0", len(leaves))
	}

	// email nodes carry their cheap listing fields.
	if july[0].Fields[listingFieldSubject] != "Your July receipt" {
		t.Errorf("email subject field = %v", july[0].Fields[listingFieldSubject])
	}
	if july[0].Fields[listingFieldFrom] != "billing@acme.example" {
		t.Errorf("email from field = %v", july[0].Fields[listingFieldFrom])
	}
}
