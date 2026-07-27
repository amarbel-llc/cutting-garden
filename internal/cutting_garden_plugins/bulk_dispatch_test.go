package cutting_garden_plugins

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"slices"
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

func rawFields(s string) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	_ = json.Unmarshal([]byte(s), &m)
	return m
}

// fakeMutatorLister is a NodeMutator + EnrichedLister for the shared
// bulk-dispatch helper tests: it records the verbs it received, fails any URI
// in failOn, returns a configurable PatchNode applied set, and serves a
// configurable enriched listing for sweeps.
type fakeMutatorLister struct {
	created, put, patched, deleted []string
	patchApplied                   []string
	failOn                         map[string]bool

	enrichedNodes []Node
	enrichedOK    bool
	enrichedErr   error
}

func (*fakeMutatorLister) Schemes() []string { return []string{"fake"} }
func (*fakeMutatorLister) TypeTag() string   { return "fake-v1" }
func (*fakeMutatorLister) Types() []NodeType { return nil }

func (*fakeMutatorLister) ListRoots(
	context.Context, *url.URL,
) ([]Node, error) {
	return nil, nil
}

func (f *fakeMutatorLister) ListEnriched(
	context.Context, *url.URL, FacetFilter,
) ([]Node, bool, error) {
	return f.enrichedNodes, f.enrichedOK, f.enrichedErr
}

func (f *fakeMutatorLister) CreateNode(
	_ context.Context, uri *url.URL, _ io.Reader, _ string,
) error {
	if f.failOn[uri.String()] {
		return errors.BadRequestf("create failed")
	}
	f.created = append(f.created, uri.String())
	return nil
}

func (f *fakeMutatorLister) PutNode(
	_ context.Context, uri *url.URL, _ io.Reader,
) error {
	if f.failOn[uri.String()] {
		return errors.BadRequestf("put failed")
	}
	f.put = append(f.put, uri.String())
	return nil
}

func (f *fakeMutatorLister) PatchNode(
	_ context.Context, uri *url.URL, _ io.Reader,
) ([]string, error) {
	if f.failOn[uri.String()] {
		return nil, errors.BadRequestf("patch failed")
	}
	f.patched = append(f.patched, uri.String())
	return f.patchApplied, nil
}

func (f *fakeMutatorLister) DeleteNode(_ context.Context, uri *url.URL) error {
	if f.failOn[uri.String()] {
		return errors.BadRequestf("delete failed")
	}
	f.deleted = append(f.deleted, uri.String())
	return nil
}

func uriStringsOf(t *testing.T, uris []*url.URL) []string {
	t.Helper()
	out := make([]string, 0, len(uris))
	for _, u := range uris {
		out = append(out, u.String())
	}
	return out
}

// TestBestEffortBulkMutate_OpsChangeset: a changeset dispatches each op via
// the matching verb and reports every op applied.
func TestBestEffortBulkMutate_OpsChangeset(t *testing.T) {
	f := &fakeMutatorLister{}
	a := mustURL(t, "fake://h/a")
	b := mustURL(t, "fake://h/b")

	result, err := BestEffortBulkMutate(context.Background(), f, f, BulkRequest{
		Atomicity: BulkBestEffort,
		Ops: []BulkOp{
			{Kind: BulkCreate, URI: a, Type: "fake-v1"},
			{Kind: BulkDelete, URI: b},
		},
	})
	if err != nil {
		t.Fatalf("BestEffortBulkMutate: %v", err)
	}
	if got := uriStringsOf(t, result.AppliedNodes); !slices.Equal(
		got, []string{"fake://h/a", "fake://h/b"},
	) {
		t.Errorf("applied = %v, want [a b]", got)
	}
	if len(f.created) != 1 || len(f.deleted) != 1 {
		t.Errorf("created=%v deleted=%v, want one each", f.created, f.deleted)
	}
}

// TestBestEffortBulkMutate_PerNodeFailureIsolated: a per-node failure lands
// in Failed and the call itself does NOT error (best-effort).
func TestBestEffortBulkMutate_PerNodeFailureIsolated(t *testing.T) {
	f := &fakeMutatorLister{failOn: map[string]bool{"fake://h/missing": true}}
	good := mustURL(t, "fake://h/good")
	missing := mustURL(t, "fake://h/missing")

	result, err := BestEffortBulkMutate(context.Background(), f, f, BulkRequest{
		Atomicity: BulkBestEffort,
		Ops: []BulkOp{
			{Kind: BulkCreate, URI: good, Type: "fake-v1"},
			{Kind: BulkDelete, URI: missing},
		},
	})
	if err != nil {
		t.Fatalf("best-effort must not return a call error: %v", err)
	}
	if got := uriStringsOf(t, result.AppliedNodes); !slices.Equal(
		got, []string{"fake://h/good"},
	) {
		t.Errorf("applied = %v, want [good]", got)
	}
	if len(result.Failed) != 1 || result.Failed[0].URI.String() != "fake://h/missing" {
		t.Errorf("failed = %+v, want [missing]", result.Failed)
	}
}

// TestApplyBulkOp_PatchNothing: a patch that lands no recognized field goes
// to PatchedNothing — neither applied nor failed (#182).
func TestApplyBulkOp_PatchNothing(t *testing.T) {
	f := &fakeMutatorLister{patchApplied: []string{}}
	task := mustURL(t, "fake://h/task")

	var result BulkResult
	ApplyBulkOp(context.Background(), f, BulkOp{Kind: BulkPatch, URI: task}, &result)

	if len(result.AppliedNodes) != 0 || len(result.Failed) != 0 {
		t.Errorf("applied=%v failed=%v, want both empty", result.AppliedNodes, result.Failed)
	}
	if len(result.PatchedNothing) != 1 {
		t.Fatalf("patchedNothing = %v, want the one task", result.PatchedNothing)
	}
}

// TestBestEffortBulkMutate_AtomicRejected: a best-effort helper rejects an
// atomic request with the sentinel — never downgrades.
func TestBestEffortBulkMutate_AtomicRejected(t *testing.T) {
	f := &fakeMutatorLister{}
	_, err := BestEffortBulkMutate(context.Background(), f, f, BulkRequest{
		Atomicity: BulkAtomic,
		Ops:       []BulkOp{{Kind: BulkDelete, URI: mustURL(t, "fake://h/a")}},
	})
	if !errors.Is(err, ErrBulkAtomicUnsupported) {
		t.Errorf("atomic err = %v, want ErrBulkAtomicUnsupported", err)
	}
}

// TestBestEffortBulkMutate_Sweep: a sweep resolves matches via ListEnriched
// and applies the op template to each.
func TestBestEffortBulkMutate_Sweep(t *testing.T) {
	f := &fakeMutatorLister{
		enrichedOK: true,
		enrichedNodes: []Node{
			{URI: mustURL(t, "fake://h/a")},
			{URI: mustURL(t, "fake://h/b")},
		},
	}

	result, err := BestEffortBulkMutate(context.Background(), f, f, BulkRequest{
		Atomicity: BulkBestEffort,
		Sweep: &BulkSweep{
			Root: mustURL(t, "fake://h"),
			Op:   BulkOp{Kind: BulkDelete},
		},
	})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got := uriStringsOf(t, result.AppliedNodes); !slices.Equal(
		got, []string{"fake://h/a", "fake://h/b"},
	) {
		t.Errorf("sweep applied = %v, want [a b]", got)
	}
}

// TestBestEffortBulkMutate_SweepDeclineRefused: an ListEnriched decline
// (ok=false) REFUSES with a bad request rather than sweeping an empty (or
// unfiltered) set — the write-safety contract.
func TestBestEffortBulkMutate_SweepDeclineRefused(t *testing.T) {
	f := &fakeMutatorLister{enrichedOK: false}

	_, err := BestEffortBulkMutate(context.Background(), f, f, BulkRequest{
		Atomicity: BulkBestEffort,
		Sweep: &BulkSweep{
			Root: mustURL(t, "fake://h"),
			Op:   BulkOp{Kind: BulkDelete},
		},
	})
	if !errors.Is400BadRequest(err) {
		t.Errorf("sweep at a declined level err = %v, want a 400 bad request", err)
	}
	if len(f.deleted) != 0 {
		t.Error("a declined sweep applied a mutation")
	}
}

// TestRecognizedPatchFields: returns the sorted subset present, always
// non-nil (the #182 present-empty contract).
func TestRecognizedPatchFields(t *testing.T) {
	supported := []string{"labels", "priority", "status", "summary"}

	got := RecognizedPatchFields(
		rawFields(`{"summary":1,"status":1,"bogus":1}`), supported,
	)
	if !slices.Equal(got, []string{"status", "summary"}) {
		t.Errorf("recognized = %v, want [status summary]", got)
	}

	empty := RecognizedPatchFields(rawFields(`{"bogus":1}`), supported)
	if empty == nil || len(empty) != 0 {
		t.Errorf("recognized = %v, want a non-nil empty slice", empty)
	}
}
