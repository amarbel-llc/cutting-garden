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

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"

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
	// AssignedLeafType is created only via CreateChild — the peer
	// assigns its identity (cutting-garden#143).
	AssignedLeafType = "cgtest-assigned-v1"
	LeafMimeType     = "text/plain"
	// IssueType is a CONTAINER type that also carries its own body — the
	// cutting-garden#168 / RFC 0018 case: an IssueBox holds a comment
	// (its child) AND has a title/state (its body), served by ReadLeaf
	// even though it has children. It declares a URI template
	// (IssueURITemplate) so URI→type resolution can gate the body read.
	IssueType = "cgtest-issue-v1"
	// IssueURITemplate is IssueType's RFC 6570 Level 1 template. The
	// "issue-" literal keeps it from matching the sibling leaves
	// (box/alpha, box/beta) or NestedBox — resolution stays sound
	// (RFC 0018 §5).
	IssueURITemplate = "cgtest://fixture/box/issue-{n}"

	RootBox    = "cgtest://fixture/box"
	NestedBox  = "cgtest://fixture/box/nested"
	LeafAlpha  = "cgtest://fixture/box/alpha"
	LeafBeta   = "cgtest://fixture/box/beta"
	LeafGamma  = "cgtest://fixture/box/nested/gamma"
	IssueBox   = "cgtest://fixture/box/issue-1"
	IssueChild = "cgtest://fixture/box/issue-1/c1"
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

// container reports whether this node has children — the descendability
// signal, keyed on the child list rather than the type tag so a
// non-ContainerType container (IssueType, a container that ALSO has a
// body — cutting-garden#168) is still enumerated by ListRoots. For the
// plain fixture nodes children!=nil is equivalent to typ==ContainerType,
// so this is behavior-preserving for them.
func (n *memNode) container() bool { return n.children != nil }

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
	// assigned counts CreateChild-created nodes, so the peer's
	// server-assigned names are deterministic per instance.
	assigned int64

	// configTOML records what ApplyConfigTOML received, for tests
	// asserting the RFC 0007 §Plugin-Owned Sections passthrough.
	configTOML    string
	configApplied bool
}

var (
	_ cutting_garden_plugins.RootProvider   = (*TreePlugin)(nil)
	_ cutting_garden_plugins.EnrichedLister = (*TreePlugin)(nil)
	_ cutting_garden_plugins.LeafReader     = (*TreePlugin)(nil)
	_ cutting_garden_plugins.FacetDescriber = (*TreePlugin)(nil)
	_ cutting_garden_plugins.FacetCounter   = (*TreePlugin)(nil)
	_ cutting_garden_plugins.FacetVersioner = (*TreePlugin)(nil)
	_ cutting_garden_plugins.FacetLabeler   = (*TreePlugin)(nil)
	_ cutting_garden_plugins.NodeMutator    = (*TreePlugin)(nil)
	_ cutting_garden_plugins.BulkMutator    = (*TreePlugin)(nil)
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
				children: []string{LeafAlpha, LeafBeta, NestedBox, IssueBox},
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
			// IssueBox is a container WITH its own body (cutting-garden#168,
			// RFC 0018 §7): it holds a comment (IssueChild) and carries a
			// title/state read via ReadLeaf. It has NO facets, so it
			// contributes nothing to RootBox's facet summary — the existing
			// facet fixtures are unchanged.
			IssueBox: {
				name:       "issue-1",
				typ:        IssueType,
				structured: map[string]any{"title": "Fix it", "state": "open"},
				children:   []string{IssueChild},
			},
			// IssueChild is built WITHOUT the leaf() helper so it carries no
			// facets: it must not perturb RootBox's facet summary. It exists
			// only to make IssueBox a container WITH children.
			IssueChild: {
				name:       "c1",
				typ:        LeafType,
				structured: map[string]any{"title": "c1"},
				raw:        []byte("first comment\n"),
				rawMime:    LeafMimeType,
			},
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

// ConfigOutEnv, when set in the peer's environment, names a file
// ApplyConfigTOML writes the received config_toml to — the passthrough
// probe for consumers that only see the peer as a subprocess (the
// registration integration test, the bats lane).
const ConfigOutEnv = "CG_TESTPEER_CONFIG_OUT"

// ApplyConfigTOML records the initialize config passthrough
// (ServeConfig.ConfigApply), and mirrors it to $CG_TESTPEER_CONFIG_OUT
// when set (see ConfigOutEnv).
func (p *TreePlugin) ApplyConfigTOML(configTOML string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.configTOML = configTOML
	p.configApplied = true

	if out := os.Getenv(ConfigOutEnv); out != "" {
		if err := os.WriteFile(out, []byte(configTOML), 0o600); err != nil {
			return errors.Wrap(err)
		}
	}

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
		{Tag: AssignedLeafType, Container: false, MimeType: LeafMimeType},
		// A container type that ALSO declares a URI template and a body
		// (cutting-garden#168, RFC 0018).
		{Tag: IssueType, Container: true, URITemplate: IssueURITemplate},
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

// ListEnriched is the EnrichedLister capability (cutting-garden#160), the
// filter-pushdown path #193 exposes over the wire: it returns node's
// immediate children narrowed by the RFC 0012 §6 filter over each child's
// facets. A nil/empty filter returns the full listing. ok is always true
// for this peer — every container it lists, it can filter — so the host
// trusts the narrowed set rather than folding host-side.
func (p *TreePlugin) ListEnriched(
	ctx context.Context,
	node *url.URL,
	filter cutting_garden_plugins.FacetFilter,
) ([]cutting_garden_plugins.Node, bool, error) {
	children, err := p.ListRoots(ctx, node)
	if err != nil {
		return nil, false, err
	}

	if len(filter) == 0 {
		return children, true, nil
	}

	matched := make([]cutting_garden_plugins.Node, 0, len(children))
	for _, child := range children {
		if filter.Matches(child.Facets) {
			matched = append(matched, child)
		}
	}

	return matched, true, nil
}

func (p *TreePlugin) ReadLeaf(
	_ context.Context, node *url.URL,
) (content cutting_garden_plugins.LeafContent, ok bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	n, found := p.nodes[node.String()]
	if !found {
		return content, false, nil
	}

	// Decline (ok=false) a node with no own body — a plain container or a
	// bodyless node — so the consumer falls back to the child listing. A
	// container that DOES have a body (IssueBox) serves it here even though
	// it has children: the RFC 0018 §7.1 relaxation of "leaf.read only for
	// a childless node" (cutting-garden#168).
	if n.structured == nil && len(n.raw) == 0 {
		return content, false, nil
	}

	if n.structured != nil {
		content.Structured = maps.Clone(n.structured)
	}

	content.Raw = slices.Clone(n.raw)
	content.RawMimeType = n.rawMime

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

	// Per-child-container attribution (RFC 0012 §13, cutting-garden#173):
	// this peer's fold already visits each immediate child's subtree
	// separately, so recording the per-child match count on the way is
	// recovered information — exactly the "populate it only when you
	// already compute it" posture the contract asks for. Direct leaf
	// children contribute to Summary but to no breakdown entry (they live
	// under no child container).
	var breakdown []cutting_garden_plugins.FacetContainerBreakdown
	for _, childURI := range root.children {
		child := p.nodes[childURI]

		// Summary folding only — the return is deliberately discarded
		// for the breakdown, because a container is not "under" itself
		// (FacetContainerBreakdown.Count counts nodes that LIVE UNDER
		// the container), and with an empty filter Matches is trivially
		// true even for a facet-less container node, which would add a
		// phantom self-match to its own bucket. caldav, the reference
		// FacetCounter, attributes leaf objects only.
		p.foldNode(child, filter, summary)

		if child.container() {
			descendants := p.foldSubtree(child, filter, summary)
			if descendants > 0 {
				breakdown = append(breakdown,
					cutting_garden_plugins.FacetContainerBreakdown{
						URI:   childURI,
						Name:  child.name,
						Count: descendants,
					})
			}
		}
	}

	limited, truncated := cutting_garden_plugins.SortAndLimitContainerBreakdown(breakdown)

	// A summarizable container returns ok=true with a possibly-EMPTY
	// summary (the caldav convention — a task-free calendar summarizes to
	// an empty non-nil map). IssueBox is the facet-less summarizable
	// container that exercises this over the wire: with the #192 wire
	// normalization in place, its empty summary round-trips
	// indistinguishably from the linked instance.
	return cutting_garden_plugins.FacetResult{
		Summary:              summary,
		Complete:             true,
		ByContainer:          limited,
		ByContainerTruncated: truncated,
	}, true, nil
}

// foldNode counts one node's facet membership into summary when it
// matches filter, reporting 1 if it matched. Caller holds p.mu.
func (p *TreePlugin) foldNode(
	node *memNode,
	filter cutting_garden_plugins.FacetFilter,
	summary cutting_garden_plugins.FacetSummary,
) int64 {
	if !filter.Matches(node.facets) {
		return 0
	}

	for dimension, values := range node.facets {
		histogram := summary[dimension]
		if histogram == nil {
			histogram = cutting_garden_plugins.FacetHistogram{}
			summary[dimension] = histogram
		}

		for _, value := range values {
			histogram[value.Key]++
		}
	}

	return 1
}

// foldSubtree counts every descendant node's facet membership into
// summary, honoring filter, and reports how many descendants matched —
// the plugin-side equivalent of the framework fold (RFC 0012 §3).
// Caller holds p.mu.
func (p *TreePlugin) foldSubtree(
	node *memNode,
	filter cutting_garden_plugins.FacetFilter,
	summary cutting_garden_plugins.FacetSummary,
) (matched int64) {
	for _, childURI := range node.children {
		child := p.nodes[childURI]

		matched += p.foldNode(child, filter, summary)

		if child.container() {
			matched += p.foldSubtree(child, filter, summary)
		}
	}

	return matched
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
		{
			// The server-assigned-identity type (cutting-garden#143):
			// created only via CreateChild — the peer names the node.
			Tag:                    AssignedLeafType,
			Accepts:                []string{"text/plain (the object body)"},
			ServerAssignedIdentity: true,
		},
		{
			// A CONTAINER type with a writable — and therefore readable
			// (RFC 0018 §7.1) — body: this declaration is what the host's
			// read gate consults to fetch IssueBox's own body beside its
			// child listing (cutting-garden#168).
			Tag: IssueType,
			Accepts: []string{
				"application/json (patch: fields merged into the" +
					" structured view)",
			},
			Example: map[string]any{"title": "example", "state": "open"},
		},
	}
}

// CreateChild is the ContainerCreator capability (cutting-garden#143):
// the peer assigns the created node's identity — a deterministic
// child-N name under the container — and reports it back.
func (p *TreePlugin) CreateChild(
	_ context.Context, container *url.URL, body io.Reader, typ string,
) (*url.URL, error) {
	if typ != AssignedLeafType {
		return nil, errors.BadRequestf(
			"create_child under %s: type %q is not server-assigned",
			container.String(), typ,
		)
	}
	data, err := readAllBody(body)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	parent, found := p.nodes[container.String()]
	if !found || !parent.container() {
		return nil, errors.BadRequestf(
			"create_child: %s is not a container", container.String(),
		)
	}

	p.assigned++
	key := strings.TrimRight(container.String(), "/") +
		fmt.Sprintf("/assigned-%d", p.assigned)
	p.nodes[key] = &memNode{
		name:       fmt.Sprintf("assigned-%d", p.assigned),
		typ:        AssignedLeafType,
		structured: map[string]any{"title": string(data)},
		raw:        data,
		rawMime:    LeafMimeType,
	}
	parent.children = append(parent.children, key)
	p.generation++

	created, err := url.Parse(key)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return created, nil
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

	// Existence violations of the strict create/put/patch/delete contracts
	// are CALLER mistakes (pick the other verb, or the right URI), matching
	// caldav's 412-precondition classification (cutting-garden#187).
	if _, exists := p.nodes[key]; exists {
		return errors.BadRequestf("create %s: node already exists", key)
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
		return errors.BadRequestf("put %s: node does not exist", key)
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
) ([]string, error) {
	data, err := readAllBody(body)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	key := uri.String()

	node, found := p.nodes[key]
	if !found {
		return nil, errors.BadRequestf("patch %s: node does not exist", key)
	}

	if len(data) == 0 {
		return nil, errors.BadRequestf("patch %s: empty body", key)
	}

	// The plugin-defined patch format: a JSON object whose fields merge
	// into the structured view; absent fields stay untouched
	// (the NodeMutator contract).
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, errors.BadRequestf("patch %s: body is not a JSON object: %s", key, err)
	}

	if node.structured == nil {
		node.structured = map[string]any{}
	}
	maps.Copy(node.structured, fields)
	p.generation++

	// This peer's structured view accepts any key, so every field named is
	// a field applied — reported explicitly (never nil) so the wire's
	// applied round-trip is exercised end-to-end (cutting-garden#182).
	// Sorted: the wire result is compared verbatim by the
	// indistinguishability tests, and Go map iteration order is random.
	applied := slices.Sorted(maps.Keys(fields))

	return applied, nil
}

func (p *TreePlugin) DeleteNode(_ context.Context, uri *url.URL) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := uri.String()

	node, found := p.nodes[key]
	if !found {
		return errors.BadRequestf("delete %s: node does not exist", key)
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
