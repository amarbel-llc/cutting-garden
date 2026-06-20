package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"
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

func TestListToolsV1_AdvertisesToolsWithCorrectAnnotations(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")

	res, err := tools.ListToolsV1(context.Background(), "")
	if err != nil {
		t.Fatalf("ListToolsV1: %v", err)
	}
	// describe_node_types (read) + the 3 CUD tools (destructive).
	if len(res.Tools) != 4 {
		t.Fatalf("got %d tools, want 4: %+v", len(res.Tools), res.Tools)
	}
	annByName := map[string]*protocol.ToolAnnotations{}
	for _, tl := range res.Tools {
		annByName[tl.Name] = tl.Annotations
	}
	for _, want := range []string{"describe_node_types", "create_node", "update_node", "delete_node"} {
		if annByName[want] == nil {
			t.Fatalf("missing tool %q in %v", want, annByName)
		}
	}
	// The CUD tools are destructive / not read-only.
	for _, name := range []string{"create_node", "update_node", "delete_node"} {
		a := annByName[name]
		if a.DestructiveHint == nil || !*a.DestructiveHint || a.ReadOnlyHint == nil || *a.ReadOnlyHint {
			t.Errorf("%q annotations = %+v, want destructive/non-readonly", name, a)
		}
	}
	// describe_node_types is read-only / not destructive.
	d := annByName["describe_node_types"]
	if d.ReadOnlyHint == nil || !*d.ReadOnlyHint || d.DestructiveHint == nil || *d.DestructiveHint {
		t.Errorf("describe_node_types annotations = %+v, want readonly/non-destructive", d)
	}
}

func TestListTools_V0AdvertisesFour(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	list, err := tools.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(list) != 4 {
		t.Fatalf("got %d tools, want 4 (describe + 3 CUD)", len(list))
	}
}

func TestListTools_DescribeAlwaysAdvertised(t *testing.T) {
	// A root whose scheme has no mutator → no write tools, but the read-only
	// describe_node_types is still advertised.
	tools := newFakeTools(t, &fakeMutator{}, "bogus://h/")
	list, _ := tools.ListTools(context.Background())
	if len(list) != 1 || list[0].Name != "describe_node_types" {
		t.Errorf("ListTools without a mutator = %+v, want only describe_node_types", list)
	}
	v1, _ := tools.ListToolsV1(context.Background(), "")
	if len(v1.Tools) != 1 || v1.Tools[0].Name != "describe_node_types" {
		t.Errorf("ListToolsV1 without a mutator = %+v, want only describe_node_types", v1.Tools)
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

// fakeSchemaPlugin is a RootLister (via fakeLister) that also describes its
// writable type's body — exercising the describe_node_types collector.
type fakeSchemaPlugin struct{ fakeLister }

func (fakeSchemaPlugin) DescribeBodies() []cutting_garden_plugins.NodeTypeBody {
	return []cutting_garden_plugins.NodeTypeBody{{
		Tag:     "test-object-v1",
		Accepts: []string{"text/calendar (raw)", "application/json (object)"},
		Example: map[string]any{"summary": "example"},
	}}
}

func TestCollectSchema_TypesAndWritability(t *testing.T) {
	schemas := collectSchema([]cutting_garden_plugins.Plugin{fakeSchemaPlugin{}})
	if len(schemas) != 1 {
		t.Fatalf("got %d schemes, want 1: %+v", len(schemas), schemas)
	}
	if schemas[0].Scheme != "faketest" {
		t.Errorf("scheme = %q, want faketest", schemas[0].Scheme)
	}
	byTag := map[string]typeSchema{}
	for _, ts := range schemas[0].Types {
		byTag[ts.Tag] = ts
	}

	// The container is not writable and carries no body.
	if cal := byTag["test-calendar-v1"]; !cal.Container || cal.Writable || cal.Body != nil {
		t.Errorf("calendar type = %+v, want container/non-writable/no-body", cal)
	}
	// The leaf with a described body is writable, with its mimetype + payload.
	obj := byTag["test-object-v1"]
	if obj.Container || !obj.Writable || obj.Body == nil {
		t.Fatalf("object type = %+v, want leaf/writable/with-body", obj)
	}
	if obj.LeafMimeType != "text/calendar" {
		t.Errorf("object leafMimeType = %q, want text/calendar", obj.LeafMimeType)
	}
	if len(obj.Body.Accepts) != 2 || obj.Body.Example == nil {
		t.Errorf("object body = %+v, want accepts+example", obj.Body)
	}
}

func TestCollectSchema_SkipsNonRootListers(t *testing.T) {
	// A plugin with no traversal (here a bare NodeMutator) contributes no
	// type catalogue.
	if s := collectSchema([]cutting_garden_plugins.Plugin{&fakeMutator{}}); len(s) != 0 {
		t.Errorf("got %d schemes from a non-RootLister, want 0", len(s))
	}
}

// TestCallTool_DescribeReturnsJSON exercises the describe_node_types dispatch:
// it returns a non-error result whose text is a JSON array (empty in the test
// binary, where no plugins are registered — the dispatch + marshal are what's
// under test).
func TestCallTool_DescribeReturnsJSON(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	res, err := tools.CallTool(context.Background(), "describe_node_types", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("describe_node_types errored: %+v", res.Content)
	}
	var arr []any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &arr); err != nil {
		t.Fatalf("describe output is not a JSON array: %v (%q)", err, res.Content[0].Text)
	}
}
