package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// fakeMutator is a registry-free NodeMutator for the tool tests. It records
// the URIs it was asked to mutate and can be made to fail.
type fakeMutator struct {
	created, updated, deleted []string
	failCreate                bool
}

func (*fakeMutator) Schemes() []string                     { return []string{"faketest"} }
func (*fakeMutator) TypeTag() string                       { return "cutting_garden-test-v1" }
func (*fakeMutator) ValidateSource(*url.URL, string) error { return nil }
func (*fakeMutator) CaptureRoot(
	cutting_garden_plugins.CaptureRootRequest,
) cutting_garden_plugins.CaptureRootResult {
	return cutting_garden_plugins.CaptureRootResult{}
}

func (f *fakeMutator) CreateNode(_ context.Context, u *url.URL, _ io.Reader, _ string) error {
	if f.failCreate {
		return errors.ErrorWithStackf("boom")
	}
	f.created = append(f.created, u.String())
	return nil
}

func (f *fakeMutator) UpdateNode(_ context.Context, u *url.URL, _ io.Reader) error {
	f.updated = append(f.updated, u.String())
	return nil
}

func (f *fakeMutator) DeleteNode(_ context.Context, u *url.URL) error {
	f.deleted = append(f.deleted, u.String())
	return nil
}

func fakeMutatorResolve(m *fakeMutator) mutatorResolveFunc {
	return func(uriStr string) (*url.URL, cutting_garden_plugins.NodeMutator, error) {
		u, err := url.Parse(uriStr)
		if err != nil {
			return nil, nil, errors.Wrap(err)
		}
		if u.Scheme != "faketest" {
			return nil, nil, errors.ErrorWithStackf("no mutator for scheme %q", u.Scheme)
		}
		return u, m, nil
	}
}

func newFakeTools(t *testing.T, m *fakeMutator, rootStrs ...string) *Tools {
	t.Helper()
	roots := make([]*url.URL, 0, len(rootStrs))
	for _, s := range rootStrs {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatalf("parse root %q: %v", s, err)
		}
		roots = append(roots, u)
	}
	return &Tools{roots: roots, resolve: fakeMutatorResolve(m)}
}

func TestListToolsV1_AdvertisesDestructiveTools(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")

	res, err := tools.ListToolsV1(context.Background(), "")
	if err != nil {
		t.Fatalf("ListToolsV1: %v", err)
	}
	if len(res.Tools) != 3 {
		t.Fatalf("got %d tools, want 3: %+v", len(res.Tools), res.Tools)
	}
	names := map[string]bool{}
	for _, tl := range res.Tools {
		names[tl.Name] = true
		if tl.Annotations == nil || tl.Annotations.DestructiveHint == nil || !*tl.Annotations.DestructiveHint {
			t.Errorf("tool %q is not annotated destructive: %+v", tl.Name, tl.Annotations)
		}
		if tl.Annotations.ReadOnlyHint == nil || *tl.Annotations.ReadOnlyHint {
			t.Errorf("tool %q must not be read-only: %+v", tl.Name, tl.Annotations)
		}
	}
	for _, want := range []string{"create_node", "update_node", "delete_node"} {
		if !names[want] {
			t.Errorf("missing tool %q in %v", want, names)
		}
	}
}

func TestListTools_V0AdvertisesThree(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	list, err := tools.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("got %d tools, want 3", len(list))
	}
}

func TestListTools_EmptyWithoutMutatorRoot(t *testing.T) {
	// A root whose scheme has no mutator → the server advertises no tools.
	tools := newFakeTools(t, &fakeMutator{}, "bogus://h/")
	if list, _ := tools.ListTools(context.Background()); len(list) != 0 {
		t.Errorf("ListTools = %d, want 0 (no mutator root)", len(list))
	}
	if v1, _ := tools.ListToolsV1(context.Background(), ""); len(v1.Tools) != 0 {
		t.Errorf("ListToolsV1 = %d, want 0 (no mutator root)", len(v1.Tools))
	}
}

func TestCallTool_CreateDispatches(t *testing.T) {
	m := &fakeMutator{}
	tools := newFakeTools(t, m, "faketest://h/")
	args := json.RawMessage(`{"uri":"faketest://h/x.ics","body":"BEGIN:VEVENT","type":"caldav-object-v1"}`)

	res, err := tools.CallTool(context.Background(), "create_node", args)
	if err != nil {
		t.Fatalf("CallTool transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res.Content)
	}
	if len(m.created) != 1 || m.created[0] != "faketest://h/x.ics" {
		t.Errorf("CreateNode not dispatched: created=%v", m.created)
	}
}

func TestCallToolV1_DeleteDispatches(t *testing.T) {
	m := &fakeMutator{}
	tools := newFakeTools(t, m, "faketest://h/")

	res, err := tools.CallToolV1(context.Background(), "delete_node",
		json.RawMessage(`{"uri":"faketest://h/x.ics"}`))
	if err != nil {
		t.Fatalf("CallToolV1 transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res.Content)
	}
	if len(m.deleted) != 1 {
		t.Errorf("DeleteNode not dispatched: deleted=%v", m.deleted)
	}
}

func TestCallTool_NonMutatorURIIsToolError(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	res, err := tools.CallTool(context.Background(), "create_node",
		json.RawMessage(`{"uri":"bogus://h/x","body":"b"}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !res.IsError {
		t.Error("a non-mutator scheme must yield an IsError tool result")
	}
}

func TestCallTool_MutationFailureIsToolError(t *testing.T) {
	m := &fakeMutator{failCreate: true}
	tools := newFakeTools(t, m, "faketest://h/")
	// A mutation rejection must be an IsError result, NOT a transport error
	// (err==nil) — the agent reads the message and decides.
	res, err := tools.CallTool(context.Background(), "create_node",
		json.RawMessage(`{"uri":"faketest://h/x.ics","body":"b"}`))
	if err != nil {
		t.Fatalf("mutation failure must not be a transport error: %v", err)
	}
	if !res.IsError {
		t.Error("mutation failure must be an IsError tool result")
	}
}

func TestCallTool_UnknownToolIsError(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	res, err := tools.CallTool(context.Background(), "nope", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !res.IsError {
		t.Error("unknown tool must yield an IsError result")
	}
}
