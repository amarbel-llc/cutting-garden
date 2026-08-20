package cutting_garden_plugins

import (
	"reflect"
	"testing"
)

// The naive interpreter is the exact-match degenerate (RFC 0019): identity
// normalization/sorting, whole-dimension buckets only, exact matching, and
// exact-bucket membership completion.
func TestNaiveTagInterpreter(t *testing.T) {
	ti, ok := LookupTagInterpreter("naive")
	if !ok {
		t.Fatal("naive interpreter not registered")
	}

	if got := ti.Normalize("project-x"); got != "project-x" {
		t.Errorf("Normalize = %q, want identity", got)
	}
	if got := ti.SortKey("_inbox"); got != "_inbox" {
		t.Errorf("SortKey = %q, want identity (naive has no lift)", got)
	}

	tags := []string{"work", "errand"}
	ms, err := ti.Buckets(tags, "")
	if err != nil {
		t.Fatalf("Buckets: %v", err)
	}
	want := []TagMembership{{Bucket: "work", Via: "work"}, {Bucket: "errand", Via: "errand"}}
	if !reflect.DeepEqual(ms, want) {
		t.Errorf("Buckets = %#v, want %#v", ms, want)
	}
	if _, err := ti.Buckets(tags, "project"); err == nil {
		t.Error("naive Buckets with a namespace must be a bad request")
	}

	if !ti.Matches(tags, "work") || ti.Matches(tags, "wor") {
		t.Error("naive Matches must be exact")
	}

	added, err := ti.Complete(tags, TagAdd, "urgent")
	if err != nil {
		t.Fatalf("Complete add: %v", err)
	}
	if !reflect.DeepEqual(added, []string{"work", "errand", "urgent"}) {
		t.Errorf("Complete add = %v", added)
	}
	removed, err := ti.Complete(tags, TagRemove, "work")
	if err != nil {
		t.Fatalf("Complete remove: %v", err)
	}
	if !reflect.DeepEqual(removed, []string{"errand"}) {
		t.Errorf("Complete remove = %v", removed)
	}
}

func TestLookupTagInterpreter_Unknown(t *testing.T) {
	if _, ok := LookupTagInterpreter("bogus"); ok {
		t.Error("unknown interpreter must not resolve")
	}
}
