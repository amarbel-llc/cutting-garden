package jira

import (
	"context"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// TestBulkMutate_OpsChangeset: an explicit changeset of two deletes applies
// both via jira's NodeMutator verbs.
func TestBulkMutate_OpsChangeset(t *testing.T) {
	f, baseURI := startFakeFaceted(t)

	result, err := (Plugin{}).BulkMutate(context.Background(),
		cutting_garden_plugins.BulkRequest{
			Atomicity: cutting_garden_plugins.BulkBestEffort,
			Ops: []cutting_garden_plugins.BulkOp{
				{Kind: cutting_garden_plugins.BulkDelete, URI: mustParseURL(t, baseURI+"/PROJ/PROJ-1")},
				{Kind: cutting_garden_plugins.BulkDelete, URI: mustParseURL(t, baseURI+"/PROJ/PROJ-2")},
			},
		})
	if err != nil {
		t.Fatalf("BulkMutate: %v", err)
	}
	if len(result.AppliedNodes) != 2 || len(result.Failed) != 0 {
		t.Fatalf("applied=%v failed=%v, want 2 applied", result.AppliedNodes, result.Failed)
	}
	if _, ok := f.issues["PROJ-1"]; ok {
		t.Error("PROJ-1 still present")
	}
	if _, ok := f.issues["PROJ-2"]; ok {
		t.Error("PROJ-2 still present")
	}
}

// TestBulkMutate_Sweep: a sweep resolves its match set via jira's ListEnriched
// (one JQL search) and applies the op template to each match — here, delete
// every Done issue in the project.
func TestBulkMutate_Sweep(t *testing.T) {
	f, baseURI := startFakeFaceted(t)

	result, err := (Plugin{}).BulkMutate(context.Background(),
		cutting_garden_plugins.BulkRequest{
			Atomicity: cutting_garden_plugins.BulkBestEffort,
			Sweep: &cutting_garden_plugins.BulkSweep{
				Root:   mustParseURL(t, baseURI+"/PROJ"),
				Filter: cutting_garden_plugins.FacetFilter{{Dimension: "status", Value: "Done"}},
				Op:     cutting_garden_plugins.BulkOp{Kind: cutting_garden_plugins.BulkDelete},
			},
		})
	if err != nil {
		t.Fatalf("BulkMutate(sweep): %v", err)
	}
	// PROJ-2 is the only Done issue.
	if len(result.AppliedNodes) != 1 || result.AppliedNodes[0].String() == "" {
		t.Fatalf("applied = %v, want just the one Done issue", result.AppliedNodes)
	}
	if _, ok := f.issues["PROJ-2"]; ok {
		t.Error("PROJ-2 (Done) not deleted by the sweep")
	}
	if _, ok := f.issues["PROJ-1"]; !ok {
		t.Error("PROJ-1 (In Progress) deleted by a status=Done sweep")
	}
}

// TestBulkMutate_AtomicRejected: Jira cannot transact, so an atomic request is
// rejected with the sentinel — never downgraded.
func TestBulkMutate_AtomicRejected(t *testing.T) {
	_, baseURI := startFakeFaceted(t)

	_, err := (Plugin{}).BulkMutate(context.Background(),
		cutting_garden_plugins.BulkRequest{
			Atomicity: cutting_garden_plugins.BulkAtomic,
			Ops: []cutting_garden_plugins.BulkOp{
				{Kind: cutting_garden_plugins.BulkDelete, URI: mustParseURL(t, baseURI+"/PROJ/PROJ-1")},
			},
		})
	if !errors.Is(err, cutting_garden_plugins.ErrBulkAtomicUnsupported) {
		t.Errorf("atomic err = %v, want ErrBulkAtomicUnsupported", err)
	}
}

// TestBulkMutate_SweepAtRootRefused: a sweep whose Root is the host (its
// children are project containers, not issues) is refused — ListEnriched
// declines there, and a mutation must not widen scope across projects.
func TestBulkMutate_SweepAtRootRefused(t *testing.T) {
	_, baseURI := startFakeFaceted(t)

	_, err := (Plugin{}).BulkMutate(context.Background(),
		cutting_garden_plugins.BulkRequest{
			Atomicity: cutting_garden_plugins.BulkBestEffort,
			Sweep: &cutting_garden_plugins.BulkSweep{
				Root: mustParseURL(t, baseURI),
				Op:   cutting_garden_plugins.BulkOp{Kind: cutting_garden_plugins.BulkDelete},
			},
		})
	if !errors.Is400BadRequest(err) {
		t.Errorf("sweep at the host root err = %v, want a 400 bad request", err)
	}
}
