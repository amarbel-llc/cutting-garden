package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/purse-first/libs/go-mcp/protocol"
)

// fakeMutator is a registry-free NodeMutator for the tool tests. It records
// the URIs it was asked to mutate and can be made to fail.
type fakeMutator struct {
	created, put, patched, deleted []string
	failCreate                     bool
	// patchApplied is what PatchNode reports as applied; nil means "does
	// not report" and a non-nil empty slice means "nothing was applied"
	// (cutting-garden#182).
	patchApplied []string
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

func (f *fakeMutator) PatchNode(
	_ context.Context, u *url.URL, _ io.Reader,
) ([]string, error) {
	f.patched = append(f.patched, u.String())
	return f.patchApplied, nil
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

// fakeBulkMutator is a registry-free BulkMutator for the bulk_mutate tool
// test: it records the request it received and returns a canned result.
type fakeBulkMutator struct {
	gotReq cutting_garden_plugins.BulkRequest
	result cutting_garden_plugins.BulkResult
}

func (fakeBulkMutator) Schemes() []string { return []string{"faketest"} }
func (fakeBulkMutator) TypeTag() string   { return "fake-bulk-v1" }
func (fakeBulkMutator) Types() []cutting_garden_plugins.NodeType {
	return nil
}

func (fakeBulkMutator) ListRoots(
	context.Context, *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	return nil, nil
}

func (f *fakeBulkMutator) BulkMutate(
	_ context.Context, req cutting_garden_plugins.BulkRequest,
) (cutting_garden_plugins.BulkResult, error) {
	f.gotReq = req
	return f.result, nil
}

func fakeBulkResolve(bm *fakeBulkMutator) bulkResolveFunc {
	return func(uriStr string) (
		*url.URL, cutting_garden_plugins.BulkMutator, error,
	) {
		u, err := url.Parse(uriStr)
		if err != nil {
			return nil, nil, err
		}
		if u.Scheme != "faketest" {
			return nil, nil, errors.ErrorWithStackf(
				"unknown scheme %q", u.Scheme,
			)
		}
		return u, bm, nil
	}
}

func TestCallTool_BulkMutateChangeset(t *testing.T) {
	bm := &fakeBulkMutator{}
	applied, _ := url.Parse("faketest://h/a.ics")
	bm.result = cutting_garden_plugins.BulkResult{
		AppliedNodes: []*url.URL{applied},
	}
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.resolveBulk = fakeBulkResolve(bm)

	res, err := tools.CallTool(context.Background(), "bulk_mutate",
		json.RawMessage(`{"ops":[`+
			`{"kind":"patch","uri":"faketest://h/a.ics","body":"{\"x\":1}"},`+
			`{"kind":"delete","uri":"faketest://h/b.ics"}]}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("bulk_mutate errored: %+v", res.Content)
	}

	// atomicity defaulted to best-effort at the tool layer; two ops decoded.
	if bm.gotReq.Atomicity != cutting_garden_plugins.BulkBestEffort {
		t.Errorf("atomicity = %q, want best-effort", bm.gotReq.Atomicity)
	}
	if len(bm.gotReq.Ops) != 2 ||
		bm.gotReq.Ops[0].Kind != cutting_garden_plugins.BulkPatch ||
		bm.gotReq.Ops[1].Kind != cutting_garden_plugins.BulkDelete {
		t.Fatalf("ops = %+v", bm.gotReq.Ops)
	}
	if string(bm.gotReq.Ops[0].Body) != `{"x":1}` {
		t.Errorf("op body = %q", bm.gotReq.Ops[0].Body)
	}
	if !strings.Contains(res.Content[0].Text, "faketest://h/a.ics") {
		t.Errorf("result missing applied node: %q", res.Content[0].Text)
	}
}

// TestCallTool_BulkMutateRejectsBothOpsAndSweep pins that a request
// supplying BOTH ops and sweep is rejected (BulkRequest.Validate's "got
// both") and — the point of the fix — that the ops are NOT silently dropped
// while the sweep runs: BulkMutate must never be dispatched at all.
func TestCallTool_BulkMutateRejectsBothOpsAndSweep(t *testing.T) {
	bm := &fakeBulkMutator{}
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.resolveBulk = fakeBulkResolve(bm)

	res, err := tools.CallTool(context.Background(), "bulk_mutate",
		json.RawMessage(`{`+
			`"ops":[{"kind":"delete","uri":"faketest://h/a.ics"}],`+
			`"sweep":{"root":"faketest://h/","op":{"kind":"delete"}}}`))

	// However the layer surfaces the bad request (transport error or an
	// IsError result), it must not read as a clean success.
	if err == nil && !res.IsError {
		t.Errorf("both ops and sweep accepted as success: %+v", res)
	}
	// The zero Atomicity proves BulkMutate was never called — the real call
	// path always sets a non-empty atomicity before dispatch.
	if bm.gotReq.Atomicity != "" {
		t.Errorf("BulkMutate dispatched despite the both-set rejection: %+v",
			bm.gotReq)
	}
}

// TestToolInputSchemasAreValidJSON guards every tool's InputSchema const:
// a malformed schema ships as an unusable tool (MCP clients reject an
// invalid inputSchema), and nothing else in the suite validates the raw
// JSON of these hand-written strings.
func TestToolInputSchemasAreValidJSON(t *testing.T) {
	for name, schema := range map[string]string{
		"create_node":         createNodeSchema,
		"put_node":            putNodeSchema,
		"patch_node":          patchNodeSchema,
		"delete_node":         deleteNodeSchema,
		"bulk_mutate":         bulkMutateSchema,
		"describe_node_types": describeNodeTypesSchema,
		"list_nodes":          listNodesSchema,
		"read_node":           readNodeSchema,
		"read_facets":         readFacetsSchema,
	} {
		if !json.Valid([]byte(schema)) {
			t.Errorf("%s InputSchema is not valid JSON:\n%s", name, schema)
		}
	}
}

// TestListTools_BulkMutateAdvertisedWhenCapable pins that bulk_mutate rides
// the tool list only when a root's plugin implements BulkMutator.
func TestListTools_BulkMutateAdvertisedWhenCapable(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.resolveBulk = fakeBulkResolve(&fakeBulkMutator{})

	list, _ := tools.ListTools(context.Background())
	found := false
	for _, tl := range list {
		if tl.Name == "bulk_mutate" {
			found = true
		}
	}
	if !found {
		t.Error("bulk_mutate not advertised despite a BulkMutator root")
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
	// 4 read tools (describe/read/list/read_facets) + 4 CUD tools (destructive).
	if len(res.Tools) != 8 {
		t.Fatalf("got %d tools, want 8: %+v", len(res.Tools), res.Tools)
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
	for _, name := range []string{"describe_node_types", "read_node", "list_nodes", "read_facets"} {
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
	if len(list) != 8 {
		t.Fatalf("got %d tools, want 8 (4 read + 4 CUD)", len(list))
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
	if len(list) != 4 || !names["describe_node_types"] || !names["read_node"] ||
		!names["list_nodes"] || !names["read_facets"] {
		t.Errorf("ListTools without a mutator = %+v, want only the 4 read tools", list)
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

// The three states of PatchNode's applied, as the patch_node tool renders
// them (cutting-garden#182). The middle case is the one that matters: it is
// exactly the shape of the cutting-garden#180 field report — the plugin
// recognized nothing, changed nothing, and previously answered "patched".
func TestCallTool_PatchReportsAppliedFields(t *testing.T) {
	args := json.RawMessage(
		`{"uri":"faketest://h/x.ics","body":"{\"summary\":\"new\",\"bogus\":1}"}`,
	)

	for _, testCase := range []struct {
		name       string
		applied    []string
		wantErr    bool
		wantInText string
	}{
		{
			name:       "reported and applied",
			applied:    []string{"summary"},
			wantInText: "applied: summary",
		},
		{
			// Plain success here would be the #180 defect: the caller has
			// no error, no signal, and no reason to re-read.
			name:       "reported as nothing applied",
			applied:    []string{},
			wantErr:    true,
			wantInText: "nothing was applied",
		},
		{
			// nil carries no information, so the tool must not invent the
			// no-op verdict for a plugin that never claimed one.
			name:       "not reported",
			applied:    nil,
			wantInText: "patched faketest://h/x.ics",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			m := &fakeMutator{patchApplied: testCase.applied}
			tools := newFakeTools(t, m, "faketest://h/")

			res, err := tools.CallTool(context.Background(), "patch_node", args)
			if err != nil {
				t.Fatalf("CallTool transport error: %v", err)
			}
			if res.IsError != testCase.wantErr {
				t.Fatalf(
					"IsError = %v, want %v (content: %+v)",
					res.IsError, testCase.wantErr, res.Content,
				)
			}

			if len(res.Content) == 0 {
				t.Fatal("empty tool result content")
			}
			text := res.Content[0].Text
			if !strings.Contains(text, testCase.wantInText) {
				t.Errorf("result %q does not contain %q", text, testCase.wantInText)
			}
		})
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

// TestFacetDimSchemas_RevalidateAfterSeconds pins the RFC 0012 §11.3 schema
// surface (cutting-garden#124): a pure dimension's revalidateAfterSeconds is
// omitted (zero value + omitempty); a volatile dimension's is the
// RevalidateAfter duration in whole seconds.
func TestFacetDimSchemas_RevalidateAfterSeconds(t *testing.T) {
	dims := []cutting_garden_plugins.FacetDimension{
		{Key: "status", Kind: cutting_garden_plugins.FacetCategorical},
		{
			Key: "due", Kind: cutting_garden_plugins.FacetNumericBucket,
			RevalidateAfter: 15 * time.Minute,
		},
	}
	schemas := facetDimSchemas(dims)
	byKey := map[string]facetDimSchema{}
	for _, s := range schemas {
		byKey[s.Key] = s
	}

	if got := byKey["status"].RevalidateAfterSeconds; got != 0 {
		t.Errorf("status revalidateAfterSeconds = %d, want 0 (pure)", got)
	}
	if got := byKey["due"].RevalidateAfterSeconds; got != 900 {
		t.Errorf("due revalidateAfterSeconds = %d, want 900 (15m)", got)
	}

	// omitempty: the JSON encoding must actually omit the zero value, and
	// carry the nonzero one, so a client can distinguish "pure" from
	// "declared volatile with a 0s window" (an invalid but distinguishable
	// state) purely by field presence.
	body, err := json.Marshal(byKey["status"])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "revalidateAfterSeconds") {
		t.Errorf("pure dimension JSON carries revalidateAfterSeconds: %s", body)
	}
	body, err = json.Marshal(byKey["due"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"revalidateAfterSeconds":900`) {
		t.Errorf("volatile dimension JSON missing revalidateAfterSeconds: %s", body)
	}
}

// TestCallTool_ListNodesRootsPagination pins cutting-garden#86 phase A on
// the no-uri (roots) branch: limit/offset slice host-side after
// enumeration, and an out-of-range offset yields an empty array, not an
// error.
func TestCallTool_ListNodesRootsPagination(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{},
		"faketest://h/a/", "faketest://h/b/", "faketest://h/c/")

	res, err := tools.CallTool(context.Background(), "list_nodes",
		json.RawMessage(`{"limit":2}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	var views []nodeView
	if err := json.Unmarshal([]byte(res.Content[0].Text), &views); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("limit=2 got %d roots, want 2: %+v", len(views), views)
	}

	res, err = tools.CallTool(context.Background(), "list_nodes",
		json.RawMessage(`{"offset":1,"limit":2}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	views = nil
	if err := json.Unmarshal([]byte(res.Content[0].Text), &views); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(views) != 2 || views[0].URI != "faketest://h/b/" {
		t.Fatalf("offset=1,limit=2 = %+v, want [b, c]", views)
	}

	// Out-of-range offset: an empty array, not an error.
	res, err = tools.CallTool(context.Background(), "list_nodes",
		json.RawMessage(`{"offset":100}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("out-of-range offset must not error: %+v", res.Content)
	}
	if res.Content[0].Text != "[]" {
		t.Errorf("out-of-range offset body = %q, want []", res.Content[0].Text)
	}
}

// TestCallTool_ListNodesContainerPagination pins the uri branch: the child
// listing text (the #203 {nodes,version} wrapper, as rendered by
// renderContents) has its nodes sliced while the version block rides through.
// The fake reader returns the wrapper shape production actually produces —
// feeding a bare array here is what let the #203 pagination regression slip
// the gate.
func TestCallTool_ListNodesContainerPagination(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.reader = fakeReader{read: &protocol.ResourceReadResult{Contents: []protocol.ResourceContent{
		{
			URI: "faketest://h/work", MimeType: "application/json",
			Text: `{"nodes":[{"uri":"a"},{"uri":"b"},{"uri":"c"}],"version":"v1"}`,
		},
	}}}

	res, err := tools.CallTool(context.Background(), "list_nodes",
		json.RawMessage(`{"uri":"faketest://h/work","limit":2}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	var got listingView
	if err := json.Unmarshal([]byte(res.Content[0].Text), &got); err != nil {
		t.Fatalf("decode %q: %v", res.Content[0].Text, err)
	}
	if len(got.Nodes) != 2 || got.Nodes[0].URI != "a" || got.Nodes[1].URI != "b" {
		t.Fatalf("limit=2 = %+v, want nodes [a, b]", got.Nodes)
	}
	if got.Version != "v1" {
		t.Errorf("version %q not preserved through pagination", got.Version)
	}

	res, err = tools.CallTool(context.Background(), "list_nodes",
		json.RawMessage(`{"uri":"faketest://h/work","offset":5}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	got = listingView{}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &got); err != nil {
		t.Fatalf("decode %q: %v", res.Content[0].Text, err)
	}
	if len(got.Nodes) != 0 {
		t.Errorf("out-of-range offset nodes = %+v, want empty", got.Nodes)
	}
}

// TestCallTool_ListNodesDefaultUnpaged pins the byte-for-byte-unchanged
// default: with neither limit nor offset, list_nodes(uri) output is
// IDENTICAL to what renderContents alone would produce — pagination is
// skipped entirely, not applied with an effective limit=0.
func TestCallTool_ListNodesDefaultUnpaged(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.reader = fakeReader{read: &protocol.ResourceReadResult{Contents: []protocol.ResourceContent{
		{
			URI: "faketest://h/work", MimeType: "application/json",
			Text: `[{"uri":"a"},{"uri":"b"}]`,
		},
	}}}

	res, err := tools.CallTool(context.Background(), "list_nodes",
		json.RawMessage(`{"uri":"faketest://h/work"}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.Content[0].Text != `[{"uri":"a"},{"uri":"b"}]` {
		t.Errorf("default (no limit/offset) output = %q, want the raw listing unchanged",
			res.Content[0].Text)
	}
}

func TestCallTool_ListNodesNegativeLimitOrOffsetIsToolError(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")

	for _, args := range []string{`{"limit":-1}`, `{"offset":-1}`} {
		res, err := tools.CallTool(context.Background(), "list_nodes", json.RawMessage(args))
		if err != nil {
			t.Fatalf("transport error for %s: %v", args, err)
		}
		if !res.IsError {
			t.Errorf("args %s: want IsError, got %+v", args, res.Content)
		}
	}
}

// TestCallTool_ListNodesQuery drives the list_nodes `query` param (Gap 1,
// cutting-garden#211's MCP host) through the real CallTool dispatch against the
// two-level fakeLister tree: a forward walk and a reverse hop, mirroring the
// CLI `list --query` host so both surfaces evaluate the same query identically.
func TestCallTool_ListNodesQuery(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.resolveLister = listerResolve(fakeLister{})

	// Forward walk: the calendars -> their objects. Only /work holds one.
	res, err := tools.CallTool(context.Background(), "list_nodes",
		json.RawMessage(`{"uri":"faketest://h/","query":"!test-calendar-v1 -> !test-object-v1"}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("query walk errored: %+v", res.Content)
	}
	var view queriedListingView
	if err := json.Unmarshal([]byte(res.Content[0].Text), &view); err != nil {
		t.Fatalf("query output is not a queriedListingView: %v (%q)", err, res.Content[0].Text)
	}
	if view.Query != "!test-calendar-v1 -> !test-object-v1" {
		t.Errorf("query not echoed back: %q", view.Query)
	}
	if len(view.Nodes) != 1 || view.Nodes[0].URI != "faketest://h/work/task1.ics" {
		t.Fatalf("walk = %+v, want the single /work/task1.ics object", view.Nodes)
	}

	// Reverse: from the matched objects back up to the calendars holding them.
	res, err = tools.CallTool(context.Background(), "list_nodes",
		json.RawMessage(`{"uri":"faketest://h/","query":"!test-calendar-v1 -> !test-object-v1 <- !test-calendar-v1"}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("query reverse errored: %+v", res.Content)
	}
	view = queriedListingView{}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &view); err != nil {
		t.Fatalf("reverse output parse: %v (%q)", err, res.Content[0].Text)
	}
	if len(view.Nodes) != 1 || view.Nodes[0].URI != "faketest://h/work" {
		t.Fatalf("reverse = %+v, want the single /work calendar", view.Nodes)
	}
}

// TestCallTool_ListNodesQueryGuards pins the query param's usage errors — it
// requires a uri, is mutually exclusive with filter, and rejects a grammar
// form the evaluator does not yet support — each surfaced as a tool error, not
// a silent empty result.
func TestCallTool_ListNodesQueryGuards(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.resolveLister = listerResolve(fakeLister{})

	cases := []struct {
		name string
		args string
	}{
		{"anchorless query", `{"query":"!test-calendar-v1"}`},
		{"query with filter", `{"uri":"faketest://h/","query":"!test-calendar-v1","filter":"status=CONFIRMED"}`},
		{"unsupported typed combinator", `{"uri":"faketest://h/","query":"!a -[!x]-> !b"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tools.CallTool(context.Background(), "list_nodes", json.RawMessage(tc.args))
			if err != nil {
				t.Fatalf("transport error: %v", err)
			}
			if !res.IsError {
				t.Errorf("expected a tool error, got: %+v", res.Content)
			}
		})
	}
}

func TestPaginate_LimitZeroIsUnbounded(t *testing.T) {
	items := []int{1, 2, 3, 4}
	if got := paginate(items, 0, 0); len(got) != 4 {
		t.Errorf("paginate(offset=0,limit=0) = %v, want all 4 items", got)
	}
	if got := paginate(items, 2, 0); len(got) != 2 || got[0] != 3 {
		t.Errorf("paginate(offset=2,limit=0) = %v, want [3 4]", got)
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

func (f fakeReader) ReadNode(
	context.Context, string, string,
) (*protocol.ResourceReadResult, error) {
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

// fakeFacetReader is a facetReader recording the last call and returning a
// fixed view or error, so the read_facets tool dispatch can be tested
// without the plugin registry / facet cache.
type fakeFacetReader struct {
	view      *facetView
	err       error
	gotURI    string
	gotFilter cutting_garden_plugins.FacetFilter
}

func (f *fakeFacetReader) ReadFacets(
	_ context.Context, uri string, filter cutting_garden_plugins.FacetFilter,
) (*facetView, error) {
	f.gotURI = uri
	f.gotFilter = filter
	if f.err != nil {
		return nil, f.err
	}
	return f.view, nil
}

func TestCallTool_ReadFacetsDispatches(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	fr := &fakeFacetReader{view: &facetView{
		Facets:    cutting_garden_plugins.FacetSummary{"status": {"CONFIRMED": 2}},
		Complete:  true,
		Freshness: freshnessFresh,
	}}
	tools.facets = fr

	res, err := tools.CallTool(context.Background(), "read_facets",
		json.RawMessage(`{"uri":"faketest://h/work"}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("read_facets errored: %+v", res.Content)
	}
	if fr.gotURI != "faketest://h/work" {
		t.Errorf("ReadFacets called with uri=%q", fr.gotURI)
	}
	if len(fr.gotFilter) != 0 {
		t.Errorf("no-filter call passed a non-empty filter: %v", fr.gotFilter)
	}
	if !strings.Contains(res.Content[0].Text, `"CONFIRMED": 2`) {
		t.Errorf("read_facets output missing the summary: %q", res.Content[0].Text)
	}
}

// TestCallTool_ReadFacetsParsesFilter pins that the tool's filter string
// parses through the SAME grammar `list --filter` uses (dimension=value,
// comma-separated, AND-composed) via cutting_garden_plugins.ParseFacetFilter.
func TestCallTool_ReadFacetsParsesFilter(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	fr := &fakeFacetReader{view: &facetView{Complete: true}}
	tools.facets = fr

	res, err := tools.CallTool(context.Background(), "read_facets",
		json.RawMessage(`{"uri":"faketest://h/work","filter":"read=false,status=CONFIRMED"}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("read_facets errored: %+v", res.Content)
	}
	want := cutting_garden_plugins.FacetFilter{
		{Dimension: "read", Value: "false"},
		{Dimension: "status", Value: "CONFIRMED"},
	}
	if len(fr.gotFilter) != len(want) {
		t.Fatalf("filter = %+v, want %+v", fr.gotFilter, want)
	}
	for i := range want {
		if fr.gotFilter[i] != want[i] {
			t.Errorf("filter[%d] = %+v, want %+v", i, fr.gotFilter[i], want[i])
		}
	}
}

func TestCallTool_ReadFacetsInvalidFilterIsToolError(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.facets = &fakeFacetReader{}

	res, err := tools.CallTool(context.Background(), "read_facets",
		json.RawMessage(`{"uri":"faketest://h/work","filter":"bogus"}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !res.IsError {
		t.Error("an invalid filter predicate must be an IsError tool result")
	}
}

func TestCallTool_ReadFacetsUnavailableIsToolError(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.facets = &fakeFacetReader{err: errors.ErrorWithStackf("facets not available")}

	res, err := tools.CallTool(context.Background(), "read_facets",
		json.RawMessage(`{"uri":"faketest://h/work"}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !res.IsError {
		t.Error("a ReadFacets error must be an IsError tool result, not a transport error")
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

// TestCallTool_ListNodesRootsUsesRootLabelOverride pins cutting-garden#120:
// when a root's URI has a friendlier label supplied (e.g. by
// command_components.AggregateRootLabels from a caldav account's DAV
// displayname), the no-uri roots listing uses it instead of the bare
// URL-derived rootLabel() fallback — an opaque path segment like a
// calendar UID never has to leak into the entry-point listing when a
// friendlier name is available. A root with NO override still falls back
// to the default derivation, so labeling is per-root, not all-or-nothing.
func TestCallTool_ListNodesRootsUsesRootLabelOverride(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{},
		"faketest://h/45d37e8a-uuid/", "faketest://h/cal-b/")
	tools.rootLabels = map[string]string{
		"faketest://h/45d37e8a-uuid/": "My Personal Calendar",
	}

	res, err := tools.CallTool(context.Background(), "list_nodes", json.RawMessage(`{}`))
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
	byURI := map[string]nodeView{}
	for _, v := range views {
		byURI[v.URI] = v
	}
	if got := byURI["faketest://h/45d37e8a-uuid/"].Name; got != "My Personal Calendar" {
		t.Errorf("labeled root Name = %q, want the RootLabeler override %q",
			got, "My Personal Calendar")
	}
	if got := byURI["faketest://h/cal-b/"].Name; got != "cal-b" {
		t.Errorf("unlabeled root Name = %q, want the default derivation %q", got, "cal-b")
	}
}
