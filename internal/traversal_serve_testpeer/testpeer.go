// Package traversal_serve_testpeer is the cg-owned RFC 0013 test
// plugin: a deterministic in-memory traversal peer serving a fixed
// "cgtest" tree with EVERY capability advertised, so the host side is
// testable with no real backend. It backs three consumers: the
// indistinguishability end-to-end in this package (the RFC 0013
// §Conformance bar), the packaged cutting-garden-test-traversal-serve
// binary the bats lane runs, and — because every input is pinned — any
// cross-implementation suite (a non-Go peer substitutes its own binary
// and must be indistinguishable over the same tree).
package traversal_serve_testpeer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/traversal_serve"
)

// The fixed tree's identity: one scheme, two node types, two containers
// and three leaves. Facet dimensions span every RFC 0012 kind —
// categorical with a CLOSED domain (state), numeric-bucket with Order
// (month), multi-valued categorical (tag), and labelled (feed).
const (
	Scheme  = "cgtest"
	TypeTag = "cutting_garden-capture_receipt-cgtest-v1"

	ContainerType = "cgtest-box-v1"
	LeafType      = "cgtest-obj-v1"
	LeafMimeType  = "text/plain"

	RootBox   = "cgtest://fixture/box"
	NestedBox = "cgtest://fixture/box/nested"
	LeafAlpha = "cgtest://fixture/box/alpha"
	LeafBeta  = "cgtest://fixture/box/beta"
	LeafGamma = "cgtest://fixture/box/nested/gamma"
)

// facetLabels is the fixed labelled-dimension resolution map behind
// ResolveFacetLabels: only the "feed" dimension resolves, and only
// these keys.
var facetLabels = map[string]map[string]string{
	"feed": {
		"f1": "Feed One",
		"f2": "Feed Two",
	},
}

// memNode is one node of the in-memory tree.
type memNode struct {
	name string
	typ  string

	// facets is the node's facet membership (leaves only in the fixed
	// tree); nil contributes nothing.
	facets map[string][]cutting_garden_plugins.FacetValue

	// Leaf content: the structured JSON projection, the verbatim raw
	// bytes, and their mime type.
	structured map[string]any
	raw        []byte
	rawMime    string

	// children is the ordered child-URI list; non-nil exactly for
	// containers.
	children []string
}

func (n *memNode) container() bool { return n.typ == ContainerType }

// TreePlugin is the deterministic in-memory RFC 0013 test plugin: the
// full capability surface over the fixed cgtest tree, with mutations
// applied to the in-memory state so a wire session can observe its own
// writes. Two fresh instances are indistinguishable until mutated.
type TreePlugin struct {
	mu    sync.Mutex
	nodes map[string]*memNode

	// generation counts applied mutations; FacetVersion derives its
	// token from it, so the token is deterministic on a fresh tree and
	// changes on every write.
	generation int64

	// configTOML records what ApplyConfigTOML received, for tests
	// asserting the RFC 0007 §Plugin-Owned Sections passthrough.
	configTOML    string
	configApplied bool
}

var (
	_ cutting_garden_plugins.RootProvider   = (*TreePlugin)(nil)
	_ cutting_garden_plugins.LeafReader     = (*TreePlugin)(nil)
	_ cutting_garden_plugins.FacetDescriber = (*TreePlugin)(nil)
	_ cutting_garden_plugins.FacetCounter   = (*TreePlugin)(nil)
	_ cutting_garden_plugins.FacetVersioner = (*TreePlugin)(nil)
	_ cutting_garden_plugins.FacetLabeler   = (*TreePlugin)(nil)
	_ cutting_garden_plugins.NodeMutator    = (*TreePlugin)(nil)
	_ cutting_garden_plugins.BodyDescriber  = (*TreePlugin)(nil)
)

// Plugin is the shared linked-path instance of the fixed tree — what an
// in-process consumer registers or compares against. Tests that mutate
// should use their own NewPlugin() (a served subprocess always does,
// via Config).
var Plugin = NewPlugin()

// NewPlugin builds a fresh instance of the fixed tree.
func NewPlugin() *TreePlugin {
	leaf := func(
		name, state, month string, monthOrder int64,
		tags []string, feed, body string,
	) *memNode {
		tagValues := make(
			[]cutting_garden_plugins.FacetValue, len(tags),
		)
		for i, tag := range tags {
			tagValues[i] = cutting_garden_plugins.FacetValue{Key: tag}
		}

		return &memNode{
			name: name,
			typ:  LeafType,
			facets: map[string][]cutting_garden_plugins.FacetValue{
				"state": {{Key: state}},
				"month": {{Key: month, Order: monthOrder}},
				"tag":   tagValues,
				"feed":  {{Key: feed}},
			},
			structured: map[string]any{"title": name, "state": state},
			raw:        []byte(body),
			rawMime:    LeafMimeType,
		}
	}

	return &TreePlugin{
		nodes: map[string]*memNode{
			RootBox: {
				name:     "box",
				typ:      ContainerType,
				children: []string{LeafAlpha, LeafBeta, NestedBox},
			},
			NestedBox: {
				name:     "nested",
				typ:      ContainerType,
				children: []string{LeafGamma},
			},
			LeafAlpha: leaf(
				"alpha", "open", "2026-06", 202606,
				[]string{"a", "b"}, "f1", "alpha body\n",
			),
			LeafBeta: leaf(
				"beta", "closed", "2026-07", 202607,
				[]string{"b"}, "f2", "beta body\n",
			),
			LeafGamma: leaf(
				"gamma", "open", "2026-07", 202607,
				[]string{"c"}, "f1", "gamma body\n",
			),
		},
	}
}

// Config is the test peer's ServeConfig over a FRESH tree instance,
// with ConfigApply recording the received TOML on that instance
// (readable via ReceivedConfigTOML through cfg.Plugin — the passthrough
// probe for tests).
func Config() traversal_serve.ServeConfig {
	plugin := NewPlugin()

	return traversal_serve.ServeConfig{
		Plugin: plugin,
		Info: traversal_serve.PluginInfo{
			Name:    "cg-test-traversal-serve",
			Version: "0.0.0",
		},
		ConfigApply: plugin.ApplyConfigTOML,
	}
}

// ApplyConfigTOML records the initialize config passthrough
// (ServeConfig.ConfigApply).
func (p *TreePlugin) ApplyConfigTOML(configTOML string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.configTOML = configTOML
	p.configApplied = true

	return nil
}

// ReceivedConfigTOML reports what ApplyConfigTOML recorded; ok is false
// when initialize never delivered a config.
func (p *TreePlugin) ReceivedConfigTOML() (configTOML string, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.configTOML, p.configApplied
}

func (p *TreePlugin) Schemes() []string { return []string{Scheme} }

func (p *TreePlugin) TypeTag() string { return TypeTag }

func (p *TreePlugin) Types() []cutting_garden_plugins.NodeType {
	return []cutting_garden_plugins.NodeType{
		{Tag: ContainerType, Container: true},
		{Tag: LeafType, Container: false, MimeType: LeafMimeType},
	}
}

func (p *TreePlugin) Roots(_ context.Context) ([]*url.URL, error) {
	root, err := url.Parse(RootBox)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return []*url.URL{root}, nil
}

func (p *TreePlugin) ListRoots(
	_ context.Context, node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	parent, ok := p.nodes[node.String()]
	if !ok || !parent.container() {
		return nil, nil
	}

	children := make(
		[]cutting_garden_plugins.Node, 0, len(parent.children),
	)

	for _, childURI := range parent.children {
		child := p.nodes[childURI]

		uri, err := url.Parse(childURI)
		if err != nil {
			return nil, errors.Wrap(err)
		}

		children = append(children, cutting_garden_plugins.Node{
			URI:    uri,
			Name:   child.name,
			Type:   child.typ,
			Facets: cloneFacets(child.facets),
		})
	}

	return children, nil
}

func (p *TreePlugin) ReadLeaf(
	_ context.Context, node *url.URL,
) (content cutting_garden_plugins.LeafContent, ok bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	leaf, found := p.nodes[node.String()]
	if !found || leaf.container() {
		return content, false, nil
	}

	if leaf.structured != nil {
		content.Structured = maps.Clone(leaf.structured)
	}

	content.Raw = slices.Clone(leaf.raw)
	content.RawMimeType = leaf.rawMime

	return content, true, nil
}

func (p *TreePlugin) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	return []cutting_garden_plugins.NodeTypeFacets{
		{
			Tag: LeafType,
			Dimensions: []cutting_garden_plugins.FacetDimension{
				{
					Key:   "state",
					Label: "State",
					Kind:  cutting_garden_plugins.FacetCategorical,
					// The CLOSED domain (RFC 0012 §2).
					Values: []cutting_garden_plugins.FacetValue{
						{Key: "open"}, {Key: "closed"},
					},
				},
				{
					Key:   "month",
					Label: "Month",
					Kind:  cutting_garden_plugins.FacetNumericBucket,
				},
				{
					Key:   "tag",
					Label: "Tags",
					Kind:  cutting_garden_plugins.FacetCategorical,
					// The multi-valued dimension.
					Multi: true,
				},
				{
					Key:   "feed",
					Label: "Feed",
					Kind:  cutting_garden_plugins.FacetLabelled,
				},
			},
		},
	}
}

func (p *TreePlugin) FacetCounts(
	_ context.Context, node *url.URL,
	filter cutting_garden_plugins.FacetFilter,
) (result cutting_garden_plugins.FacetResult, ok bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	root, found := p.nodes[node.String()]
	if !found || !root.container() {
		return result, false, nil
	}

	summary := cutting_garden_plugins.FacetSummary{}
	p.foldSubtree(root, filter, summary)

	return cutting_garden_plugins.FacetResult{
		Summary:  summary,
		Complete: true,
	}, true, nil
}

// foldSubtree counts every descendant node's facet membership into
// summary, honoring filter — the plugin-side equivalent of the
// framework fold (RFC 0012 §3). Caller holds p.mu.
func (p *TreePlugin) foldSubtree(
	node *memNode,
	filter cutting_garden_plugins.FacetFilter,
	summary cutting_garden_plugins.FacetSummary,
) {
	for _, childURI := range node.children {
		child := p.nodes[childURI]

		if filter.Matches(child.facets) {
			for dimension, values := range child.facets {
				histogram := summary[dimension]
				if histogram == nil {
					histogram = cutting_garden_plugins.FacetHistogram{}
					summary[dimension] = histogram
				}

				for _, value := range values {
					histogram[value.Key]++
				}
			}
		}

		if child.container() {
			p.foldSubtree(child, filter, summary)
		}
	}
}

func (p *TreePlugin) FacetVersion(
	_ context.Context, node *url.URL,
) (token string, ok bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, found := p.nodes[node.String()]; !found {
		return "", false, nil
	}

	return fmt.Sprintf("cgtest-gen-%d", p.generation), true, nil
}

func (p *TreePlugin) ResolveFacetLabels(
	_ context.Context, dimension string, keys []string,
) (map[string]string, error) {
	labels := make(map[string]string)

	for _, key := range keys {
		if label, found := facetLabels[dimension][key]; found {
			labels[key] = label
		}
	}

	return labels, nil
}

func (p *TreePlugin) DescribeBodies() []cutting_garden_plugins.NodeTypeBody {
	return []cutting_garden_plugins.NodeTypeBody{
		{
			Tag: LeafType,
			Accepts: []string{
				"text/plain (the object body)",
				"application/json (patch: fields merged into the structured view)",
			},
			Example: map[string]any{"title": "example"},
		},
	}
}

func (p *TreePlugin) CreateNode(
	_ context.Context, uri *url.URL, body io.Reader, typ string,
) error {
	data, err := readAllBody(body)
	if err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	key := uri.String()

	if _, exists := p.nodes[key]; exists {
		return errors.ErrorWithStackf("create %s: node already exists", key)
	}

	if typ != ContainerType && typ != LeafType {
		return errors.BadRequestf(
			"create %s: type %q is not declared", key, typ,
		)
	}

	parent, found := p.nodes[parentURI(uri)]
	if !found || !parent.container() {
		return errors.BadRequestf(
			"create %s: parent is not an existing container", key,
		)
	}

	name := path.Base(uri.Path)

	node := &memNode{name: name, typ: typ}
	if typ == ContainerType {
		node.children = []string{}
	} else {
		node.structured = map[string]any{"title": name}
		node.raw = data
		node.rawMime = LeafMimeType
	}

	p.nodes[key] = node
	parent.children = append(parent.children, key)
	p.generation++

	return nil
}

func (p *TreePlugin) PutNode(
	_ context.Context, uri *url.URL, body io.Reader,
) error {
	data, err := readAllBody(body)
	if err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	key := uri.String()

	node, found := p.nodes[key]
	if !found {
		return errors.ErrorWithStackf("put %s: node does not exist", key)
	}

	if node.container() {
		return errors.BadRequestf(
			"put %s: containers are not replaced as a unit", key,
		)
	}

	node.raw = data
	p.generation++

	return nil
}

func (p *TreePlugin) PatchNode(
	_ context.Context, uri *url.URL, body io.Reader,
) error {
	data, err := readAllBody(body)
	if err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	key := uri.String()

	node, found := p.nodes[key]
	if !found {
		return errors.ErrorWithStackf("patch %s: node does not exist", key)
	}

	if len(data) == 0 {
		return errors.BadRequestf("patch %s: empty body", key)
	}

	// The plugin-defined patch format: a JSON object whose fields merge
	// into the structured view; absent fields stay untouched
	// (the NodeMutator contract).
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return errors.BadRequestf("patch %s: body is not a JSON object: %s", key, err)
	}

	if node.structured == nil {
		node.structured = map[string]any{}
	}
	maps.Copy(node.structured, fields)
	p.generation++

	return nil
}

func (p *TreePlugin) DeleteNode(_ context.Context, uri *url.URL) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := uri.String()

	node, found := p.nodes[key]
	if !found {
		return errors.ErrorWithStackf("delete %s: node does not exist", key)
	}

	p.deleteSubtree(key, node)

	if parent, found := p.nodes[parentURI(uri)]; found {
		parent.children = slices.DeleteFunc(
			parent.children,
			func(childURI string) bool { return childURI == key },
		)
	}

	p.generation++

	return nil
}

// deleteSubtree removes node and every descendant. Caller holds p.mu.
func (p *TreePlugin) deleteSubtree(key string, node *memNode) {
	for _, childURI := range node.children {
		if child, found := p.nodes[childURI]; found {
			p.deleteSubtree(childURI, child)
		}
	}

	delete(p.nodes, key)
}

// parentURI derives a node's parent URI by trimming the last path
// segment — the tree's addressing is purely path-shaped.
func parentURI(uri *url.URL) string {
	parent := *uri
	parent.Path = path.Dir(strings.TrimSuffix(uri.Path, "/"))

	return strings.TrimSuffix(parent.String(), "/")
}

// readAllBody drains a mutation body, tolerating nil (a bodyless
// create).
func readAllBody(body io.Reader) ([]byte, error) {
	if body == nil {
		return nil, nil
	}

	data, err := io.ReadAll(body)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return data, nil
}

// cloneFacets deep-copies a facet map so callers never alias the tree's
// internal state.
func cloneFacets(
	facets map[string][]cutting_garden_plugins.FacetValue,
) map[string][]cutting_garden_plugins.FacetValue {
	if facets == nil {
		return nil
	}

	cloned := make(
		map[string][]cutting_garden_plugins.FacetValue, len(facets),
	)
	for key, values := range facets {
		cloned[key] = slices.Clone(values)
	}

	return cloned
}

// Main is the whole plugin process: the RFC 0013 bring-up sequence
// (cookie guard → rendezvous listen → announce on stdout → accept)
// around Serve, with the stdin-EOF lifecycle signal. Returns the
// process exit code: 0 after a graceful shutdown (the shutdown
// notification or stdin EOF), nonzero on bring-up failure or a dropped
// stream.
func Main() int {
	cookie, err := traversal_serve.CookieFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ln, sock, cleanup, err := traversal_serve.ListenRendezvous()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer cleanup()

	line, err := traversal_serve.AnnounceLine(cookie, traversal_serve.Handshake{
		Version: traversal_serve.SchemaV1,
		Network: traversal_serve.HandshakeNetwork,
		Address: sock,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if _, err := os.Stdout.WriteString(line); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// stdin EOF is a shutdown signal (RFC 0013 §Launch and rendezvous),
	// armed BEFORE accept so a host that dies (or a smoke test that
	// never dials) unblocks the accept via the listener close instead of
	// hanging the peer forever.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		cancel()
	}()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	conn, err := ln.AcceptUnix()
	if err != nil {
		if ctx.Err() != nil {
			return 0
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if err := traversal_serve.Serve(ctx, conn, Config()); err != nil {
		if ctx.Err() != nil {
			return 0
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
