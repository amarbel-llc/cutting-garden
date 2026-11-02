package cutting_garden_plugins

import (
	"context"
	"io"
	"net/url"
	"slices"
	"strings"
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// detailMutator reports per-op detail: the fields a patch applied, a new URI,
// a deleted node's name, and a delete of a missing node as not found.
type detailMutator struct {
	fakeMutatorLister
	newURI  map[string]*url.URL // patch target → its URI afterwards
	names   map[string]string   // delete target → name
	missing map[string]bool     // delete target → not found
}

func (d *detailMutator) PatchNodeDetail(
	ctx context.Context, uri *url.URL, body io.Reader,
) (PatchDetail, error) {
	applied, err := d.PatchNode(ctx, uri, body)
	return PatchDetail{Applied: applied, NewURI: d.newURI[uri.String()]}, err
}

func (d *detailMutator) DeleteNodeDetail(
	_ context.Context, uri *url.URL, _ io.Reader,
) (DeleteDetail, error) {
	if d.failOn[uri.String()] {
		return DeleteDetail{}, errors.BadRequestf("delete failed")
	}
	if d.missing[uri.String()] {
		return DeleteDetail{NotFound: true}, errors.BadRequestf("no such node")
	}
	d.deleted = append(d.deleted, uri.String())
	return DeleteDetail{Name: d.names[uri.String()]}, nil
}

func outcomeStatuses(r BulkResult) []BulkOutcomeStatus {
	out := make([]BulkOutcomeStatus, 0, len(r.Outcomes))
	for _, o := range r.Outcomes {
		out = append(out, o.Status)
	}
	return out
}

// A plugin with neither detail interface still gets one outcome per op, in
// request order, carrying only a status.
func TestApplyBulkOp_OutcomesFallBackToStatusOnly(t *testing.T) {
	f := &fakeMutatorLister{
		patchApplied: []string{"summary"},
		failOn:       map[string]bool{"fake://h/bad": true},
	}
	a, b, bad := mustURL(t, "fake://h/a"), mustURL(t, "fake://h/b"), mustURL(t, "fake://h/bad")
	res := BulkBestEffortOps(context.Background(), f, []BulkOp{
		{Kind: BulkPatch, URI: a, Body: []byte(`{}`)},
		{Kind: BulkDelete, URI: b},
		{Kind: BulkPut, URI: bad},
	})
	want := []BulkOutcomeStatus{OutcomeApplied, OutcomeDeleted, OutcomeFailed}
	if got := outcomeStatuses(res); !slices.Equal(got, want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
	// PatchNode's applied still reaches Fields; no URI or name can come from a
	// plugin with neither detail interface.
	if !slices.Equal(res.Outcomes[0].Fields, []string{"summary"}) {
		t.Errorf("patch fields = %v", res.Outcomes[0].Fields)
	}
	for _, o := range res.Outcomes {
		if o.NewURI != nil || o.Name != "" {
			t.Errorf("a plugin with no detail interface gave detail: %+v", o)
		}
	}
	if o := res.Outcomes[2]; o.Err != "put failed" || o.URI.String() != "fake://h/bad" {
		t.Errorf("failed outcome = %+v", o)
	}
	checkInvariant(t, res, 3)
}

// A mixed batch with detail: a patch with fields and a new URI, a delete with
// a name, a delete of a missing node, a failing delete — in request order.
func TestApplyBulkOp_MixedBatchOutcomes(t *testing.T) {
	d := &detailMutator{
		fakeMutatorLister: fakeMutatorLister{
			patchApplied: []string{"title", "state"},
			failOn:       map[string]bool{"fake://h/boom": true},
		},
		newURI:  map[string]*url.URL{"fake://h/p": mustURL(t, "fake://h/p?id=1")},
		names:   map[string]string{"fake://h/d": "Dee"},
		missing: map[string]bool{"fake://h/gone": true},
	}
	res := BulkBestEffortOps(context.Background(), d, []BulkOp{
		{Kind: BulkPatch, URI: mustURL(t, "fake://h/p"), Body: []byte(`{}`)},
		{Kind: BulkDelete, URI: mustURL(t, "fake://h/d")},
		{Kind: BulkDelete, URI: mustURL(t, "fake://h/gone")},
		{Kind: BulkDelete, URI: mustURL(t, "fake://h/boom")},
	})
	want := []BulkOutcomeStatus{OutcomeApplied, OutcomeDeleted, OutcomeNotFound, OutcomeFailed}
	if got := outcomeStatuses(res); !slices.Equal(got, want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
	if o := res.Outcomes[0]; !slices.Equal(o.Fields, []string{"title", "state"}) ||
		o.NewURI == nil || o.NewURI.String() != "fake://h/p?id=1" {
		t.Errorf("patch outcome = %+v", o)
	}
	if o := res.Outcomes[1]; o.Name != "Dee" {
		t.Errorf("delete outcome = %+v", o)
	}
	if o := res.Outcomes[2]; o.Err != "no such node" {
		t.Errorf("not-found outcome = %+v", o)
	}
	// A deleted node is also in AppliedNodes; a not-found delete is also in
	// Failed — the old lists keep their meaning.
	if len(res.AppliedNodes) != 2 || len(res.Failed) != 2 {
		t.Errorf("applied=%v failed=%+v", res.AppliedNodes, res.Failed)
	}
	checkInvariant(t, res, 4)
}

// NewURI is reported only when it differs from the URI the op targeted.
func TestApplyBulkOp_NewURIOnlyWhenItDiffers(t *testing.T) {
	d := &detailMutator{
		fakeMutatorLister: fakeMutatorLister{patchApplied: []string{"state"}},
		newURI:            map[string]*url.URL{"fake://h/p?id=1": mustURL(t, "fake://h/p?id=1")},
	}
	res := BulkBestEffortOps(context.Background(), d, []BulkOp{
		{Kind: BulkPatch, URI: mustURL(t, "fake://h/p?id=1"), Body: []byte(`{}`)},
	})
	if res.Outcomes[0].NewURI != nil {
		t.Errorf("NewURI = %v for an unchanged URI", res.Outcomes[0].NewURI)
	}
}

// A patch that applied nothing is patched_nothing, with its (empty) fields.
func TestApplyBulkOp_PatchedNothingOutcome(t *testing.T) {
	d := &detailMutator{fakeMutatorLister: fakeMutatorLister{patchApplied: []string{}}}
	res := BulkBestEffortOps(context.Background(), d, []BulkOp{
		{Kind: BulkPatch, URI: mustURL(t, "fake://h/p"), Body: []byte(`{}`)},
	})
	if got := outcomeStatuses(res); !slices.Equal(got, []BulkOutcomeStatus{OutcomePatchedNothing}) {
		t.Errorf("statuses = %v", got)
	}
	checkInvariant(t, res, 1)
}

// A sweep yields one outcome per matched node, with the delete's name.
func TestBestEffortBulkMutate_SweepOutcomes(t *testing.T) {
	d := &detailMutator{
		fakeMutatorLister: fakeMutatorLister{
			enrichedOK:    true,
			enrichedNodes: []Node{{URI: mustURL(t, "fake://h/a")}, {URI: mustURL(t, "fake://h/b")}},
		},
		names: map[string]string{"fake://h/a": "A", "fake://h/b": "B"},
	}
	res, err := BestEffortBulkMutate(context.Background(), d, d, BulkRequest{
		Atomicity: BulkBestEffort,
		Sweep:     &BulkSweep{Root: mustURL(t, "fake://h"), Op: BulkOp{Kind: BulkDelete}},
	})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(res.Outcomes) != 2 || res.Outcomes[0].Name != "A" || res.Outcomes[1].Name != "B" ||
		res.Outcomes[0].Status != OutcomeDeleted {
		t.Errorf("outcomes = %+v", res.Outcomes)
	}
	checkInvariant(t, res, 2)
}

// A temp-id create keeps its Created mapping and gets an applied outcome; an op
// that depends on a create that failed is never attempted and says so under
// the failed status.
func TestApplyBulkOp_TempIDOutcomes(t *testing.T) {
	c := &creatorMutator{created: mustURL(t, "fake://h/new")}
	res := BulkBestEffortOps(context.Background(), c, []BulkOp{
		{Kind: BulkCreate, TempID: "+a", In: mustURL(t, "fake://h/"), Body: []byte(`{}`)},
		{Kind: BulkPatch, Ref: "+a", Body: []byte(`{}`)},
	})
	if len(res.Created) != 1 || res.Created[0].URI.String() != "fake://h/new" {
		t.Fatalf("created = %+v", res.Created)
	}
	if got := outcomeStatuses(res); !slices.Equal(got, []BulkOutcomeStatus{OutcomeApplied, OutcomeApplied}) {
		t.Errorf("statuses = %v", got)
	}
	if o := res.Outcomes[0]; o.Ref != "+a" || o.URI.String() != "fake://h/new" {
		t.Errorf("create outcome = %+v", o)
	}

	c.createErr = errors.BadRequestf("create refused")
	c.created = nil
	res = BulkBestEffortOps(context.Background(), c, []BulkOp{
		{Kind: BulkCreate, TempID: "+a", In: mustURL(t, "fake://h/"), Body: []byte(`{}`)},
		{Kind: BulkPatch, Ref: "+a", Body: []byte(`{}`)},
	})
	if got := outcomeStatuses(res); !slices.Equal(got, []BulkOutcomeStatus{OutcomeFailed, OutcomeFailed}) {
		t.Fatalf("statuses = %v", got)
	}
	if o := res.Outcomes[1]; !strings.Contains(o.Err, "depends on the create of temp id +a") || o.Ref != "+a" {
		t.Errorf("never-attempted outcome = %+v", o)
	}
	checkInvariant(t, res, 2)
}

// The RFC 0017 invariant holds with Outcomes present: applied + patched-nothing
// + failed is the targeted set, and so is Outcomes.
func checkInvariant(t *testing.T, r BulkResult, targeted int) {
	t.Helper()
	if n := len(r.AppliedNodes) + len(r.PatchedNothing) + len(r.Failed); n != targeted {
		t.Errorf("applied+patched_nothing+failed = %d, want the %d targeted", n, targeted)
	}
	if len(r.Outcomes) != targeted {
		t.Errorf("outcomes = %d, want the %d targeted", len(r.Outcomes), targeted)
	}
}

// creatorMutator is a NodeMutator + ContainerCreator.
type creatorMutator struct {
	fakeMutatorLister
	created   *url.URL
	createErr error
}

func (c *creatorMutator) CreateChild(
	context.Context, *url.URL, io.Reader, string,
) (*url.URL, error) {
	return c.created, c.createErr
}
