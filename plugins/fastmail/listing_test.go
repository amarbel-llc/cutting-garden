package fastmail

import (
	"context"
	"slices"
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
	// Only the thread carries the tag set (G6); the mailbox its counts.
	if !slices.Contains(byTag[typeThread], listingFieldTags) {
		t.Errorf("thread listing fields %v lack %q", byTag[typeThread], listingFieldTags)
	}
	for _, tag := range []string{typeEmail, typeMailbox} {
		if slices.Contains(byTag[tag], listingFieldTags) {
			t.Errorf("%s declares a %q listing field; only the thread has one", tag, listingFieldTags)
		}
	}
	if !slices.Contains(byTag[typeMailbox], listingFieldThreads) || !slices.Contains(byTag[typeMailbox], listingFieldEmails) {
		t.Errorf("mailbox listing fields = %v, want the threads/emails counts", byTag[typeMailbox])
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
	// T1 is in the inbox and fully read: its tag set is the leaf label plus
	// _inbox, carried identically as a facet and as the tags listing field.
	wantTags := []string{stateTagInbox, "receipts"}
	var facetTagKeys []string
	for _, v := range july.Facets[facetTags] {
		facetTagKeys = append(facetTagKeys, v.Key)
	}
	if !slices.Equal(facetTagKeys, wantTags) {
		t.Errorf("July tags facet = %v, want %v", facetTagKeys, wantTags)
	}
	if got, _ := july.Fields[listingFieldTags].([]string); !slices.Equal(got, wantTags) {
		t.Errorf("July tags field = %v, want %v", july.Fields[listingFieldTags], wantTags)
	}
	if july.Fields[listingFieldSubject] != "Your July receipt" {
		t.Errorf("July subject field = %v", july.Fields[listingFieldSubject])
	}
	if july.Fields[listingFieldDate] != "2026-07-14T09:12:03Z" {
		t.Errorf("July date field = %v, want the newest receivedAt", july.Fields[listingFieldDate])
	}
}

func TestListEnriched_Filter(t *testing.T) {
	account := newFixture(t)
	nodes, ok, err := (Plugin{}).ListEnriched(
		context.Background(), receiptsURI(account),
		cutting_garden_plugins.FacetFilter{{Dimension: facetTags, Value: stateTagUnread}},
	)
	if err != nil || !ok {
		t.Fatalf("ListEnriched(filter): ok=%v err=%v", ok, err)
	}
	// Only the thread with an unseen member (June/T2) survives the filter.
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
