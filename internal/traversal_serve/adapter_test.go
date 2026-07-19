package traversal_serve

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// --- in-process session plumbing ------------------------------------

// countingConn wraps a stream counting Write calls. The peer writes
// exactly one line per Call/Notify, so the counter is the wire-traffic
// meter the decline-gating tests assert on.
type countingConn struct {
	io.ReadWriteCloser
	writes atomic.Int64
}

func (c *countingConn) Write(p []byte) (int, error) {
	c.writes.Add(1)
	return c.ReadWriteCloser.Write(p)
}

// inProcessDialer is the injected session-maker behind
// newWirePluginWithDialer: each dial serves cfg over a fresh net.Pipe
// (no subprocess), performs the initialize exchange exactly as Launch
// would, and hands back a Session wrapping the client peer. Dials and
// per-session write counters are recorded for assertions.
type inProcessDialer struct {
	cfg ServeConfig

	mu       sync.Mutex
	dials    int
	conns    []*countingConn
	sessions []*Session
}

func (d *inProcessDialer) dial(ctx context.Context) (*Session, error) {
	clientConn, serverConn := net.Pipe()

	go func() { _ = Serve(context.Background(), serverConn, d.cfg) }()

	conn := &countingConn{ReadWriteCloser: clientConn}
	peer := NewPeer(conn)

	var init InitializeResult
	err := peer.Call(ctx, MethodInitialize, InitializeParams{
		ProtocolVersions: []string{SchemaV1},
	}, &init)
	if err != nil {
		_ = peer.Close()
		return nil, err
	}

	sess := &Session{Init: init, peer: peer}

	d.mu.Lock()
	d.dials++
	d.conns = append(d.conns, conn)
	d.sessions = append(d.sessions, sess)
	d.mu.Unlock()

	return sess, nil
}

func (d *inProcessDialer) dialCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dials
}

// writeCount totals line writes across every session this dialer
// produced — including the initialize call each dial performs.
func (d *inProcessDialer) writeCount() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()

	var total int64
	for _, conn := range d.conns {
		total += conn.writes.Load()
	}
	return total
}

func (d *inProcessDialer) closeAll() {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, sess := range d.sessions {
		_ = sess.Close()
	}
}

func newTestWirePlugin(
	t *testing.T, spec PluginSpec, cfg ServeConfig,
) (*WirePlugin, *inProcessDialer) {
	t.Helper()

	dialer := &inProcessDialer{cfg: cfg}
	t.Cleanup(dialer.closeAll)

	return newWirePluginWithDialer(spec, dialer.dial), dialer
}

func memSpec() PluginSpec {
	return PluginSpec{
		Name:    "fake-mem",
		Command: []string{"unused-in-process"},
		Schemes: []string{"mem"},
	}
}

func minSpec() PluginSpec {
	return PluginSpec{
		Name:    "fake-min",
		Command: []string{"unused-in-process"},
		Schemes: []string{"min"},
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()

	uri, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return uri
}

// --- tests -----------------------------------------------------------

// TestWirePluginFullCapabilityRoundTrips drives every advertised
// capability through the adapter against the full fake plugin: the
// declarations from the cached initialize, every read method, and every
// mutation verb, all over one session (one dial).
func TestWirePluginFullCapabilityRoundTrips(t *testing.T) {
	plugin := &fakeFullPlugin{}
	adapter, dialer := newTestWirePlugin(t, memSpec(), fullPluginConfig(plugin))
	ctx := context.Background()

	if got := adapter.TypeTag(); got != "cutting_garden-capture_receipt-mem-v1" {
		t.Errorf("TypeTag = %q", got)
	}

	types := adapter.Types()
	if len(types) != 2 {
		t.Fatalf("Types = %+v, want 2 entries", types)
	}
	if !types[0].Container || types[0].Tag != fakeContainerType {
		t.Errorf("types[0] = %+v", types[0])
	}
	if types[0].MimeType != "" {
		t.Errorf("container mime = %q, want empty", types[0].MimeType)
	}
	if types[1].MimeType != "text/plain" {
		t.Errorf("leaf mime = %q, want text/plain", types[1].MimeType)
	}

	roots, err := adapter.Roots(ctx)
	if err != nil {
		t.Fatalf("Roots: %v", err)
	}
	if len(roots) != 1 || roots[0].String() != fakeRootURI {
		t.Errorf("Roots = %v, want [%s]", roots, fakeRootURI)
	}

	nodes, err := adapter.ListRoots(ctx, mustParseURL(t, fakeRootURI))
	if err != nil {
		t.Fatalf("ListRoots: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("ListRoots = %+v, want 2 nodes", nodes)
	}
	if nodes[0].URI.String() != fakeLeafA || nodes[0].Name != "a" ||
		nodes[0].Type != fakeLeafType {
		t.Errorf("nodes[0] = %+v", nodes[0])
	}
	if got := nodes[0].Facets["state"]; len(got) != 1 || got[0].Key != "open" {
		t.Errorf("nodes[0] state facet = %+v, want [open]", got)
	}
	if got := nodes[0].Facets["month"]; len(got) != 1 || got[0].Order != 202607 {
		t.Errorf("nodes[0] month facet = %+v, want order 202607", got)
	}

	content, ok, err := adapter.ReadLeaf(ctx, mustParseURL(t, fakeLeafA))
	if err != nil || !ok {
		t.Fatalf("ReadLeaf(a) = ok %t, err %v; want true, nil", ok, err)
	}
	structuredJSON, err := json.Marshal(content.Structured)
	if err != nil {
		t.Fatalf("re-marshal structured: %v", err)
	}
	var structured struct {
		Title string `json:"title"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(structuredJSON, &structured); err != nil {
		t.Fatalf("decode structured: %v", err)
	}
	if structured.Title != "a" || structured.State != "open" {
		t.Errorf("structured = %+v", structured)
	}
	if string(content.Raw) != "hello, mem" {
		t.Errorf("raw = %q, want %q", content.Raw, "hello, mem")
	}
	if content.RawMimeType != "text/plain" {
		t.Errorf("raw mime = %q, want text/plain", content.RawMimeType)
	}

	if _, ok, err := adapter.ReadLeaf(
		ctx, mustParseURL(t, fakeLeafB),
	); ok || err != nil {
		t.Errorf("ReadLeaf(b) = ok %t, err %v; want false, nil", ok, err)
	}

	facetResult, ok, err := adapter.FacetCounts(
		ctx, mustParseURL(t, fakeRootURI), nil,
	)
	if err != nil || !ok {
		t.Fatalf("FacetCounts(root) = ok %t, err %v; want true, nil", ok, err)
	}
	if !facetResult.Complete {
		t.Error("FacetCounts complete = false, want true")
	}
	if facetResult.Summary["state"]["open"] != 1 ||
		facetResult.Summary["state"]["closed"] != 1 {
		t.Errorf("summary = %+v", facetResult.Summary)
	}

	if _, ok, err := adapter.FacetCounts(
		ctx, mustParseURL(t, fakeLeafA), nil,
	); ok || err != nil {
		t.Errorf("FacetCounts(leaf) = ok %t, err %v; want false, nil", ok, err)
	}

	token, ok, err := adapter.FacetVersion(ctx, mustParseURL(t, fakeRootURI))
	if err != nil || !ok || token != "ctag-1" {
		t.Errorf("FacetVersion = %q, %t, %v; want ctag-1, true, nil",
			token, ok, err)
	}

	labels, err := adapter.ResolveFacetLabels(ctx, "state", []string{"open"})
	if err != nil {
		t.Fatalf("ResolveFacetLabels: %v", err)
	}
	if labels["open"] != "Label open" {
		t.Errorf("labels = %+v", labels)
	}

	facets := adapter.DescribeFacets()
	if len(facets) != 1 || facets[0].Tag != fakeLeafType {
		t.Fatalf("DescribeFacets = %+v, want one block for %s",
			facets, fakeLeafType)
	}
	if len(facets[0].Dimensions) != 2 {
		t.Fatalf("dimensions = %+v, want 2", facets[0].Dimensions)
	}
	if got := facets[0].Dimensions[0].Values; len(got) != 2 {
		t.Errorf("closed domain values = %+v, want 2", got)
	}

	bodies := adapter.DescribeBodies()
	if len(bodies) != 1 || bodies[0].Tag != fakeLeafType {
		t.Errorf("DescribeBodies = %+v, want one entry for %s",
			bodies, fakeLeafType)
	}

	newURI := mustParseURL(t, "mem://host/root/new")
	err = adapter.CreateNode(
		ctx, newURI, strings.NewReader("hello create"), fakeLeafType,
	)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	leafA := mustParseURL(t, fakeLeafA)
	if err := adapter.PutNode(
		ctx, leafA, strings.NewReader("full replacement"),
	); err != nil {
		t.Fatalf("PutNode: %v", err)
	}
	if err := adapter.PatchNode(
		ctx, leafA, strings.NewReader(`{"state":"closed"}`),
	); err != nil {
		t.Fatalf("PatchNode: %v", err)
	}
	if err := adapter.DeleteNode(ctx, leafA); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}

	plugin.mu.Lock()
	if plugin.createdURI != "mem://host/root/new" ||
		plugin.createdType != fakeLeafType ||
		string(plugin.createdBody) != "hello create" {
		t.Errorf("create recorded %q %q %q",
			plugin.createdURI, plugin.createdType, plugin.createdBody)
	}
	if string(plugin.putBody) != "full replacement" {
		t.Errorf("put body = %q", plugin.putBody)
	}
	if string(plugin.patchBody) != `{"state":"closed"}` {
		t.Errorf("patch body = %q", plugin.patchBody)
	}
	plugin.mu.Unlock()

	// A wire error passes through the adapter's wrap: the RPC code is
	// still extractable (server rejects an empty patch body).
	err = adapter.PatchNode(ctx, leafA, strings.NewReader(""))
	if code, ok := CodeOf(err); !ok || code != CodeInvalidParams {
		t.Errorf("empty patch: CodeOf = %d, %t, want %d, true",
			code, ok, CodeInvalidParams)
	}

	if got := dialer.dialCount(); got != 1 {
		t.Errorf("dials = %d, want 1 — the session must be reused", got)
	}
}

// TestWirePluginDeclineGatingSendsNoTraffic pins the decline paths:
// against a RootLister-only plugin, every optional read capability
// answers its contract's decline value with NO wire call — the only
// line ever written is the dial's initialize.
func TestWirePluginDeclineGatingSendsNoTraffic(t *testing.T) {
	adapter, dialer := newTestWirePlugin(t, minSpec(), minimalPluginConfig())
	ctx := context.Background()
	uri := mustParseURL(t, "min://host/x")

	roots, err := adapter.Roots(ctx)
	if roots != nil || err != nil {
		t.Errorf("Roots = %v, %v; want nil, nil", roots, err)
	}

	if _, ok, err := adapter.ReadLeaf(ctx, uri); ok || err != nil {
		t.Errorf("ReadLeaf = ok %t, err %v; want false, nil", ok, err)
	}

	if _, ok, err := adapter.FacetCounts(ctx, uri, nil); ok || err != nil {
		t.Errorf("FacetCounts = ok %t, err %v; want false, nil", ok, err)
	}

	if _, ok, err := adapter.FacetVersion(ctx, uri); ok || err != nil {
		t.Errorf("FacetVersion = ok %t, err %v; want false, nil", ok, err)
	}

	labels, err := adapter.ResolveFacetLabels(ctx, "state", []string{"x"})
	if labels != nil || err != nil {
		t.Errorf("ResolveFacetLabels = %v, %v; want nil, nil", labels, err)
	}

	if got := adapter.DescribeFacets(); got != nil {
		t.Errorf("DescribeFacets = %+v, want nil", got)
	}
	if got := adapter.DescribeBodies(); got != nil {
		t.Errorf("DescribeBodies = %+v, want nil", got)
	}

	if got := dialer.writeCount(); got != 1 {
		t.Errorf("wire writes = %d, want 1 (initialize only)", got)
	}
	if got := dialer.dialCount(); got != 1 {
		t.Errorf("dials = %d, want 1", got)
	}
}

// TestWirePluginMutationWithoutCapMutateErrors pins that a write on a
// plugin that did not advertise mutate is a REAL error naming the
// plugin and the missing capability — never a silent decline — and
// that no mutation call reaches the wire.
func TestWirePluginMutationWithoutCapMutateErrors(t *testing.T) {
	adapter, dialer := newTestWirePlugin(t, minSpec(), minimalPluginConfig())
	ctx := context.Background()
	uri := mustParseURL(t, "min://host/x")

	mutations := map[string]func() error{
		"create": func() error {
			return adapter.CreateNode(
				ctx, uri, strings.NewReader("x"), "min-obj-v1",
			)
		},
		"put": func() error {
			return adapter.PutNode(ctx, uri, strings.NewReader("x"))
		},
		"patch": func() error {
			return adapter.PatchNode(ctx, uri, strings.NewReader("x"))
		},
		"delete": func() error {
			return adapter.DeleteNode(ctx, uri)
		},
	}

	for name, mutate := range mutations {
		err := mutate()
		if err == nil {
			t.Errorf("%s: no error for an unadvertised mutation", name)
			continue
		}
		if !strings.Contains(err.Error(), "fake-min") ||
			!strings.Contains(err.Error(), CapMutate) {
			t.Errorf("%s: error %q does not name the plugin and the"+
				" missing capability", name, err)
		}
	}

	if got := dialer.writeCount(); got != 1 {
		t.Errorf("wire writes = %d, want 1 (initialize only)", got)
	}
}

// TestWirePluginSchemesEchoMismatchIsPersistent pins the misconfig
// path: a plugin whose initialize echo does not cover the configured
// schemes is rejected at first spawn, and EVERY subsequent operation
// fails fast on the recorded error — no respawn, a misconfiguration is
// not retryable.
func TestWirePluginSchemesEchoMismatchIsPersistent(t *testing.T) {
	spec := memSpec()
	spec.Schemes = []string{"other"}

	adapter, dialer := newTestWirePlugin(
		t, spec, fullPluginConfig(&fakeFullPlugin{}),
	)
	ctx := context.Background()
	uri := mustParseURL(t, "other://host/root")

	_, err := adapter.ListRoots(ctx, uri)
	if err == nil {
		t.Fatal("expected a schemes-echo mismatch error")
	}
	if !strings.Contains(err.Error(), "other") {
		t.Errorf("error %q does not name the missing scheme", err)
	}

	if _, err := adapter.Roots(ctx); err == nil {
		t.Error("Roots after mismatch: no error")
	}
	if _, _, err := adapter.FacetVersion(ctx, uri); err == nil {
		t.Error("FacetVersion after mismatch: no error")
	}
	if err := adapter.DeleteNode(ctx, uri); err == nil {
		t.Error("DeleteNode after mismatch: no error")
	}
	if got := adapter.Types(); got != nil {
		t.Errorf("Types after mismatch = %+v, want nil", got)
	}

	if got := dialer.dialCount(); got != 1 {
		t.Errorf("dials = %d, want 1 — a misconfig must not respawn", got)
	}
}

// TestWirePluginSpawnFailureIsPersistent pins the cutting-garden#165
// fault-isolation contract at the adapter's finest grain: a plugin that
// fails to spawn or complete its bring-up (a missing command, a child
// that exits before announcing, initialize erroring — all surfaced by
// Launch/dial as one opaque error) is rejected at first spawn, and
// EVERY subsequent operation fails fast on the SAME recorded error —
// no respawn attempt, exactly like the schemes-echo-mismatch
// misconfiguration path — so a plugin that crashed once cannot
// re-crash on every later touch. The declaration methods (which have
// no error channel) degrade to their zero value instead of erroring.
func TestWirePluginSpawnFailureIsPersistent(t *testing.T) {
	var dials atomic.Int64
	spawnErr := errors.New("exec: no such file or directory")

	adapter := newWirePluginWithDialer(
		minSpec(),
		func(context.Context) (*Session, error) {
			dials.Add(1)
			return nil, spawnErr
		},
	)
	ctx := context.Background()
	uri := mustParseURL(t, "min://host/root")

	_, err := adapter.ListRoots(ctx, uri)
	if err == nil {
		t.Fatal("expected a spawn-failure error")
	}
	if !strings.Contains(err.Error(), spawnErr.Error()) {
		t.Errorf("error %q does not wrap the spawn failure", err)
	}
	if !strings.Contains(err.Error(), "fake-min") {
		t.Errorf("error %q does not name the plugin", err)
	}

	if _, err := adapter.Roots(ctx); err == nil {
		t.Error("Roots after spawn failure: no error")
	}
	if _, _, err := adapter.FacetVersion(ctx, uri); err == nil {
		t.Error("FacetVersion after spawn failure: no error")
	}
	if err := adapter.DeleteNode(ctx, uri); err == nil {
		t.Error("DeleteNode after spawn failure: no error")
	}
	if got := adapter.Types(); got != nil {
		t.Errorf("Types after spawn failure = %+v, want nil", got)
	}
	if got := adapter.TypeTag(); got != "" {
		t.Errorf("TypeTag after spawn failure = %q, want empty", got)
	}
	if got := adapter.DescribeFacets(); got != nil {
		t.Errorf("DescribeFacets after spawn failure = %+v, want nil", got)
	}

	if got := dials.Load(); got != 1 {
		t.Errorf("dials = %d, want 1 — a dead plugin must not respawn"+
			" (would re-crash on every touch)", got)
	}
}

// TestWirePluginRegistrationIsLazyAndOffline pins that construction and
// Schemes() — everything scheme registration needs — spawn nothing.
func TestWirePluginRegistrationIsLazyAndOffline(t *testing.T) {
	adapter, dialer := newTestWirePlugin(
		t, memSpec(), fullPluginConfig(&fakeFullPlugin{}),
	)

	if got := adapter.Schemes(); !slices.Equal(got, []string{"mem"}) {
		t.Errorf("Schemes = %v, want [mem]", got)
	}

	if got := dialer.dialCount(); got != 0 {
		t.Errorf("dials = %d, want 0 — registration must be offline", got)
	}
}

// TestWirePluginRespawnsOnceAfterSessionDeath pins the RFC 0013
// §Session lifecycle respawn allowance: a session found dead when an
// operation needs it is replaced (once), and the operation succeeds on
// the fresh session.
func TestWirePluginRespawnsOnceAfterSessionDeath(t *testing.T) {
	adapter, dialer := newTestWirePlugin(
		t, memSpec(), fullPluginConfig(&fakeFullPlugin{}),
	)
	ctx := context.Background()
	root := mustParseURL(t, fakeRootURI)

	if _, err := adapter.ListRoots(ctx, root); err != nil {
		t.Fatalf("first ListRoots: %v", err)
	}
	if got := dialer.dialCount(); got != 1 {
		t.Fatalf("dials = %d, want 1", got)
	}

	// Kill the live session out from under the adapter.
	dialer.mu.Lock()
	sess := dialer.sessions[0]
	dialer.mu.Unlock()
	_ = sess.Close()

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("closed session did not report death")
	}

	nodes, err := adapter.ListRoots(ctx, root)
	if err != nil {
		t.Fatalf("post-death ListRoots: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("post-death ListRoots = %+v, want 2 nodes", nodes)
	}

	if got := dialer.dialCount(); got != 2 {
		t.Errorf("dials = %d, want 2 — exactly one respawn", got)
	}
}

// TestWirePluginDeclarationsServedFromCachedInit pins that the
// declaration methods (Types/TypeTag/DescribeFacets/DescribeBodies)
// answer from the cached initialize result: after the first spawn, no
// further wire traffic.
func TestWirePluginDeclarationsServedFromCachedInit(t *testing.T) {
	adapter, dialer := newTestWirePlugin(
		t, memSpec(), fullPluginConfig(&fakeFullPlugin{}),
	)

	if types := adapter.Types(); len(types) != 2 {
		t.Fatalf("Types = %+v, want 2 entries", types)
	}

	after := dialer.writeCount()
	if after != 1 {
		t.Fatalf("writes after first Types = %d, want 1 (initialize)", after)
	}

	_ = adapter.TypeTag()
	_ = adapter.Types()
	_ = adapter.DescribeFacets()
	_ = adapter.DescribeBodies()

	if got := dialer.writeCount(); got != after {
		t.Errorf("declaration reads added wire traffic: %d -> %d writes",
			after, got)
	}
	if got := dialer.dialCount(); got != 1 {
		t.Errorf("dials = %d, want 1", got)
	}
}

// TestWirePluginCreateChild pins node.create_child (cutting-garden#143):
// the created URI the peer assigned comes back through the adapter, and
// a plugin that does not advertise container-create refuses the write
// with an error naming the capability (never a silent decline).
func TestWirePluginCreateChild(t *testing.T) {
	plugin := &fakeFullPlugin{}
	adapter, _ := newTestWirePlugin(
		t, memSpec(), fullPluginConfig(plugin),
	)

	created, err := adapter.CreateChild(
		context.Background(), mustParseURL(t, fakeRootURI),
		strings.NewReader("hello child"), "mem-obj-v1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if created.String() != fakeRootURI+"/assigned-1" {
		t.Errorf("created = %q, want %q/assigned-1", created, fakeRootURI)
	}

	plugin.mu.Lock()
	gotBody := string(plugin.createdBody)
	plugin.mu.Unlock()
	if gotBody != "hello child" {
		t.Errorf("create_child body = %q", gotBody)
	}

	minimal, _ := newTestWirePlugin(
		t, minSpec(), minimalPluginConfig(),
	)
	_, err = minimal.CreateChild(
		context.Background(), mustParseURL(t, "min://host/root"),
		strings.NewReader("x"), "min-obj-v1",
	)
	if err == nil ||
		!strings.Contains(err.Error(), CapContainerCreate) {
		t.Errorf("unadvertised create_child: err = %v, want capability"+
			" refusal", err)
	}
}

// credentialLeakPlugin emits a child URI carrying userinfo — the
// invariant violation the adapter must reject rather than surface
// (RFC 0007/0013 §Security: enforce on plugin output, don't trust).
type credentialLeakPlugin struct{}

func (credentialLeakPlugin) Schemes() []string { return []string{"leak"} }

func (credentialLeakPlugin) TypeTag() string {
	return "cutting_garden-capture_receipt-leak-v1"
}

func (credentialLeakPlugin) Types() []cutting_garden_plugins.NodeType {
	return []cutting_garden_plugins.NodeType{{Tag: "leak-obj-v1"}}
}

func (credentialLeakPlugin) ListRoots(
	_ context.Context, _ *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	uri, err := url.Parse("leak://user:hunter2@host/x")
	if err != nil {
		return nil, err
	}

	return []cutting_garden_plugins.Node{
		{URI: uri, Name: "x", Type: "leak-obj-v1"},
	}, nil
}

// TestWirePluginListRootsRejectsCredentialedChildURI pins the host-side
// §Security enforcement on nodes.list output: a userinfo-bearing child
// URI is an error, not a passthrough, and the password never appears in
// the message.
func TestWirePluginListRootsRejectsCredentialedChildURI(t *testing.T) {
	spec := PluginSpec{
		Name:    "fake-leak",
		Command: []string{"unused-in-process"},
		Schemes: []string{"leak"},
	}
	adapter, _ := newTestWirePlugin(t, spec, ServeConfig{
		Plugin: credentialLeakPlugin{},
		Info:   PluginInfo{Name: "fake-leak", Version: "0"},
	})

	_, err := adapter.ListRoots(
		context.Background(), mustParseURL(t, "leak://host/"),
	)
	if err == nil {
		t.Fatal("expected a credential-free violation error")
	}
	if !strings.Contains(err.Error(), "credential-free") {
		t.Errorf("error %q does not cite the invariant", err)
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("error %q leaks the credential", err)
	}
}
