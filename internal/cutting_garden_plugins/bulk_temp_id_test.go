package cutting_garden_plugins

import (
	"context"
	"io"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// fakeCreatingMutator adds ContainerCreator to the dispatch fake: the source
// names each child "<container>child-<n>".
type fakeCreatingMutator struct {
	fakeMutatorLister
	childrenOf []string
	failCreate bool
}

func (f *fakeCreatingMutator) CreateChild(
	_ context.Context, container *url.URL, _ io.Reader, _ string,
) (*url.URL, error) {
	if f.failCreate {
		return nil, context.DeadlineExceeded
	}
	f.childrenOf = append(f.childrenOf, container.String())
	return url.Parse(container.String() + "child-" + string(rune('0'+len(f.childrenOf))))
}

// TestApplyBulkOp_TempIDCreateAndRefs: a temp-id create goes to CreateChild,
// a later create nests in it through InRef, and a Ref op lands on the created
// node — all reported against the URIs the source chose.
func TestApplyBulkOp_TempIDCreateAndRefs(t *testing.T) {
	f := &fakeCreatingMutator{}
	root := mustURL(t, "fake://h/")

	result, err := BestEffortBulkMutate(context.Background(), f, f, BulkRequest{
		Atomicity: BulkBestEffort,
		Ops: []BulkOp{
			{Kind: BulkCreate, TempID: "+a", In: root, Type: "fake-v1"},
			{Kind: BulkCreate, TempID: "+b", InRef: "+a", Type: "fake-v1"},
			{Kind: BulkDelete, Ref: "+a"},
		},
	})
	if err != nil {
		t.Fatalf("BestEffortBulkMutate: %v", err)
	}

	if !slices.Equal(f.childrenOf, []string{"fake://h/", "fake://h/child-1"}) {
		t.Errorf("CreateChild containers = %v", f.childrenOf)
	}
	if !slices.Equal(f.deleted, []string{"fake://h/child-1"}) {
		t.Errorf("the Ref op landed on %v, want the +a node", f.deleted)
	}
	if len(result.Created) != 2 || result.Created[0].TempID != "+a" ||
		result.Created[1].URI.String() != "fake://h/child-1child-2" {
		t.Errorf("created = %+v", result.Created)
	}
	if len(result.AppliedNodes) != 3 || len(result.Failed) != 0 {
		t.Errorf("applied=%v failed=%+v", result.AppliedNodes, result.Failed)
	}
}

// TestApplyBulkOp_FailedCreateFailsDependents: best-effort — an op naming a
// temp id whose create failed fails without running.
func TestApplyBulkOp_FailedCreateFailsDependents(t *testing.T) {
	f := &fakeCreatingMutator{failCreate: true}

	result, err := BestEffortBulkMutate(context.Background(), f, f, BulkRequest{
		Atomicity: BulkBestEffort,
		Ops: []BulkOp{
			{Kind: BulkCreate, TempID: "+a", In: mustURL(t, "fake://h/"), Type: "fake-v1"},
			{Kind: BulkPatch, Ref: "+a"},
		},
	})
	if err != nil {
		t.Fatalf("BestEffortBulkMutate: %v", err)
	}
	if len(f.patched) != 0 {
		t.Errorf("the dependent patch ran: %v", f.patched)
	}
	if len(result.Failed) != 2 || result.Failed[1].Ref != "+a" ||
		!strings.Contains(result.Failed[1].Err, "depends on") {
		t.Errorf("failed = %+v", result.Failed)
	}
}

// TestApplyBulkOp_TempIDNeedsContainerCreator: a plugin that cannot create
// server-assigned children fails the op, not the call.
func TestApplyBulkOp_TempIDNeedsContainerCreator(t *testing.T) {
	f := &fakeMutatorLister{}
	var result BulkResult
	ApplyBulkOp(context.Background(), f, BulkOp{
		Kind: BulkCreate, TempID: "+a", In: mustURL(t, "fake://h/"),
	}, &result)
	if len(result.Failed) != 1 || result.Failed[0].Ref != "+a" {
		t.Errorf("failed = %+v", result.Failed)
	}
}

// TestApplyBulkOp_NilAppliedIsApplied: a plugin that does not report patch
// fields (nil) has its patch counted applied, not patchedNothing (#182).
func TestApplyBulkOp_NilAppliedIsApplied(t *testing.T) {
	f := &fakeMutatorLister{}
	var result BulkResult
	ApplyBulkOp(context.Background(), f, BulkOp{
		Kind: BulkPatch, URI: mustURL(t, "fake://h/task"),
	}, &result)
	if len(result.AppliedNodes) != 1 || len(result.PatchedNothing) != 0 {
		t.Errorf("applied=%v patchedNothing=%v", result.AppliedNodes, result.PatchedNothing)
	}
}

func TestBulkRequestValidate_TempIDRules(t *testing.T) {
	root := &url.URL{Scheme: "fake", Host: "h", Path: "/"}
	node := &url.URL{Scheme: "fake", Host: "h", Path: "/n"}
	create := func(id string) BulkOp {
		return BulkOp{Kind: BulkCreate, TempID: id, In: root}
	}

	for name, req := range map[string]BulkRequest{
		"no address":       {Ops: []BulkOp{{Kind: BulkDelete}}},
		"uri and ref":      {Ops: []BulkOp{create("+a"), {Kind: BulkDelete, URI: node, Ref: "+a"}}},
		"forward ref":      {Ops: []BulkOp{{Kind: BulkDelete, Ref: "+a"}, create("+a")}},
		"undefined in_ref": {Ops: []BulkOp{{Kind: BulkCreate, TempID: "+b", InRef: "+a"}}},
		"redefined":        {Ops: []BulkOp{create("+a"), create("+a")}},
		"temp id on patch": {Ops: []BulkOp{{Kind: BulkPatch, TempID: "+a", In: root}}},
		"no container":     {Ops: []BulkOp{{Kind: BulkCreate, TempID: "+a"}}},
		"two containers":   {Ops: []BulkOp{create("+a"), {Kind: BulkCreate, TempID: "+b", In: root, InRef: "+a"}}},
		"in without temp":  {Ops: []BulkOp{{Kind: BulkCreate, URI: node, In: root}}},
		"atomic temp ids":  {Atomicity: BulkAtomic, Ops: []BulkOp{create("+a")}},
		"temp id in sweep": {Sweep: &BulkSweep{Root: root, Op: BulkOp{Kind: BulkDelete, Ref: "+a"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if req.Atomicity == "" {
				req.Atomicity = BulkBestEffort
			}
			if err := req.Validate(); err == nil {
				t.Error("accepted")
			}
		})
	}

	ok := BulkRequest{Atomicity: BulkBestEffort, Ops: []BulkOp{
		create("+a"),
		{Kind: BulkCreate, TempID: "+b", InRef: "+a"},
		{Kind: BulkPatch, Ref: "+b"},
		{Kind: BulkDelete, URI: node},
	}}
	if err := ok.Validate(); err != nil {
		t.Errorf("valid temp-id request refused: %v", err)
	}
}
