package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"
)

// fakeMutator is a registry-free NodeMutator for the tool tests. It records
// the URIs it was asked to mutate and can be made to fail.
type fakeMutator struct {
	created, put, patched, deleted []string
	failCreate                     bool
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

func (f *fakeMutator) PutNode(_ context.Context, u *url.URL, _ io.Reader) error {
	f.put = append(f.put, u.String())
	return nil
}

func (f *fakeMutator) PatchNode(_ context.Context, u *url.URL, _ io.Reader) error {
	f.patched = append(f.patched, u.String())
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
	// 3 read tools (describe/read/list) + 4 CUD tools (destructive).
	if len(res.Tools) != 7 {
		t.Fatalf("got %d tools, want 7: %+v", len(res.Tools), res.Tools)
	}
	annByName := map[string]*protocol.ToolAnnotations{}
	for _, tl := range res.Tools {
		annByName[tl.Name] = tl.Annotations
	}
	// The CUD tools are destructive / not read-only.
	for _, name := range []string{"create_node", "put_node", "patch_node", "delete_node"} {
		a := annByName[name]
		if a == nil {
			t.Fatalf("missing tool %q in %v", name, annByName)
		}
		if a.DestructiveHint == nil || !*a.DestructiveHint || a.ReadOnlyHint == nil || *a.ReadOnlyHint {
			t.Errorf("%q annotations = %+v, want destructive/non-readonly", name, a)
		}
	}
	// The read tools are read-only / not destructive.
	for _, name := range []string{"describe_node_types", "read_node", "list_nodes"} {
		a := annByName[name]
		if a == nil {
			t.Fatalf("missing tool %q in %v", name, annByName)
		}
		if a.ReadOnlyHint == nil || !*a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint {
			t.Errorf("%q annotations = %+v, want readonly/non-destructive", name, a)
		}
	}
}

func TestListTools_V0AdvertisesAll(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	list, err := tools.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(list) != 7 {
		t.Fatalf("got %d tools, want 7 (3 read + 4 CUD)", len(list))
	}
}

func TestListTools_ReadToolsAlwaysAdvertised(t *testing.T) {
	// A root whose scheme has no mutator → no write tools, but the read-only
	// discovery tools are still advertised.
	tools := newFakeTools(t, &fakeMutator{}, "bogus://h/")
	names := map[string]bool{}
	list, _ := tools.ListTools(context.Background())
	for _, tl := range list {
		names[tl.Name] = true
	}
	if len(list) != 3 || !names["describe_node_types"] || !names["read_node"] || !names["list_nodes"] {
		t.Errorf("ListTools without a mutator = %+v, want only the 3 read tools", list)
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

func TestCallTool_PutDispatches(t *testing.T) {
	m := &fakeMutator{}
	tools := newFakeTools(t, m, "faketest://h/")
	args := json.RawMessage(`{"uri":"faketest://h/x.ics","body":"BEGIN:VEVENT"}`)

	res, err := tools.CallTool(context.Background(), "put_node", args)
	if err != nil {
		t.Fatalf("CallTool transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res.Content)
	}
	if len(m.put) != 1 || m.put[0] != "faketest://h/x.ics" {
		t.Errorf("PutNode not dispatched: put=%v", m.put)
	}
}

func TestCallTool_PatchDispatches(t *testing.T) {
	m := &fakeMutator{}
	tools := newFakeTools(t, m, "faketest://h/")
	args := json.RawMessage(`{"uri":"faketest://h/x.ics","body":"{\"component\":\"VTODO\",\"task\":{\"summary\":\"new\"}}"}`)

	res, err := tools.CallTool(context.Background(), "patch_node", args)
	if err != nil {
		t.Fatalf("CallTool transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res.Content)
	}
	if len(m.patched) != 1 || m.patched[0] != "faketest://h/x.ics" {
		t.Errorf("PatchNode not dispatched: patched=%v", m.patched)
	}
}

// fakeCreator implements ContainerCreator + BodyDescriber: one type is
// declared server-assigned, so create_node dispatches CreateChild for it
// (uri = the container) and CreateNode for everything else.
type fakeCreator struct {
	fakeMutator
	childrenOf []string
}

func (f *fakeCreator) DescribeBodies() []cutting_garden_plugins.NodeTypeBody {
	return []cutting_garden_plugins.NodeTypeBody{
		{Tag: "test-assigned-v1", ServerAssignedIdentity: true},
		{Tag: "test-object-v1"},
	}
}

func (f *fakeCreator) CreateChild(
	_ context.Context, container *url.URL, _ io.Reader, _ string,
) (*url.URL, error) {
	f.childrenOf = append(f.childrenOf, container.String())
	return url.Parse(container.String() + "/assigned-9")
}

// TestCallTool_CreateChildDispatches pins the #143 tool semantics: a
// server-assigned type routes create_node to CreateChild with the uri
// as the CONTAINER and reports the URI the source chose; a
// caller-named type on the same plugin still routes to CreateNode.
func TestCallTool_CreateChildDispatches(t *testing.T) {
	c := &fakeCreator{}
	tools := newFakeTools(t, &c.fakeMutator, "faketest://h/")
	tools.resolveCreator = func(
		uriStr string,
	) (*url.URL, cutting_garden_plugins.ContainerCreator, error) {
		u, err := url.Parse(uriStr)
		return u, c, err
	}

	res, err := tools.CallTool(context.Background(), "create_node",
		json.RawMessage(`{"uri":"faketest://h/box","body":"b","type":"test-assigned-v1"}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res.Content)
	}
	if len(c.childrenOf) != 1 || c.childrenOf[0] != "faketest://h/box" {
		t.Fatalf("CreateChild not dispatched with the container: %v",
			c.childrenOf)
	}
	if len(res.Content) == 0 ||
		!strings.Contains(res.Content[0].Text, "faketest://h/box/assigned-9") {
		t.Errorf("result does not report the created URI: %+v", res.Content)
	}

	// A caller-named type on the same plugin still takes CreateNode.
	res, err = tools.CallTool(context.Background(), "create_node",
		json.RawMessage(`{"uri":"faketest://h/x.ics","body":"b","type":"test-object-v1"}`))
	if err != nil || res.IsError {
		t.Fatalf("caller-named create failed: %v %+v", err, res)
	}
	if len(c.created) != 1 || c.created[0] != "faketest://h/x.ics" {
		t.Errorf("CreateNode not dispatched: %v", c.created)
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

// fakeReader is a resourceReader returning a fixed read result, so the
// read_node / list_nodes(uri) dispatch can be tested without the plugin
// registry.
type fakeReader struct {
	read *protocol.ResourceReadResult
}

func (f fakeReader) ReadResource(context.Context, string) (*protocol.ResourceReadResult, error) {
	return f.read, nil
}

func TestCallTool_ReadNodeReturnsContentAndBlobLink(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.reader = fakeReader{read: &protocol.ResourceReadResult{Contents: []protocol.ResourceContent{
		{URI: "faketest://h/x.ics", MimeType: "application/json", Text: `{"summary":"S"}`},
		{URI: "madder://blobs/abc", MimeType: "text/calendar"},
	}}}

	res, err := tools.CallTool(context.Background(), "read_node",
		json.RawMessage(`{"uri":"faketest://h/x.ics"}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("read_node errored: %+v", res.Content)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, `"summary":"S"`) {
		t.Errorf("read_node missing the object JSON: %q", text)
	}
	if !strings.Contains(text, "raw bytes: madder://blobs/abc") {
		t.Errorf("read_node missing the blob link note: %q", text)
	}
}

func TestCallTool_ListNodesRootsAndContainer(t *testing.T) {
	// No uri → the configured roots THEMSELVES, as container entry points
	// (not their children): mirrors the `list` command and avoids flattening
	// a per-calendar root's events into the entry-point listing (#15).
	roots := newFakeTools(t, &fakeMutator{}, "faketest://h/cal-a/", "faketest://h/cal-b/")
	res, err := roots.CallTool(context.Background(), "list_nodes", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_nodes() errored: %+v", res.Content)
	}
	var views []nodeView
	if err := json.Unmarshal([]byte(res.Content[0].Text), &views); err != nil {
		t.Fatalf("list_nodes() output is not a node-view array: %v (%q)", err, res.Content[0].Text)
	}
	if len(views) != 2 {
		t.Fatalf("list_nodes() = %d nodes, want the 2 configured roots: %+v", len(views), views)
	}
	byURI := map[string]nodeView{}
	for _, v := range views {
		byURI[v.URI] = v
	}
	if a, ok := byURI["faketest://h/cal-a/"]; !ok || !a.Container || a.Name != "cal-a" {
		t.Errorf("root cal-a = %+v (present=%v), want a container named cal-a", a, ok)
	}
	if b, ok := byURI["faketest://h/cal-b/"]; !ok || !b.Container || b.Name != "cal-b" {
		t.Errorf("root cal-b = %+v (present=%v), want a container named cal-b", b, ok)
	}

	// A uri → that container's child listing (ReadResource).
	sub := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	sub.reader = fakeReader{read: &protocol.ResourceReadResult{Contents: []protocol.ResourceContent{
		{URI: "faketest://h/work", MimeType: "application/json", Text: `[{"uri":"faketest://h/work/a.ics"}]`},
	}}}
	res, err = sub.CallTool(context.Background(), "list_nodes",
		json.RawMessage(`{"uri":"faketest://h/work"}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.IsError || !strings.Contains(res.Content[0].Text, "faketest://h/work/a.ics") {
		t.Errorf("list_nodes(uri) = %+v, want the container's child", res.Content)
	}
}
