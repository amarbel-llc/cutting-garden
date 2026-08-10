package fastmail

import (
	"context"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

func TestDescribeListingFields(t *testing.T) {
	byTag := map[string][]string{}
	for _, ntf := range (Plugin{}).DescribeListingFields() {
		for _, f := range ntf.Fields {
			byTag[ntf.Tag] = append(byTag[ntf.Tag], f.Key)
		}
	}
	for _, tag := range []string{typeThread, typeEmail} {
		keys := byTag[tag]
		want := map[string]bool{listingFieldSubject: false, listingFieldFrom: false, listingFieldDate: false}
		for _, k := range keys {
			if _, ok := want[k]; ok {
				want[k] = true
			}
		}
		for k, seen := range want {
			if !seen {
				t.Errorf("%s missing listing field %q", tag, k)
			}
		}
	}
}

func TestListEnriched_Threads(t *testing.T) {
	account := newFixture(t)
	nodes, ok, err := (Plugin{}).ListEnriched(
		context.Background(), receiptsURI(account), nil,
	)
	if err != nil {
		t.Fatalf("ListEnriched(receipts): %v", err)
	}
	if !ok {
		t.Fatal("ListEnriched(receipts) ok=false, want true")
	}

	// Level-scoping: same node count as ListRoots (2 threads, no child mailboxes).
	if got := len(listRoots(t, receiptsURI(account).String())); got != len(nodes) {
		t.Errorf("ListEnriched returned %d nodes, ListRoots %d — must match", len(nodes), got)
	}

	july, ok := nodeByName(nodes, "Your July receipt")
	if !ok {
		t.Fatal("no July thread in enriched listing")
	}
	if july.Type != typeThread {
		t.Errorf("July node type = %q", july.Type)
	}
	if july.Facets[facetRead][0].Key != readValueRead {
		t.Errorf("July read facet = %v, want read", july.Facets[facetRead])
	}
	if july.Fields[listingFieldSubject] != "Your July receipt" {
		t.Errorf("July subject field = %v", july.Fields[listingFieldSubject])
	}
}

func TestListEnriched_Filter(t *testing.T) {
	account := newFixture(t)
	nodes, ok, err := (Plugin{}).ListEnriched(
		context.Background(), receiptsURI(account),
		cutting_garden_plugins.FacetFilter{{Dimension: facetRead, Value: readValueUnread}},
	)
	if err != nil || !ok {
		t.Fatalf("ListEnriched(filter): ok=%v err=%v", ok, err)
	}
	// Only the unread thread (June/T2) survives the filter.
	threads := 0
	for _, n := range nodes {
		if n.Type == typeThread {
			threads++
			if n.Name != "June receipt" {
				t.Errorf("filtered thread = %q, want June receipt", n.Name)
			}
		}
	}
	if threads != 1 {
		t.Errorf("filtered thread count = %d, want 1", threads)
	}
}

func TestListEnriched_ChildMailboxes(t *testing.T) {
	account := newFixture(t)
	// area has a child mailbox (finance) and no direct threads.
	nodes, ok, err := (Plugin{}).ListEnriched(
		context.Background(), mailboxURI(account, []string{"area"}), nil,
	)
	if err != nil || !ok {
		t.Fatalf("ListEnriched(area): ok=%v err=%v", ok, err)
	}
	if _, found := nodeByName(nodes, "finance"); !found {
		t.Error("ListEnriched(area) dropped the finance child mailbox")
	}
}

func TestListEnriched_DeclinesAccountRoot(t *testing.T) {
	account := newFixture(t)
	_, ok, err := (Plugin{}).ListEnriched(
		context.Background(), accountRootURI(account), nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("ListEnriched(account root) ok=true, want false (declines)")
	}
}
