package ynab

import (
	"context"
	"net/url"
	"path"
	"slices"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// patchOp is a bulk patch of node uri with a JSON body.
func patchOp(t *testing.T, uri, body string) cutting_garden_plugins.BulkOp {
	t.Helper()
	return cutting_garden_plugins.BulkOp{
		Kind: cutting_garden_plugins.BulkPatch,
		URI:  mustParseURL(t, uri),
		Body: []byte(body),
	}
}

func bulk(
	t *testing.T, ops ...cutting_garden_plugins.BulkOp,
) cutting_garden_plugins.BulkResult {
	t.Helper()
	result, err := (Plugin{}).BulkMutate(context.Background(), cutting_garden_plugins.BulkRequest{
		Ops: ops, Atomicity: cutting_garden_plugins.BulkBestEffort,
	})
	if err != nil {
		t.Fatalf("BulkMutate: %v", err)
	}
	return result
}

// lastSegments names each URI by its last path segment, sorted.
func lastSegments(uris []*url.URL) []string {
	out := make([]string, len(uris))
	for i, u := range uris {
		out[i] = path.Base(u.Path)
	}
	slices.Sort(out)
	return out
}

func failedSegments(failures []cutting_garden_plugins.BulkFailure) []string {
	uris := make([]*url.URL, 0, len(failures))
	for _, f := range failures {
		uris = append(uris, f.URI)
	}
	return lastSegments(uris)
}

// The whole point (cutting-garden#334): however many transactions a changeset
// patches, it is ONE write, and the requests around it do not grow with N.
// After a listing (so the targets' dates are known), three patches touching a
// category, a payee and a memo cost: one fresh read of the targets, the
// category tree, the payee list and the single bulk write.
func TestBulkMutate_ManyPatchesAreOneWrite(t *testing.T) {
	srv := newFixture(t)
	list(t, txnPrefix)
	before := srv.Requests()

	result := bulk(t,
		patchOp(t, txnPrefix+"t1", `{"category":"Groceries"}`),
		patchOp(t, txnPrefix+"t2", `{"payee":"Landlord"}`),
		patchOp(t, txnPrefix+"t4", `{"memo":"bulk"}`),
	)

	if got := lastSegments(result.AppliedNodes); !slices.Equal(got, []string{"t1", "t2", "t4"}) {
		t.Errorf("applied = %v (failed %v), want t1 t2 t4", got, result.Failed)
	}
	if srv.Writes() != 1 {
		t.Errorf("writes = %d, want exactly 1", srv.Writes())
	}
	if n := srv.Requests() - before; n != 4 {
		t.Errorf("a 3-patch changeset cost %d requests, want 4 (fresh targets + categories + payees + write)", n)
	}
	if saved, _ := srv.TransactionByID("b1", "t1"); *saved.CategoryID != "c1" {
		t.Errorf("t1 category = %v, want c1", *saved.CategoryID)
	}
	if saved, _ := srv.TransactionByID("b1", "t2"); *saved.PayeeID != "p3" {
		t.Errorf("t2 payee = %v, want p3", *saved.PayeeID)
	}
	if saved, _ := srv.TransactionByID("b1", "t4"); saved.Memo == nil || *saved.Memo != "bulk" {
		t.Errorf("t4 memo = %v, want bulk", saved.Memo)
	}
}

// One refused op does not stop the others: it is a BulkFailure, the rest apply,
// and the refused transaction is untouched.
func TestBulkMutate_ARefusedOpDoesNotStopTheRest(t *testing.T) {
	srv := newFixture(t)
	result := bulk(t,
		patchOp(t, txnPrefix+"t1", `{"memo":"ok"}`),
		patchOp(t, txnPrefix+"t3", `{"cleared":"uncleared","memo":"nope"}`), // t3 is reconciled
		patchOp(t, txnPrefix+"t2", `{"category":"Nonesuch"}`),               // unknown category
		patchOp(t, txnPrefix+"t4", `{"memo":"ok too"}`),
	)
	if got := lastSegments(result.AppliedNodes); !slices.Equal(got, []string{"t1", "t4"}) {
		t.Errorf("applied = %v, want t1 t4", got)
	}
	if got := failedSegments(result.Failed); !slices.Equal(got, []string{"t2", "t3"}) {
		t.Errorf("failed = %v, want t2 t3", got)
	}
	if srv.Writes() != 1 {
		t.Errorf("writes = %d, want 1", srv.Writes())
	}
	if saved, _ := srv.TransactionByID("b1", "t3"); saved.Memo != nil || saved.Cleared != "reconciled" {
		t.Errorf("t3 changed despite its refusal: memo %v cleared %q", saved.Memo, saved.Cleared)
	}
}

// The guards read the transactions FRESH: a transaction reconciled behind the
// plugin's back after a listing cached it is still refused.
func TestBulkMutate_GuardsSeeFreshDataNotTheCachedListing(t *testing.T) {
	srv := newFixture(t)
	list(t, txnPrefix) // t1, t2, t4 are now cached as not reconciled
	srv.SetCleared("b1", "t2", "reconciled")

	result := bulk(t,
		patchOp(t, txnPrefix+"t1", `{"memo":"fine"}`),
		patchOp(t, txnPrefix+"t2", `{"cleared":"cleared"}`),
	)
	if got := lastSegments(result.AppliedNodes); !slices.Equal(got, []string{"t1"}) {
		t.Errorf("applied = %v, want t1", got)
	}
	if got := failedSegments(result.Failed); !slices.Equal(got, []string{"t2"}) {
		t.Errorf("failed = %v, want t2 (it was reconciled after the listing)", got)
	}
	if saved, _ := srv.TransactionByID("b1", "t2"); saved.Cleared != "reconciled" {
		t.Errorf("t2 was un-reconciled by a bulk patch: %q", saved.Cleared)
	}
}

// Patches that change nothing are PatchedNothing and send no write.
func TestBulkMutate_NoOpsAreReportedAndWriteNothing(t *testing.T) {
	srv := newFixture(t)
	result := bulk(t,
		patchOp(t, txnPrefix+"t1", `{"category":"Dining Out"}`), // already so
		patchOp(t, txnPrefix+"t2", `{"flag":"red"}`),            // nothing recognized
	)
	if got := lastSegments(result.PatchedNothing); !slices.Equal(got, []string{"t1", "t2"}) {
		t.Errorf("patched nothing = %v, want t1 t2", got)
	}
	if len(result.AppliedNodes) != 0 || len(result.Failed) != 0 {
		t.Errorf("applied %v failed %v, want none", result.AppliedNodes, result.Failed)
	}
	if srv.Writes() != 0 {
		t.Errorf("no-ops sent %d write(s)", srv.Writes())
	}
}

// Two patches of one transaction in a request are refused, not merged or
// ordered by guess.
func TestBulkMutate_TheSameTransactionTwiceIsRefused(t *testing.T) {
	srv := newFixture(t)
	result := bulk(t,
		patchOp(t, txnPrefix+"t1", `{"memo":"first"}`),
		patchOp(t, txnPrefix+"t1", `{"memo":"second"}`),
	)
	if len(result.AppliedNodes) != 1 || len(result.Failed) != 1 {
		t.Fatalf("applied %v failed %v, want one of each", result.AppliedNodes, result.Failed)
	}
	if saved, _ := srv.TransactionByID("b1", "t1"); *saved.Memo != "first" {
		t.Errorf("memo = %q, want the first patch", *saved.Memo)
	}
}

// A write the server answered without applying is a failure for that op, not an
// applied node.
func TestBulkMutate_AnIgnoredWriteIsAFailure(t *testing.T) {
	srv := newFixture(t)
	srv.IgnoreWrites(true)
	result := bulk(t, patchOp(t, txnPrefix+"t1", `{"category":"Rent"}`))
	if len(result.AppliedNodes) != 0 || len(result.Failed) != 1 {
		t.Errorf("applied %v failed %v, want one failure", result.AppliedNodes, result.Failed)
	}
}

// A transaction that does not exist fails on its own.
func TestBulkMutate_AnUnknownTransactionFailsAlone(t *testing.T) {
	newFixture(t)
	result := bulk(t,
		patchOp(t, txnPrefix+"nope", `{"memo":"x"}`),
		patchOp(t, txnPrefix+"t1", `{"memo":"y"}`),
	)
	if got := lastSegments(result.AppliedNodes); !slices.Equal(got, []string{"t1"}) {
		t.Errorf("applied = %v, want t1", got)
	}
	if got := failedSegments(result.Failed); !slices.Equal(got, []string{"nope"}) {
		t.Errorf("failed = %v, want nope", got)
	}
}

// Payee repoints in one changeset read the payee list once.
func TestBulkMutate_PayeeListIsReadOnceForTheBatch(t *testing.T) {
	srv := newFixture(t)
	list(t, txnPrefix)
	before := srv.Requests()
	result := bulk(t,
		patchOp(t, txnPrefix+"t1", `{"payee":"Grocer"}`),
		patchOp(t, txnPrefix+"t2", `{"payee":"Landlord"}`),
		patchOp(t, txnPrefix+"t4", `{"payee":"Corner Cafe"}`),
	)
	if len(result.AppliedNodes) != 3 {
		t.Fatalf("applied %v failed %v, want 3 applied", result.AppliedNodes, result.Failed)
	}
	// fresh targets + payees + write; no category lookup, no per-op payee read.
	if n := srv.Requests() - before; n != 3 {
		t.Errorf("3 payee repoints cost %d requests, want 3", n)
	}
}

// A payee rename is not batchable (one request each in the API): it rides the
// single-node path inside a changeset and still applies.
func TestBulkMutate_APayeeRenameTakesTheSinglePath(t *testing.T) {
	srv := newFixture(t)
	result := bulk(t,
		patchOp(t, payeePrefix+"p1", `{"name":"Corner Café"}`),
		patchOp(t, txnPrefix+"t1", `{"memo":"m"}`),
	)
	if got := lastSegments(result.AppliedNodes); !slices.Equal(got, []string{"p1", "t1"}) {
		t.Errorf("applied = %v (failed %v), want p1 t1", got, result.Failed)
	}
	if saved, _ := srv.PayeeByID("b1", "p1"); saved.Name != "Corner Café" {
		t.Errorf("payee name = %q", saved.Name)
	}
}

// The verbs ynab refuses stay refused inside a changeset, per op.
func TestBulkMutate_RefusedVerbsAreFailuresPerOp(t *testing.T) {
	srv := newFixture(t)
	result := bulk(t,
		cutting_garden_plugins.BulkOp{Kind: cutting_garden_plugins.BulkDelete, URI: mustParseURL(t, txnPrefix+"t1")},
		cutting_garden_plugins.BulkOp{Kind: cutting_garden_plugins.BulkPut, URI: mustParseURL(t, txnPrefix+"t2"), Body: []byte(`{}`)},
		patchOp(t, txnPrefix+"t4", `{"memo":"still goes"}`),
	)
	if got := failedSegments(result.Failed); !slices.Equal(got, []string{"t1", "t2"}) {
		t.Errorf("failed = %v, want t1 t2", got)
	}
	if got := lastSegments(result.AppliedNodes); !slices.Equal(got, []string{"t4"}) {
		t.Errorf("applied = %v, want t4", got)
	}
	if srv.Writes() != 1 {
		t.Errorf("writes = %d, want 1", srv.Writes())
	}
}

func TestBulkMutate_AtomicIsRejectedNeverDowngraded(t *testing.T) {
	srv := newFixture(t)
	_, err := (Plugin{}).BulkMutate(context.Background(), cutting_garden_plugins.BulkRequest{
		Ops:       []cutting_garden_plugins.BulkOp{patchOp(t, txnPrefix+"t1", `{"memo":"x"}`)},
		Atomicity: cutting_garden_plugins.BulkAtomic,
	})
	if !errors.Is(err, cutting_garden_plugins.ErrBulkAtomicUnsupported) {
		t.Errorf("atomic err = %v, want ErrBulkAtomicUnsupported", err)
	}
	if srv.Requests() != 0 || srv.Writes() != 0 {
		t.Errorf("a rejected atomic request cost %d requests, %d writes", srv.Requests(), srv.Writes())
	}
}

func TestBulkMutate_BadRequestShapesAreRejected(t *testing.T) {
	newFixture(t)
	for name, req := range map[string]cutting_garden_plugins.BulkRequest{
		"neither ops nor sweep": {Atomicity: cutting_garden_plugins.BulkBestEffort},
		"no atomicity":          {Ops: []cutting_garden_plugins.BulkOp{patchOp(t, txnPrefix+"t1", `{}`)}},
	} {
		if _, err := (Plugin{}).BulkMutate(context.Background(), req); err == nil || !errors.Is400BadRequest(err) {
			t.Errorf("%s: want a 400, got %v", name, err)
		}
	}
}

// A sweep resolves its matches with the plugin's own enriched listing and
// batches the template over them: one write for every uncleared transaction.
func TestBulkMutate_SweepBatchesTheMatches(t *testing.T) {
	srv := newFixture(t)
	result, err := (Plugin{}).BulkMutate(context.Background(), cutting_garden_plugins.BulkRequest{
		Sweep: &cutting_garden_plugins.BulkSweep{
			Root:   mustParseURL(t, txnPrefix),
			Filter: cutting_garden_plugins.FacetFilter{{Dimension: facetCleared, Value: clearedUncleared}},
			Op:     cutting_garden_plugins.BulkOp{Kind: cutting_garden_plugins.BulkPatch, Body: []byte(`{"memo":"swept"}`)},
		},
		Atomicity: cutting_garden_plugins.BulkBestEffort,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := lastSegments(result.AppliedNodes); !slices.Equal(got, []string{"t2"}) {
		t.Errorf("applied = %v, want t2 (the only uncleared transaction in the window)", got)
	}
	if srv.Writes() != 1 {
		t.Errorf("writes = %d, want 1", srv.Writes())
	}
}

// A sweep over a node whose children are not transactions is refused, never
// widened to "everything".
func TestBulkMutate_SweepOverANonTransactionNodeIsRefused(t *testing.T) {
	srv := newFixture(t)
	_, err := (Plugin{}).BulkMutate(context.Background(), cutting_garden_plugins.BulkRequest{
		Sweep: &cutting_garden_plugins.BulkSweep{
			Root: mustParseURL(t, "ynab://personal/Consolidated/"),
			Op:   cutting_garden_plugins.BulkOp{Kind: cutting_garden_plugins.BulkPatch, Body: []byte(`{"memo":"x"}`)},
		},
		Atomicity: cutting_garden_plugins.BulkBestEffort,
	})
	if err == nil || !errors.Is400BadRequest(err) {
		t.Errorf("want a 400, got %v", err)
	}
	if srv.Writes() != 0 {
		t.Errorf("a refused sweep wrote %d time(s)", srv.Writes())
	}
}
