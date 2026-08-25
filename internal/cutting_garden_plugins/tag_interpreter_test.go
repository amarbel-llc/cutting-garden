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

// dodder-hyphen (RFC 0019 §6, §7): hyphen segments form a hierarchy, a
// namespace grouping rolls deeper tags up to their immediate next segment with
// Via naming the producing tag, bare-term matching is transitive along the
// segment path, write-back removes a bucket's whole subtree, and `_` is a
// literal character with no lift.
func TestDodderHyphenTagInterpreter_Buckets(t *testing.T) {
	ti, ok := LookupTagInterpreter("dodder-hyphen")
	if !ok {
		t.Fatal("dodder-hyphen interpreter not registered")
	}

	tags := []string{
		"project-cutting_garden",
		"project-client-acme",
		"project-client-baxter",
	}

	// §6.1 immediate-segment rollup + Via: project groups to -cutting_garden
	// and -client; the two client tags coalesce into ONE -client membership
	// (per-node dedup), and each bucket's Via is one of its contributors.
	ms, err := ti.Buckets(tags, "project")
	if err != nil {
		t.Fatalf("Buckets(project): %v", err)
	}
	want := []TagMembership{
		{Bucket: "-cutting_garden", Via: "project-cutting_garden"},
		{Bucket: "-client", Via: "project-client-acme"},
	}
	if !reflect.DeepEqual(ms, want) {
		t.Errorf("Buckets(project) = %#v, want %#v", ms, want)
	}
	// The coalesced bucket's Via MUST be one of its real contributors.
	for _, m := range ms {
		if m.Bucket == "-client" &&
			m.Via != "project-client-acme" && m.Via != "project-client-baxter" {
			t.Errorf("-client Via = %q, want a real contributor", m.Via)
		}
	}

	// §6.1 drill-down: grouping by the deeper namespace buckets the leaves
	// separately.
	deeper, err := ti.Buckets(tags, "project-client")
	if err != nil {
		t.Fatalf("Buckets(project-client): %v", err)
	}
	wantDeeper := []TagMembership{
		{Bucket: "-acme", Via: "project-client-acme"},
		{Bucket: "-baxter", Via: "project-client-baxter"},
	}
	if !reflect.DeepEqual(deeper, wantDeeper) {
		t.Errorf("Buckets(project-client) = %#v, want %#v", deeper, wantDeeper)
	}

	// A tag equal to the namespace (no segment beneath it) and a tag not under
	// the namespace both contribute nothing; an empty result is normal.
	empty, err := ti.Buckets([]string{"project", "other"}, "project")
	if err != nil {
		t.Fatalf("Buckets(namespace-only): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("Buckets over [project, other] namespace project = %#v, want empty", empty)
	}
}

func TestDodderHyphenTagInterpreter_WholeDimension(t *testing.T) {
	ti, _ := LookupTagInterpreter("dodder-hyphen")

	// Whole-dimension parity with naive: one membership per distinct tag,
	// Bucket == Via == tag.
	ms, err := ti.Buckets([]string{"work", "urgent", "work"}, "")
	if err != nil {
		t.Fatalf("Buckets(\"\"): %v", err)
	}
	want := []TagMembership{
		{Bucket: "work", Via: "work"},
		{Bucket: "urgent", Via: "urgent"},
	}
	if !reflect.DeepEqual(ms, want) {
		t.Errorf("whole-dimension Buckets = %#v, want %#v", ms, want)
	}
}

func TestDodderHyphenTagInterpreter_Matches(t *testing.T) {
	ti, _ := LookupTagInterpreter("dodder-hyphen")

	// §6.2 transitive matching along segment boundaries.
	if !ti.Matches([]string{"project-client-acme"}, "project") {
		t.Error("project must match project-client-acme (segment-prefix)")
	}
	if !ti.Matches([]string{"project-client-acme"}, "project-client") {
		t.Error("project-client must match project-client-acme")
	}
	if ti.Matches([]string{"project"}, "pro") {
		t.Error("pro must NOT match project (not a segment boundary)")
	}
	if !ti.Matches([]string{"project-client-acme"}, "project-client-acme") {
		t.Error("exact tag must match")
	}

	// work is a segment-prefix of work-thing, so it matches transitively; it
	// also matches itself exactly.
	if !ti.Matches([]string{"work"}, "work") {
		t.Error("work must match work exactly")
	}
	if !ti.Matches([]string{"work-thing"}, "work") {
		t.Error("work must match work-thing transitively (segment-prefix)")
	}
}

func TestDodderHyphenTagInterpreter_Complete(t *testing.T) {
	ti, _ := LookupTagInterpreter("dodder-hyphen")

	// TagAdd appends the (full) bucket tag when absent.
	added, err := ti.Complete([]string{"other"}, TagAdd, "project-client")
	if err != nil {
		t.Fatalf("Complete add: %v", err)
	}
	if !reflect.DeepEqual(added, []string{"other", "project-client"}) {
		t.Errorf("Complete add = %v", added)
	}

	// TagRemove is EXACT: it drops only the bucket tag, keeping deeper tags
	// like project-client-acme (the apply layer, which holds the namespace,
	// enumerates a subtree; Complete does not).
	base := []string{"project-client", "project-client-acme", "other"}
	removed, err := ti.Complete(base, TagRemove, "project-client")
	if err != nil {
		t.Fatalf("Complete remove: %v", err)
	}
	if !reflect.DeepEqual(removed, []string{"project-client-acme", "other"}) {
		t.Errorf("Complete exact remove = %v, want [project-client-acme other]", removed)
	}
	// Input MUST NOT be mutated by the remove.
	if !reflect.DeepEqual(base, []string{"project-client", "project-client-acme", "other"}) {
		t.Errorf("Complete remove mutated input: %v", base)
	}

	// Whole-dimension sibling preservation: removing a node from the `work`
	// bucket MUST NOT strip an independent `work-urgent` tag it carries — the
	// reason removal is exact, not segment-prefix.
	sibling, err := ti.Complete(
		[]string{"work", "work-urgent", "other"}, TagRemove, "work",
	)
	if err != nil {
		t.Fatalf("Complete sibling remove: %v", err)
	}
	if !reflect.DeepEqual(sibling, []string{"work-urgent", "other"}) {
		t.Errorf("Complete remove work = %v, want [work-urgent other] (sibling survives)", sibling)
	}

	// Idempotence: add of a present tag and remove of an absent subtree both
	// return the set's content unchanged.
	present := []string{"a", "project-client"}
	noop, err := ti.Complete(present, TagAdd, "project-client")
	if err != nil {
		t.Fatalf("Complete add idempotent: %v", err)
	}
	if !reflect.DeepEqual(noop, present) {
		t.Errorf("idempotent add = %v, want %v", noop, present)
	}
	absent := []string{"a", "b"}
	noopRemove, err := ti.Complete(absent, TagRemove, "project-client")
	if err != nil {
		t.Fatalf("Complete remove idempotent: %v", err)
	}
	if !reflect.DeepEqual(noopRemove, absent) {
		t.Errorf("idempotent remove = %v, want %v", noopRemove, absent)
	}
}

func TestDodderHyphenTagInterpreter_UnderscoreLiteral(t *testing.T) {
	ti, _ := LookupTagInterpreter("dodder-hyphen")

	// §7: `_` is literal — no lift, no alias. _inbox and inbox are distinct.
	if got := ti.Normalize("_inbox"); got != "_inbox" {
		t.Errorf("Normalize(_inbox) = %q, want identity (no lift)", got)
	}
	if ti.Normalize("_inbox") == ti.Normalize("inbox") {
		t.Error("_inbox and inbox must be distinct tags under dodder-hyphen")
	}
	if got := ti.SortKey("_ inbox"); got != "_ inbox" {
		t.Errorf("SortKey(_ inbox) = %q, want identity", got)
	}
}
