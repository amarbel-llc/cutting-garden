package caldav

import (
	"context"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// TestBulkMutate_BestEffortChangeset: an explicit changeset of two creates
// applies both, and each stored object is present.
func TestBulkMutate_BestEffortChangeset(t *testing.T) {
	f, home := startFakeEmpty(t)
	a := objectArg(home, "/dav/cal/a.ics")
	b := objectArg(home, "/dav/cal/b.ics")

	result, err := Plugin{}.BulkMutate(context.Background(),
		cutting_garden_plugins.BulkRequest{
			Atomicity: cutting_garden_plugins.BulkBestEffort,
			Ops: []cutting_garden_plugins.BulkOp{
				{
					Kind: cutting_garden_plugins.BulkCreate, URI: mustParseURL(t, a),
					Type: typeObject, Body: []byte(vevent("u-a", "A")),
				},
				{
					Kind: cutting_garden_plugins.BulkCreate, URI: mustParseURL(t, b),
					Type: typeObject, Body: []byte(vevent("u-b", "B")),
				},
			},
		})
	if err != nil {
		t.Fatalf("BulkMutate: %v", err)
	}
	if len(result.AppliedNodes) != 2 || len(result.Failed) != 0 {
		t.Fatalf("applied = %v, failed = %v; want 2 applied, 0 failed",
			result.AppliedNodes, result.Failed)
	}
	if _, ok := f.resources["/dav/cal/a.ics"]; !ok {
		t.Error("a.ics not stored")
	}
	if _, ok := f.resources["/dav/cal/b.ics"]; !ok {
		t.Error("b.ics not stored")
	}
}

// TestBulkMutate_PartialFailureIsBestEffort: a valid create and a delete of
// a missing object partition into applied and failed — a per-node failure
// is a BulkFailure, never a returned call error (best-effort).
func TestBulkMutate_PartialFailureIsBestEffort(t *testing.T) {
	f, home := startFakeEmpty(t)
	good := objectArg(home, "/dav/cal/good.ics")
	missing := objectArg(home, "/dav/cal/missing.ics")

	result, err := Plugin{}.BulkMutate(context.Background(),
		cutting_garden_plugins.BulkRequest{
			Atomicity: cutting_garden_plugins.BulkBestEffort,
			Ops: []cutting_garden_plugins.BulkOp{
				{
					Kind: cutting_garden_plugins.BulkCreate, URI: mustParseURL(t, good),
					Type: typeObject, Body: []byte(vevent("g", "Good")),
				},
				{Kind: cutting_garden_plugins.BulkDelete, URI: mustParseURL(t, missing)},
			},
		})
	if err != nil {
		t.Fatalf("best-effort must not return a call error on a per-node"+
			" failure: %v", err)
	}
	if len(result.AppliedNodes) != 1 ||
		result.AppliedNodes[0].String() != mustParseURL(t, good).String() {
		t.Errorf("applied = %v, want [good]", result.AppliedNodes)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("failed = %v, want 1 (the missing delete)", result.Failed)
	}
	if _, ok := f.resources["/dav/cal/good.ics"]; !ok {
		t.Error("good.ics not stored despite being reported applied")
	}
}

// TestBulkMutate_PatchNothingReportedDistinctly: an unrecognized-only patch
// is ACCEPTED but applies no field, so its target lands in PatchedNothing —
// neither applied nor failed (#182 at bulk scale).
func TestBulkMutate_PatchNothingReportedDistinctly(t *testing.T) {
	_, home := startFake(t) // task1.ics exists
	task := objectArg(home, "/dav/cal/task1.ics")

	result, err := Plugin{}.BulkMutate(context.Background(),
		cutting_garden_plugins.BulkRequest{
			Atomicity: cutting_garden_plugins.BulkBestEffort,
			Ops: []cutting_garden_plugins.BulkOp{
				{
					Kind: cutting_garden_plugins.BulkPatch, URI: mustParseURL(t, task),
					Body: []byte(`{"component":"VTODO","task":{"color":"red"}}`),
				},
			},
		})
	if err != nil {
		t.Fatalf("BulkMutate: %v", err)
	}
	if len(result.AppliedNodes) != 0 || len(result.Failed) != 0 {
		t.Errorf("applied = %v, failed = %v; want both empty",
			result.AppliedNodes, result.Failed)
	}
	if len(result.PatchedNothing) != 1 {
		t.Fatalf("patchedNothing = %v, want the one task", result.PatchedNothing)
	}
}

// TestBulkMutate_SweepAtDeclineLevelRefused: a sweep whose Root is a
// calendar-HOME (ListEnriched declines, ok=false — its children are
// calendars, not the objects a filter selects) must REFUSE with a bad
// request, never silently return an empty success. Treating a decline as
// "nothing matched" would hide a level-mismatched sweep; falling through to
// the unfiltered children would let a mutation widen its scope to every
// calendar.
func TestBulkMutate_SweepAtDeclineLevelRefused(t *testing.T) {
	_, home := startFake(t) // "/dav/" — a calendar-home, selfIsCalendar=false

	_, err := Plugin{}.BulkMutate(context.Background(),
		cutting_garden_plugins.BulkRequest{
			Atomicity: cutting_garden_plugins.BulkBestEffort,
			Sweep: &cutting_garden_plugins.BulkSweep{
				Root: mustParseURL(t, home),
				Op:   cutting_garden_plugins.BulkOp{Kind: cutting_garden_plugins.BulkDelete},
			},
		})
	if !errors.Is400BadRequest(err) {
		t.Errorf("sweep at a calendar-home err = %v, want a 400 bad request"+
			" refusal", err)
	}
}

// TestBulkMutate_AtomicRejected: CalDAV cannot transact atomically, so an
// atomic request is rejected with the sentinel — never downgraded.
func TestBulkMutate_AtomicRejected(t *testing.T) {
	_, home := startFakeEmpty(t)
	a := objectArg(home, "/dav/cal/a.ics")

	_, err := Plugin{}.BulkMutate(context.Background(),
		cutting_garden_plugins.BulkRequest{
			Atomicity: cutting_garden_plugins.BulkAtomic,
			Ops: []cutting_garden_plugins.BulkOp{
				{Kind: cutting_garden_plugins.BulkDelete, URI: mustParseURL(t, a)},
			},
		})
	if !errors.Is(err, cutting_garden_plugins.ErrBulkAtomicUnsupported) {
		t.Errorf("atomic err = %v, want ErrBulkAtomicUnsupported", err)
	}
}
