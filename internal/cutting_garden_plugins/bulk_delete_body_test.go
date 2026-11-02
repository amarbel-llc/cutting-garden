package cutting_garden_plugins

import (
	"context"
	"io"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// deleterWith is a mutator whose delete takes options.
type deleterWith struct {
	fakeMutatorLister
	bodies map[string]string
}

func (d *deleterWith) DeleteNodeWith(_ context.Context, uri *url.URL, body io.Reader) error {
	b, _ := io.ReadAll(body)
	if d.bodies == nil {
		d.bodies = map[string]string{}
	}
	d.bodies[uri.String()] = string(b)
	return nil
}

func TestApplyBulkOp_DeleteBody(t *testing.T) {
	uri := mustURL(t, "fake://h/a")
	body := []byte(`{"subtree": true}`)

	t.Run("no body is DeleteNode, as before", func(t *testing.T) {
		d := &deleterWith{}
		var r BulkResult
		ApplyBulkOp(context.Background(), d, BulkOp{Kind: BulkDelete, URI: uri}, &r)
		if len(r.AppliedNodes) != 1 || len(d.deleted) != 1 || len(d.bodies) != 0 {
			t.Errorf("applied=%v deleted=%v bodies=%v", r.AppliedNodes, d.deleted, d.bodies)
		}
	})
	t.Run("a body reaches DeleteNodeWith", func(t *testing.T) {
		d := &deleterWith{}
		var r BulkResult
		ApplyBulkOp(context.Background(), d, BulkOp{Kind: BulkDelete, URI: uri, Body: body}, &r)
		if len(r.AppliedNodes) != 1 || d.bodies["fake://h/a"] != string(body) || len(d.deleted) != 0 {
			t.Errorf("applied=%v deleted=%v bodies=%v", r.AppliedNodes, d.deleted, d.bodies)
		}
	})
	t.Run("a source without it refuses the body and deletes nothing", func(t *testing.T) {
		f := &fakeMutatorLister{}
		var r BulkResult
		ApplyBulkOp(context.Background(), f, BulkOp{Kind: BulkDelete, URI: uri, Body: body}, &r)
		if len(r.AppliedNodes) != 0 || len(f.deleted) != 0 || len(r.Failed) != 1 ||
			!strings.Contains(r.Failed[0].Err, "this source's delete takes no body") {
			t.Errorf("applied=%v deleted=%v failed=%+v", r.AppliedNodes, f.deleted, r.Failed)
		}
	})
}

// A sweep's op template carries the body to every matched node.
func TestBestEffortBulkMutate_SweepDeleteBody(t *testing.T) {
	nodes := []Node{{URI: mustURL(t, "fake://h/a")}, {URI: mustURL(t, "fake://h/b")}}
	body := []byte(`{"subtree": true}`)
	req := BulkRequest{
		Atomicity: BulkBestEffort,
		Sweep:     &BulkSweep{Root: mustURL(t, "fake://h"), Op: BulkOp{Kind: BulkDelete, Body: body}},
	}

	d := &deleterWith{fakeMutatorLister: fakeMutatorLister{enrichedOK: true, enrichedNodes: nodes}}
	result, err := BestEffortBulkMutate(context.Background(), d, d, req)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	got := []string{d.bodies["fake://h/a"], d.bodies["fake://h/b"]}
	if len(result.AppliedNodes) != 2 || !slices.Equal(got, []string{string(body), string(body)}) {
		t.Errorf("applied=%v bodies=%v", result.AppliedNodes, d.bodies)
	}

	f := &fakeMutatorLister{enrichedOK: true, enrichedNodes: nodes}
	result, err = BestEffortBulkMutate(context.Background(), f, f, req)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(result.AppliedNodes) != 0 || len(f.deleted) != 0 || len(result.Failed) != 2 {
		t.Errorf("applied=%v deleted=%v failed=%+v; want both refused", result.AppliedNodes, f.deleted, result.Failed)
	}
}
