package traversal_serve

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// The fake full plugin's in-memory tree: one container with two leaves,
// facets on the leaves.
const (
	fakeRootURI = "mem://host/root"
	fakeLeafA   = "mem://host/root/a"
	fakeLeafB   = "mem://host/root/b"

	fakeContainerType = "mem-dir-v1"
	fakeLeafType      = "mem-obj-v1"
)

// fakeFullPlugin implements EVERY optional capability over a tiny
// in-memory tree, and records mutations for assertion.
type fakeFullPlugin struct {
	mu          sync.Mutex
	createdURI  string
	createdType string
	createdBody []byte
	putBody     []byte
	patchBody   []byte
	// patchApplied is what PatchNode reports; nil (the zero value) is the
	// "does not report applied fields" case (cutting-garden#182).
	patchApplied []string
}

var (
	_ cutting_garden_plugins.RootProvider   = (*fakeFullPlugin)(nil)
	_ cutting_garden_plugins.LeafReader     = (*fakeFullPlugin)(nil)
	_ cutting_garden_plugins.FacetDescriber = (*fakeFullPlugin)(nil)
	_ cutting_garden_plugins.FacetCounter   = (*fakeFullPlugin)(nil)
	_ cutting_garden_plugins.FacetVersioner = (*fakeFullPlugin)(nil)
	_ cutting_garden_plugins.FacetLabeler   = (*fakeFullPlugin)(nil)
	_ cutting_garden_plugins.NodeMutator    = (*fakeFullPlugin)(nil)
	_ cutting_garden_plugins.BodyDescriber  = (*fakeFullPlugin)(nil)
)

func (p *fakeFullPlugin) Schemes() []string { return []string{"mem"} }

func (p *fakeFullPlugin) TypeTag() string {
	return "cutting_garden-capture_receipt-mem-v1"
}

func (p *fakeFullPlugin) Types() []cutting_garden_plugins.NodeType {
	return []cutting_garden_plugins.NodeType{
		{Tag: fakeContainerType, Container: true},
		{Tag: fakeLeafType, Container: false, MimeType: "text/plain"},
	}
}

func (p *fakeFullPlugin) ListRoots(
	_ context.Context, node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	if node.String() != fakeRootURI {
		return nil, nil
	}

	leafA, _ := url.Parse(fakeLeafA)
	leafB, _ := url.Parse(fakeLeafB)

	return []cutting_garden_plugins.Node{
		{
			URI:  leafA,
			Name: "a",
			Type: fakeLeafType,
			Facets: map[string][]cutting_garden_plugins.FacetValue{
				"state": {{Key: "open"}},
				"month": {{Key: "2026-07", Order: 202607}},
			},
		},
		{
			URI:  leafB,
			Name: "b",
			Type: fakeLeafType,
			Facets: map[string][]cutting_garden_plugins.FacetValue{
				"state": {{Key: "closed"}},
			},
		},
	}, nil
}

func (p *fakeFullPlugin) Roots(
	_ context.Context,
) ([]*url.URL, error) {
	root, _ := url.Parse(fakeRootURI)
	return []*url.URL{root}, nil
}

func (p *fakeFullPlugin) ReadLeaf(
	_ context.Context, node *url.URL,
) (cutting_garden_plugins.LeafContent, bool, error) {
	if node.String() != fakeLeafA {
		return cutting_garden_plugins.LeafContent{}, false, nil
	}

	return cutting_garden_plugins.LeafContent{
		Structured:  map[string]any{"title": "a", "state": "open"},
		Raw:         []byte("hello, mem"),
		RawMimeType: "text/plain",
	}, true, nil
}

func (p *fakeFullPlugin) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	return []cutting_garden_plugins.NodeTypeFacets{
		{
			Tag: fakeLeafType,
			Dimensions: []cutting_garden_plugins.FacetDimension{
				{
					Key:   "state",
					Label: "State",
					Kind:  cutting_garden_plugins.FacetCategorical,
					Values: []cutting_garden_plugins.FacetValue{
						{Key: "open"}, {Key: "closed"},
					},
				},
				{
					Key:  "month",
					Kind: cutting_garden_plugins.FacetNumericBucket,
				},
			},
		},
	}
}

func (p *fakeFullPlugin) FacetCounts(
	_ context.Context, node *url.URL, _ cutting_garden_plugins.FacetFilter,
) (cutting_garden_plugins.FacetResult, bool, error) {
	if node.String() != fakeRootURI {
		return cutting_garden_plugins.FacetResult{}, false, nil
	}

	return cutting_garden_plugins.FacetResult{
		Summary: cutting_garden_plugins.FacetSummary{
			"state": {"open": 1, "closed": 1},
		},
		Complete: true,
	}, true, nil
}

func (p *fakeFullPlugin) FacetVersion(
	_ context.Context, _ *url.URL,
) (string, bool, error) {
	return "ctag-1", true, nil
}

func (p *fakeFullPlugin) ResolveFacetLabels(
	_ context.Context, _ string, keys []string,
) (map[string]string, error) {
	labels := make(map[string]string, len(keys))
	for _, key := range keys {
		labels[key] = "Label " + key
	}
	return labels, nil
}

func (p *fakeFullPlugin) CreateNode(
	_ context.Context, uri *url.URL, body io.Reader, typ string,
) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.createdURI = uri.String()
	p.createdType = typ
	p.createdBody = data

	return nil
}

func (p *fakeFullPlugin) CreateChild(
	_ context.Context, container *url.URL, body io.Reader, typ string,
) (*url.URL, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.createdType = typ
	p.createdBody = data
	p.createdURI = container.String() + "/assigned-1"

	return url.Parse(p.createdURI)
}

func (p *fakeFullPlugin) PutNode(
	_ context.Context, _ *url.URL, body io.Reader,
) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.putBody = data

	return nil
}

func (p *fakeFullPlugin) PatchNode(
	_ context.Context, _ *url.URL, body io.Reader,
) ([]string, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.patchBody = data

	return p.patchApplied, nil
}

func (p *fakeFullPlugin) DeleteNode(
	_ context.Context, _ *url.URL,
) error {
	return nil
}

func (p *fakeFullPlugin) DescribeBodies() []cutting_garden_plugins.NodeTypeBody {
	return []cutting_garden_plugins.NodeTypeBody{
		{
			Tag:     fakeLeafType,
			Accepts: []string{"text/plain (the object body)"},
			Example: "hello",
		},
	}
}

// fakeMinimalPlugin implements ONLY RootLister — the mandatory floor of
// RFC 0013 §Method set. It must advertise no optional capabilities.
type fakeMinimalPlugin struct{}

var _ cutting_garden_plugins.RootLister = fakeMinimalPlugin{}

func (fakeMinimalPlugin) Schemes() []string { return []string{"min"} }

func (fakeMinimalPlugin) TypeTag() string {
	return "cutting_garden-capture_receipt-min-v1"
}

func (fakeMinimalPlugin) Types() []cutting_garden_plugins.NodeType {
	return []cutting_garden_plugins.NodeType{
		{Tag: "min-obj-v1", Container: false},
	}
}

func (fakeMinimalPlugin) ListRoots(
	_ context.Context, _ *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	return nil, nil
}

// serveResult joins Serve's exit exactly once, so both a test body and
// the harness cleanup can consult it.
type serveResult struct {
	once sync.Once
	err  error
	ch   chan error
}

func (r *serveResult) wait(t *testing.T) error {
	t.Helper()

	r.once.Do(func() {
		select {
		case r.err = <-r.ch:
		case <-time.After(5 * time.Second):
			t.Errorf("Serve did not return within 5s")
			r.err = errors.ErrorWithStackf("serve result timeout")
		}
	})

	return r.err
}

// startServe runs Serve over one end of a net.Pipe and hands back a
// handler-less client Peer (the host role) driving the other end.
func startServe(
	t *testing.T, cfg ServeConfig,
) (*Peer, *serveResult) {
	t.Helper()

	clientConn, serverConn := net.Pipe()

	result := &serveResult{ch: make(chan error, 1)}
	go func() {
		result.ch <- Serve(context.Background(), serverConn, cfg)
	}()

	client := NewPeer(clientConn)

	t.Cleanup(func() {
		_ = client.Close()
		_ = result.wait(t)
	})

	return client, result
}

func fullPluginConfig(plugin *fakeFullPlugin) ServeConfig {
	return ServeConfig{
		Plugin: plugin,
		Info:   PluginInfo{Name: "fake-mem", Version: "0.0.1"},
	}
}

func minimalPluginConfig() ServeConfig {
	return ServeConfig{
		Plugin: fakeMinimalPlugin{},
		Info:   PluginInfo{Name: "fake-min", Version: "0.0.1"},
	}
}

func mustInitialize(t *testing.T, client *Peer) InitializeResult {
	t.Helper()

	var result InitializeResult
	err := client.Call(
		context.Background(),
		MethodInitialize,
		InitializeParams{ProtocolVersions: []string{SchemaV1}},
		&result,
	)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}

	return result
}

func TestServeInitializeFullPluginDeclaration(t *testing.T) {
	client, _ := startServe(t, fullPluginConfig(&fakeFullPlugin{}))

	result := mustInitialize(t, client)

	if result.Schema != SchemaV1 {
		t.Errorf("schema = %q, want %q", result.Schema, SchemaV1)
	}

	if result.Plugin.Name != "fake-mem" {
		t.Errorf("plugin name = %q, want %q", result.Plugin.Name, "fake-mem")
	}

	if !slices.Equal(result.Schemes, []string{"mem"}) {
		t.Errorf("schemes = %v, want [mem]", result.Schemes)
	}

	if result.TypeTag != "cutting_garden-capture_receipt-mem-v1" {
		t.Errorf("type_tag = %q", result.TypeTag)
	}

	wantCaps := []string{
		CapRoots, CapLeafRead, CapFacetCounts,
		CapFacetVersion, CapFacetLabels, CapMutate, CapContainerCreate,
	}
	gotCaps := slices.Clone(result.Capabilities)
	slices.Sort(gotCaps)
	slices.Sort(wantCaps)
	if !slices.Equal(gotCaps, wantCaps) {
		t.Errorf("capabilities = %v, want %v", result.Capabilities, wantCaps)
	}

	if len(result.NodeTypes) != 2 {
		t.Fatalf("node_types = %v, want 2 entries", result.NodeTypes)
	}

	if !result.NodeTypes[0].Container || result.NodeTypes[0].Tag != fakeContainerType {
		t.Errorf("node_types[0] = %+v", result.NodeTypes[0])
	}

	if result.NodeTypes[1].MimeType != "text/plain" {
		t.Errorf("node_types[1].mime_type = %q, want text/plain",
			result.NodeTypes[1].MimeType)
	}

	if len(result.Facets) != 1 || result.Facets[0].Tag != fakeLeafType {
		t.Errorf("facets = %+v, want one block for %s", result.Facets, fakeLeafType)
	}

	if len(result.Bodies) != 1 || result.Bodies[0].Tag != fakeLeafType {
		t.Errorf("bodies = %+v, want one entry for %s", result.Bodies, fakeLeafType)
	}
}

func TestServeInitializeMinimalPluginDeclaration(t *testing.T) {
	client, _ := startServe(t, minimalPluginConfig())

	result := mustInitialize(t, client)

	if len(result.Capabilities) != 0 {
		t.Errorf("capabilities = %v, want none", result.Capabilities)
	}

	if result.Facets != nil {
		t.Errorf("facets = %+v, want absent", result.Facets)
	}

	if result.Bodies != nil {
		t.Errorf("bodies = %+v, want absent", result.Bodies)
	}

	if len(result.NodeTypes) != 1 {
		t.Errorf("node_types = %+v, want 1 entry", result.NodeTypes)
	}
}

func TestServeInitializeVersionMismatch(t *testing.T) {
	client, _ := startServe(t, minimalPluginConfig())

	err := client.Call(
		context.Background(),
		MethodInitialize,
		InitializeParams{ProtocolVersions: []string{"traversal-plugin/v99"}},
		nil,
	)
	if err == nil {
		t.Fatal("expected an error")
	}

	if code, ok := CodeOf(err); !ok || code != CodeUnsupportedVersion {
		t.Errorf("CodeOf = %d, %t, want %d, true",
			code, ok, CodeUnsupportedVersion)
	}
}

func TestServeInitializeConfigApplyError(t *testing.T) {
	cfg := minimalPluginConfig()
	cfg.ConfigApply = func(string) error {
		return errors.ErrorWithStackf("bad section")
	}

	client, _ := startServe(t, cfg)

	err := client.Call(
		context.Background(),
		MethodInitialize,
		InitializeParams{ProtocolVersions: []string{SchemaV1}},
		nil,
	)
	if err == nil {
		t.Fatal("expected an error")
	}

	if code, ok := CodeOf(err); !ok || code != CodeInvalidConfig {
		t.Errorf("CodeOf = %d, %t, want %d, true", code, ok, CodeInvalidConfig)
	}

	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		if rpcErr.Message == "" {
			t.Error("invalid-config message is empty")
		}
	}
}

func TestServeRejectsRequestBeforeInitialize(t *testing.T) {
	client, _ := startServe(t, minimalPluginConfig())

	err := client.Call(
		context.Background(),
		MethodNodesList,
		NodesListParams{URI: "min://host/root"},
		nil,
	)
	if err == nil {
		t.Fatal("expected an error")
	}

	if code, ok := CodeOf(err); !ok || code != CodeInvalidParams {
		t.Errorf("CodeOf = %d, %t, want %d, true", code, ok, CodeInvalidParams)
	}
}

func TestServeNodesListRoundTrip(t *testing.T) {
	client, _ := startServe(t, fullPluginConfig(&fakeFullPlugin{}))
	mustInitialize(t, client)

	var result NodesListResult
	err := client.Call(
		context.Background(),
		MethodNodesList,
		NodesListParams{URI: fakeRootURI},
		&result,
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Nodes) != 2 {
		t.Fatalf("nodes = %+v, want 2", result.Nodes)
	}

	nodeA := result.Nodes[0]
	if nodeA.URI != fakeLeafA || nodeA.Name != "a" || nodeA.Type != fakeLeafType {
		t.Errorf("nodes[0] = %+v", nodeA)
	}

	if got := nodeA.Facets["state"]; len(got) != 1 || got[0].Key != "open" {
		t.Errorf("nodes[0] state facet = %+v, want [open]", got)
	}

	if got := nodeA.Facets["month"]; len(got) != 1 || got[0].Order != 202607 {
		t.Errorf("nodes[0] month facet = %+v, want order 202607", got)
	}

	if got := result.Nodes[1].Facets["state"]; len(got) != 1 || got[0].Key != "closed" {
		t.Errorf("nodes[1] state facet = %+v, want [closed]", got)
	}
}

func TestServeNodesListRejectsForeignScheme(t *testing.T) {
	client, _ := startServe(t, fullPluginConfig(&fakeFullPlugin{}))
	mustInitialize(t, client)

	for _, uri := range []string{"other://host/x", ""} {
		err := client.Call(
			context.Background(),
			MethodNodesList,
			NodesListParams{URI: uri},
			nil,
		)
		if err == nil {
			t.Fatalf("uri %q: expected an error", uri)
		}

		if code, ok := CodeOf(err); !ok || code != CodeInvalidParams {
			t.Errorf("uri %q: CodeOf = %d, %t, want %d, true",
				uri, code, ok, CodeInvalidParams)
		}
	}
}

func TestServeLeafRead(t *testing.T) {
	client, _ := startServe(t, fullPluginConfig(&fakeFullPlugin{}))
	mustInitialize(t, client)

	var okResult LeafReadResult
	err := client.Call(
		context.Background(),
		MethodLeafRead,
		LeafReadParams{URI: fakeLeafA},
		&okResult,
	)
	if err != nil {
		t.Fatal(err)
	}

	if !okResult.OK {
		t.Fatal("leaf.read ok = false, want true")
	}

	var structured struct {
		Title string `json:"title"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(okResult.Structured, &structured); err != nil {
		t.Fatalf("decode structured: %v", err)
	}
	if structured.Title != "a" || structured.State != "open" {
		t.Errorf("structured = %+v", structured)
	}

	raw, err := base64.StdEncoding.DecodeString(okResult.RawBase64)
	if err != nil {
		t.Fatalf("decode raw_base64: %v", err)
	}
	if string(raw) != "hello, mem" {
		t.Errorf("raw = %q, want %q", raw, "hello, mem")
	}

	if okResult.RawMimeType != "text/plain" {
		t.Errorf("raw_mime_type = %q, want text/plain", okResult.RawMimeType)
	}

	var declineResult LeafReadResult
	err = client.Call(
		context.Background(),
		MethodLeafRead,
		LeafReadParams{URI: fakeLeafB},
		&declineResult,
	)
	if err != nil {
		t.Fatal(err)
	}

	if declineResult.OK {
		t.Error("leaf.read ok = true for a non-fetchable leaf, want false")
	}

	if declineResult.Structured != nil || declineResult.RawBase64 != "" {
		t.Errorf("ok=false result carries content: %+v", declineResult)
	}
}

func TestServeFacetCounts(t *testing.T) {
	client, _ := startServe(t, fullPluginConfig(&fakeFullPlugin{}))
	mustInitialize(t, client)

	var okResult FacetCountsResult
	err := client.Call(
		context.Background(),
		MethodFacetCounts,
		FacetCountsParams{URI: fakeRootURI},
		&okResult,
	)
	if err != nil {
		t.Fatal(err)
	}

	if !okResult.OK || !okResult.Complete {
		t.Errorf("ok = %t, complete = %t, want true, true",
			okResult.OK, okResult.Complete)
	}

	if okResult.Summary["state"]["open"] != 1 ||
		okResult.Summary["state"]["closed"] != 1 {
		t.Errorf("summary = %+v", okResult.Summary)
	}

	var declineResult FacetCountsResult
	err = client.Call(
		context.Background(),
		MethodFacetCounts,
		FacetCountsParams{URI: fakeLeafA},
		&declineResult,
	)
	if err != nil {
		t.Fatal(err)
	}

	if declineResult.OK {
		t.Error("facets.counts ok = true for an unsummarized node, want false")
	}

	if declineResult.Summary != nil {
		t.Errorf("ok=false result carries a summary: %+v", declineResult.Summary)
	}
}

func TestServeUnadvertisedMethodFailsMethodNotFound(t *testing.T) {
	client, _ := startServe(t, minimalPluginConfig())
	mustInitialize(t, client)

	err := client.Call(
		context.Background(),
		MethodFacetCounts,
		FacetCountsParams{URI: "min://host/root"},
		nil,
	)
	if err == nil {
		t.Fatal("expected an error")
	}

	if code, ok := CodeOf(err); !ok || code != CodeMethodNotFound {
		t.Errorf("CodeOf = %d, %t, want %d, true",
			code, ok, CodeMethodNotFound)
	}
}

func TestServeNodeCreateDecodesBody(t *testing.T) {
	plugin := &fakeFullPlugin{}
	client, _ := startServe(t, fullPluginConfig(plugin))
	mustInitialize(t, client)

	body := []byte("hello create")
	err := client.Call(
		context.Background(),
		MethodNodeCreate,
		NodeCreateParams{
			URI:        "mem://host/root/new",
			Type:       fakeLeafType,
			BodyBase64: base64.StdEncoding.EncodeToString(body),
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	plugin.mu.Lock()
	defer plugin.mu.Unlock()

	if plugin.createdURI != "mem://host/root/new" {
		t.Errorf("created uri = %q", plugin.createdURI)
	}

	if plugin.createdType != fakeLeafType {
		t.Errorf("created type = %q, want %q", plugin.createdType, fakeLeafType)
	}

	if string(plugin.createdBody) != string(body) {
		t.Errorf("created body = %q, want %q", plugin.createdBody, body)
	}
}

// TestServeNodePutAndPatch pins the two remaining mutation verbs: put
// full-replaces via PutNode, patch partial-updates via PatchNode, and
// an empty patch body is rejected as CodeInvalidParams before the
// plugin is consulted (the NodeMutator empty-body contract).
func TestServeNodePutAndPatch(t *testing.T) {
	plugin := &fakeFullPlugin{}
	client, _ := startServe(t, fullPluginConfig(plugin))
	mustInitialize(t, client)

	putBody := []byte("full replacement")
	if err := client.Call(
		context.Background(),
		MethodNodePut,
		NodePutParams{
			URI:        fakeLeafA,
			BodyBase64: base64.StdEncoding.EncodeToString(putBody),
		},
		nil,
	); err != nil {
		t.Fatal(err)
	}

	patchBody := []byte(`{"state":"closed"}`)
	if err := client.Call(
		context.Background(),
		MethodNodePatch,
		NodePatchParams{
			URI:        fakeLeafA,
			BodyBase64: base64.StdEncoding.EncodeToString(patchBody),
		},
		nil,
	); err != nil {
		t.Fatal(err)
	}

	err := client.Call(
		context.Background(),
		MethodNodePatch,
		NodePatchParams{URI: fakeLeafA},
		nil,
	)
	if code, ok := CodeOf(err); !ok || code != CodeInvalidParams {
		t.Errorf("empty patch body: CodeOf = %d, %t, want %d, true",
			code, ok, CodeInvalidParams)
	}

	plugin.mu.Lock()
	defer plugin.mu.Unlock()

	if string(plugin.putBody) != string(putBody) {
		t.Errorf("put body = %q, want %q", plugin.putBody, putBody)
	}

	if string(plugin.patchBody) != string(patchBody) {
		t.Errorf("patch body = %q, want %q", plugin.patchBody, patchBody)
	}
}

// TestServeNodePatchAppliedRoundTrip pins that node.patch's result keeps
// "did not report" DISTINCT from "reported nothing applied"
// (cutting-garden#182). The distinction only survives if the key is omitted
// rather than serialized as an empty list, so this asserts on the raw JSON:
// a []string result field would make the first two cases identical on the
// wire and quietly report every pre-#182 peer's successful patch as a no-op.
func TestServeNodePatchAppliedRoundTrip(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		applied []string
		wantRaw string
	}{
		{name: "does not report", applied: nil, wantRaw: `{}`},
		{name: "reports nothing applied", applied: []string{}, wantRaw: `{"applied":[]}`},
		{
			name:    "reports applied fields",
			applied: []string{"state"},
			wantRaw: `{"applied":["state"]}`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			plugin := &fakeFullPlugin{patchApplied: testCase.applied}
			client, _ := startServe(t, fullPluginConfig(plugin))
			mustInitialize(t, client)

			var raw json.RawMessage
			if err := client.Call(
				context.Background(),
				MethodNodePatch,
				NodePatchParams{
					URI: fakeLeafA,
					BodyBase64: base64.StdEncoding.EncodeToString(
						[]byte(`{"state":"closed"}`),
					),
				},
				&raw,
			); err != nil {
				t.Fatal(err)
			}

			if string(raw) != testCase.wantRaw {
				t.Errorf("node.patch result = %s, want %s", raw, testCase.wantRaw)
			}
		})
	}
}

func TestServeShutdownNotificationReturnsNil(t *testing.T) {
	client, result := startServe(t, fullPluginConfig(&fakeFullPlugin{}))
	mustInitialize(t, client)

	if err := client.Notify(MethodShutdown, struct{}{}); err != nil {
		t.Fatal(err)
	}

	if err := result.wait(t); err != nil {
		t.Errorf("Serve = %v, want nil after shutdown", err)
	}

	_ = client.Close()
}

func TestServeNonRootListerPluginRefused(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()

	err := Serve(context.Background(), serverConn, ServeConfig{
		Plugin: bareIdentityPlugin{},
		Info:   PluginInfo{Name: "bare", Version: "0.0.1"},
	})
	if err == nil {
		t.Fatal("Serve accepted a plugin without RootLister")
	}
}

// bareIdentityPlugin implements Plugin but NOT RootLister — Serve must
// refuse it before serving (nodes.list is mandatory).
type bareIdentityPlugin struct{}

func (bareIdentityPlugin) Schemes() []string { return []string{"bare"} }

func (bareIdentityPlugin) TypeTag() string {
	return "cutting_garden-capture_receipt-bare-v1"
}
