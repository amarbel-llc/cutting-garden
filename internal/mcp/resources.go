package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"code.linenisgreat.com/cutting-garden/internal/capture_plugin"
	"code.linenisgreat.com/cutting-garden/internal/command_components"
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/purse-first/libs/go-mcp/protocol"
	"code.linenisgreat.com/purse-first/libs/go-mcp/server"
)

// madderBlobScheme prefixes a content-addressed blob URI: the verbatim
// source bytes of a leaf, written to the server's madder store and linked
// by digest so a client fetches them out-of-band (#85).
const madderBlobScheme = "madder://blobs/"

// mimeListing is the content type of a container's child listing: a JSON
// array of node views. Containers advertise it so a client knows a
// `resources/read` yields more structure to descend rather than object
// bytes.
const mimeListing = "application/json"

// mimeObject is the content type of a leaf object's structured body: a JSON
// object (the parsed fields a client reads), distinct from mimeListing's
// JSON array of child views only by content, not media type.
const mimeObject = "application/json"

// mimeFacets marks the facet-summary content block a container read carries
// beside its child listing (RFC 0012 §7), so a client can tell the summary
// from the listing itself rather than by position.
const mimeFacets = "application/vnd.cutting-garden.facets+json"

// resolveFunc resolves a URI string to its parsed URL and the RootLister
// plugin registered for its scheme. It has the shape of
// command_components.ResolveRootListerPlugin and is a field on Resources
// so tests can substitute a registry-free fake.
type resolveFunc func(uriStr string) (
	*url.URL, cutting_garden_plugins.RootLister, error,
)

// Resources implements go-mcp's server.ResourceProvider over the
// cutting-garden RootLister registry (FDR 0014): `Node` becomes an MCP
// resource and `ListRoots` backs `resources/list` and `resources/read`.
//
// It is configured with a set of root endpoint URIs. `resources/list`
// enumerates the immediate children of those roots; `resources/read`
// drills one level deeper into any container URI — the same lazy,
// one-level-per-call traversal the `list` command and capture share. A
// read of a childless URI offers the leaf object's structured body when
// the plugin implements LeafReader (#85).
//
// The provider holds no per-node cursor: every read re-resolves the
// plugin from the requested URI, mirroring the stateless RootLister
// contract (a node is always addressed by URI, never a server-held
// position). Discovery is read-only; the one write it performs is
// content-addressed and optional: when writer is non-nil, a leaf read
// stores the object's verbatim bytes and adds a `madder://blobs/<digest>`
// link beside the parsed fields (#85). A nil writer (no store configured)
// degrades cleanly to structured-only reads.
type Resources struct {
	roots   []*url.URL
	resolve resolveFunc
	// writer persists a leaf's verbatim bytes so they can be linked by
	// digest. Optional: nil means no store is configured and a leaf read
	// returns its parsed fields without a raw-bytes link.
	writer capture_plugin.Writer
	// facets memoizes container facet summaries (RFC 0012 §11) so reads
	// serve cached counts instead of recomputing per read.
	facets *facetCache
	// listings memoizes enriched, unfiltered container listings
	// (cutting-garden#160 phase 3) so reads serve cached enrichment instead
	// of recomputing a plugin's data-bearing fetch per read.
	listings *listingCache
}

var _ server.ResourceProvider = (*Resources)(nil)

// newResources builds a provider over the given root endpoints, wired to
// the real plugin registry. writer is the optional blob sink for raw leaf
// bytes (nil when no store is configured).
func newResources(roots []*url.URL, writer capture_plugin.Writer) *Resources {
	return &Resources{
		roots:    roots,
		resolve:  command_components.ResolveRootListerPlugin,
		writer:   writer,
		facets:   newFacetCache(),
		listings: newListingCache(),
	}
}

// startFacetMaintenance warms the facet AND listing caches with the
// configured roots (so first reads hit warm cache) and runs both eager
// refreshers until ctx is done (RFC 0012 §11.2, extended to #160's listing
// enrichment cache). Warmup is best-effort: an endpoint that fails or
// declines simply stays cold and computes on first touch. The listing
// refresher runs in its own goroutine since facets.maintain blocks for the
// life of ctx.
func (r *Resources) startFacetMaintenance(ctx context.Context) {
	for _, root := range r.roots {
		if ctx.Err() != nil {
			return
		}
		uri := root.String()
		if u, lister, err := r.resolve(uri); err == nil {
			_, _ = r.facets.serve(ctx, lister, uri, u)
			_, _, _ = r.listings.serve(ctx, lister, uri, u)
		}
	}
	go r.listings.maintain(ctx, r.resolve, facetRefreshInterval)
	r.facets.maintain(ctx, r.resolve, facetRefreshInterval)
}

// ListResources enumerates the immediate children of every configured
// root and returns them as MCP resources. This is the `ListRoots →
// resources/list` mapping: the roots are the givens, and the children
// under them are the discoverable resources. A resolution or traversal
// failure on any root is surfaced (the listing is not silently partial).
func (r *Resources) ListResources(
	ctx context.Context,
) ([]protocol.Resource, error) {
	var out []protocol.Resource
	for _, root := range r.roots {
		_, lister, err := r.resolve(root.String())
		if err != nil {
			return nil, errors.Wrapf(err, "resolve root %s", root)
		}
		nodes, err := lister.ListRoots(ctx, root)
		if err != nil {
			return nil, errors.Wrapf(err, "list roots under %s", root)
		}
		for _, n := range nodes {
			out = append(out, nodeToResource(lister, n))
		}
	}
	return out, nil
}

// content selector values (RFC 0018 §7.3): what a read returns for a node
// that has children, a body, or both.
const (
	// contentBoth returns the node's own body AND its child listing.
	contentBoth = "both"
	// contentChildren returns the child listing only — the pre-RFC-0018
	// behavior (cheap browsing, no body fetch).
	contentChildren = "children"
	// contentBody returns the node's own body only, skipping the listing.
	contentBody = "body"
)

// ReadResource is the MCP resources/read entry (server.ResourceProvider).
// resources/read carries no selector, so it reads uri with the default
// `both` (RFC 0018 §7.3): a body-bearing container returns its own body
// beside its child listing. The read_node tool exposes the other selectors.
func (r *Resources) ReadResource(
	ctx context.Context,
	uri string,
) (*protocol.ResourceReadResult, error) {
	return r.ReadNode(ctx, uri, contentBoth)
}

// ReadNode reads uri under the RFC 0018 §7.3 content selector. `children`
// is the pre-RFC-0018 read verbatim: list the immediate children, and for
// a childless node offer the leaf's own body when the plugin can fetch it
// (#85) — otherwise an empty listing, honest for a genuinely empty
// container. `both` adds, for a container that HAS children, that
// container's OWN body beside the listing (RFC 0018 §7): the host fetches
// it when local URI→type resolution says the type declares a body, or —
// for a template-less URI — by probe (RFC 0018 §6), and a container with
// no own body adds nothing. `body` returns the node's own body alone, never
// a listing.
func (r *Resources) ReadNode(
	ctx context.Context,
	uri string,
	content string,
) (*protocol.ResourceReadResult, error) {
	u, lister, err := r.resolve(uri)
	if err != nil {
		return nil, errors.Wrapf(err, "read resource %s", uri)
	}

	// body-only: the node's own body, never a listing (RFC 0018 §7.3).
	if content == contentBody {
		if result, ok, lerr := r.readLeaf(ctx, lister, uri, u); lerr != nil {
			return nil, lerr
		} else if ok {
			return result, nil
		}
		// No own body: an honest empty result (body mode never lists).
		return &protocol.ResourceReadResult{}, nil
	}

	nodes, prov, err := r.listings.serve(ctx, lister, uri, u)
	if err != nil {
		return nil, errors.Wrapf(err, "list roots under %s", uri)
	}

	// Childless: a leaf or an empty container. Offer the leaf's body when
	// the plugin can fetch it (shared by children and both, unchanged);
	// otherwise fall through to the (empty) listing.
	if len(nodes) == 0 {
		if result, ok, lerr := r.readLeaf(ctx, lister, uri, u); lerr != nil {
			return nil, lerr
		} else if ok {
			return result, nil
		}
	}

	var contents []protocol.ResourceContent

	// both-mode only: a container that HAS children MAY also carry its own
	// body (RFC 0018 §7). Fetch it declaration-gated (URI→type resolves to a
	// type that declares a body) or, for a template-less URI, by probe; a
	// container with no own body adds nothing.
	if content == contentBoth && len(nodes) > 0 &&
		r.containerBodyWanted(lister, uri) {
		bodyContents, ok, berr := r.leafContents(ctx, lister, uri, u)
		if berr != nil {
			return nil, berr
		}
		if ok {
			contents = append(contents, bodyContents...)
		}
	}

	// The child listing (children and both).
	listingJSON, err := renderNodeViews(lister, nodes, prov)
	if err != nil {
		return nil, err
	}
	contents = append(contents, protocol.ResourceContent{
		URI:      uri,
		MimeType: mimeListing,
		Text:     listingJSON,
	})

	// A container carries the hoisted facet summary of its subtree beside the
	// child listing, when its plugin can summarize it (RFC 0012 §7).
	if len(nodes) > 0 {
		facets, ferr := r.facetContent(ctx, lister, uri, u)
		if ferr != nil {
			return nil, ferr
		}
		if facets != nil {
			contents = append(contents, *facets)
		}
	}

	return &protocol.ResourceReadResult{Contents: contents}, nil
}

// listingVersion is the container snapshot provenance a listing carries
// when its plugin implements FacetVersioner (cutting-garden#203): the raw
// FacetVersion token — compare it across two listings to know for certain
// whether they read the same underlying snapshot — plus when it was
// resolved and how fresh. Unlike read_facets (facetView), which keeps the
// raw token cache-internal and surfaces only freshness, a listing exposes
// the token itself, because cross-call equality IS the use case; the token
// is opaque-but-comparable by RFC 0012 design and carries nothing secret.
// All fields omitempty: a listing whose plugin declares no versioner
// carries no version at all.
type listingVersion struct {
	Version           string `json:"version,omitempty"`
	VersionComputedAt string `json:"versionComputedAt,omitempty"`
	Freshness         string `json:"freshness,omitempty"`
}

// listingView is the shape of an enriched (unfiltered) child listing: the
// nodes plus the optional version block. It is what both list_nodes' default
// path and resources/read emit for a container (cutting-garden#203 kept the
// two byte-identical — the version rides both, not just the tool), replacing
// the pre-#203 bare nodeView array.
type listingView struct {
	Nodes []nodeView `json:"nodes"`
	listingVersion
}

// renderNodeViews marshals a container's children as the enriched listing
// JSON — the nodes plus, when the plugin is a FacetVersioner, the version
// block whose token corresponds to exactly these nodes (prov comes from the
// same cache entry that produced them).
func renderNodeViews(
	lister cutting_garden_plugins.RootLister,
	nodes []cutting_garden_plugins.Node,
	prov listingProvenance,
) (string, error) {
	views := make([]nodeView, 0, len(nodes))
	for _, n := range nodes {
		views = append(views, enrichedNodeView(lister, n))
	}
	body, err := json.MarshalIndent(listingView{
		Nodes:          views,
		listingVersion: prov.view(),
	}, "", "  ")
	if err != nil {
		return "", errors.Wrap(err)
	}
	return string(body), nil
}

// containerBodyWanted decides whether both-mode should fetch a
// child-bearing container's OWN body (RFC 0018 §7.2). It resolves the URI
// to its declared type locally: a type that declares a body (BodyDescriber)
// is read; a type with no body is skipped (no wasted round trip); an
// unresolved URI (no template, a tie, no match — RFC 0018 §4/§6) falls back
// to the probe, so a template-less plugin's container body is still exposed.
func (r *Resources) containerBodyWanted(
	lister cutting_garden_plugins.RootLister, uri string,
) bool {
	resolved, ok := cutting_garden_plugins.ResolveNodeTypeByURI(lister, uri)
	if !ok {
		return true
	}
	return typeDeclaresBody(lister, resolved.Type.Tag)
}

// typeDeclaresBody reports whether lister declares a writable body for the
// given node type (its BodyDescriber lists the tag). A writable body is by
// construction readable, so this is the RFC 0018 §7.2 single source of
// truth for "this container type has an own body," reusing the FDR 0020
// declaration rather than a new capability token.
func typeDeclaresBody(
	lister cutting_garden_plugins.RootLister, tag string,
) bool {
	bd, ok := lister.(cutting_garden_plugins.BodyDescriber)
	if !ok {
		return false
	}
	for _, b := range bd.DescribeBodies() {
		if b.Tag == tag {
			return true
		}
	}
	return false
}

// readLeaf returns the structured body of a leaf object when lister can
// fetch it (the LeafReader capability, #85). ok is false when the plugin
// has no LeafReader, or does not recognize u as a fetchable leaf, so the
// caller falls back to the empty listing. A non-nil error is an unexpected
// failure to surface, not the ordinary "not a leaf" outcome.
func (r *Resources) readLeaf(
	ctx context.Context,
	lister cutting_garden_plugins.RootLister,
	uri string,
	u *url.URL,
) (*protocol.ResourceReadResult, bool, error) {
	contents, ok, err := r.leafContents(ctx, lister, uri, u)
	if err != nil || !ok {
		return nil, ok, err
	}
	return &protocol.ResourceReadResult{Contents: contents}, true, nil
}

// leafContents fetches a node's OWN body as content blocks — its structured
// fields (mimeObject) plus, when a store is configured, a content-addressed
// blob link to the verbatim bytes (#85) — and reports ok. It is the shared
// body-fetch behind both a childless leaf read (readLeaf) and both-mode's
// container-own-body block (RFC 0018 §7). ok is false when the plugin has
// no LeafReader or does not recognize u as having an own body; a non-nil
// error is an unexpected failure to surface, not the ordinary "no body".
func (r *Resources) leafContents(
	ctx context.Context,
	lister cutting_garden_plugins.RootLister,
	uri string,
	u *url.URL,
) ([]protocol.ResourceContent, bool, error) {
	lr, ok := lister.(cutting_garden_plugins.LeafReader)
	if !ok {
		return nil, false, nil
	}
	content, ok, err := lr.ReadLeaf(ctx, u)
	if err != nil {
		return nil, false, errors.Wrapf(err, "read leaf %s", uri)
	}
	if !ok {
		return nil, false, nil
	}

	body, err := json.MarshalIndent(content.Structured, "", "  ")
	if err != nil {
		return nil, false, errors.Wrap(err)
	}

	contents := []protocol.ResourceContent{{
		URI:      uri,
		MimeType: mimeObject,
		Text:     string(body),
	}}
	// Add the verbatim source as a content-addressed blob link beside the
	// parsed fields, when a store is configured to hold it. The bytes are
	// written, not inlined: the client fetches them by digest.
	if link := r.rawBlobLink(ctx, content); link != nil {
		contents = append(contents, *link)
	}

	return contents, true, nil
}

// rawBlobLink stores a leaf's verbatim bytes and returns a link-only
// content entry addressing them by digest (`madder://blobs/<digest>`,
// no inlined text). It returns nil — no link, the structured fields
// stand alone — when no store is configured, the leaf has no raw form,
// or the write fails: persisting the source bytes is a best-effort
// enrichment that must never fail the read of the parsed object.
func (r *Resources) rawBlobLink(
	ctx context.Context,
	content cutting_garden_plugins.LeafContent,
) *protocol.ResourceContent {
	if r.writer == nil || len(content.Raw) == 0 {
		return nil
	}
	digest, _, err := r.writer.WriteBlob(ctx, bytes.NewReader(content.Raw))
	if err != nil {
		return nil
	}
	return &protocol.ResourceContent{
		URI:      madderBlobScheme + digest,
		MimeType: content.RawMimeType,
	}
}

// facetView is the JSON projection of a container's hoisted facet summary:
// the per-dimension histograms, whether the summary covers the whole
// subtree, and the memoization layer's freshness metadata (RFC 0012 §11.2).
type facetView struct {
	Facets   cutting_garden_plugins.FacetSummary `json:"facets"`
	Complete bool                                `json:"complete"`
	// ByContainer is the OPTIONAL per-child-container breakdown of Facets
	// (RFC 0012 §13, cutting-garden#170): which immediate child container
	// of the summarized node each matching node lives under, so a caller
	// can descend into exactly the containers that contributed instead of
	// guessing across a wide fan-out. nil/empty when the plugin does not
	// compute per-container attribution for this node (honest absence,
	// not every plugin or node has one to report).
	ByContainer []cutting_garden_plugins.FacetContainerBreakdown `json:"byContainer,omitempty"`
	// ByContainerTruncated is true when ByContainer was capped
	// (FacetContainerBreakdownLimit) and more non-empty child containers
	// contributed beyond what is listed.
	ByContainerTruncated bool `json:"byContainerTruncated,omitempty"`
	// ComputedAt is when the served summary was computed (RFC 3339).
	ComputedAt string `json:"computedAt,omitempty"`
	// ValidUntil, present when the summary contains volatile dimensions
	// (RFC 0012 §11.3), is computedAt plus the volatile window: the
	// bound on the VOLATILE dimensions' currency (pure dimensions in the
	// same summary remain token-fresh past it). RFC 3339.
	ValidUntil string `json:"validUntil,omitempty"`
	// Freshness is fresh | unverified | stale (facet_cache.go).
	Freshness string `json:"freshness,omitempty"`
	// Error carries the last refresh/compute failure when the served
	// summary is stale (or, with no facets at all, why none is available).
	Error string `json:"error,omitempty"`
	// Labels resolves FacetLabelled dimensions' opaque value keys to display
	// names (RFC 0012 §7): {dimension: {key: label}}. Present only for
	// dimensions the plugin declares FacetLabelled AND implements
	// FacetLabeler for; a key with no resolved label is simply absent (the
	// consumer falls back to the key). Presentation-only and non-fatal —
	// resolution failure omits labels, never fails the read (§7, §9).
	Labels map[string]map[string]string `json:"labels,omitempty"`
}

// facetContent serves a container's hoisted facet summary as a facet
// content block (RFC 0012 §7), from the memoization layer (§11): cached
// summaries are served as-is, a first touch computes once. It returns nil
// when the plugin is not a FacetCounter or declines this node. Per the §9
// implicit-surface rule a facet failure never fails the enclosing read:
// with no cached summary to fall back on, the block degrades to an
// error-only notation (stale-with-error serving is the cache's job).
func (r *Resources) facetContent(
	ctx context.Context,
	lister cutting_garden_plugins.RootLister,
	uri string,
	u *url.URL,
) (*protocol.ResourceContent, error) {
	view, err := r.facets.serve(ctx, lister, uri, u)
	if err != nil {
		view = &facetView{Freshness: freshnessStale, Error: err.Error()}
	}
	if view == nil {
		return nil, nil
	}
	attachLabels(ctx, lister, view)

	body, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return &protocol.ResourceContent{
		URI:      uri,
		MimeType: mimeFacets,
		Text:     string(body),
	}, nil
}

// ReadFacets returns the facet view for uri — the read_facets tool's read
// surface (cutting-garden#151), the same facetView shape a container's
// resources/read carries beside its child listing but reachable from a
// tools-only client. Two paths per the phase-1 design:
//
//   - Nil/empty filter serves the MEMOIZED summary via the same facetCache
//     `resources/read` uses (RFC 0012 §11.2): an implicit-surface read,
//     degrading a compute failure to a stale, error-noted view rather than
//     failing the call (§9).
//   - A non-empty filter is an EXPLICIT facet request (RFC 0012 §9):
//     it computes directly via FacetCounts(filter), bypassing the cache
//     entirely, and is fail-fast — an error surfaces to the caller — and
//     implicitly fresh (a direct compute is always current).
//
// Returns an error when the resolved plugin does not implement
// FacetCounter: facets are simply unavailable for that scheme.
func (r *Resources) ReadFacets(
	ctx context.Context,
	uri string,
	filter cutting_garden_plugins.FacetFilter,
) (*facetView, error) {
	u, lister, err := r.resolve(uri)
	if err != nil {
		return nil, errors.Wrapf(err, "read facets %s", uri)
	}
	counter, ok := lister.(cutting_garden_plugins.FacetCounter)
	if !ok {
		return nil, errors.ErrorWithStackf(
			"read_facets %s: facets are not available for scheme %q "+
				"(plugin does not implement FacetCounter)", uri, u.Scheme,
		)
	}

	// Validate an explicit filter against the plugin's declared schema
	// BEFORE computing anything (cutting-garden#161): an undeclared
	// dimension or an out-of-domain closed-dimension value is rejected
	// with an actionable error, so a filter that genuinely matches
	// nothing stays distinguishable from a typo'd one.
	if len(filter) > 0 {
		var dims []cutting_garden_plugins.NodeTypeFacets
		if describer, ok := lister.(cutting_garden_plugins.FacetDescriber); ok {
			dims = describer.DescribeFacets()
		}
		if verr := filter.Validate(dims); verr != nil {
			return nil, errors.Wrapf(verr, "read_facets %s", uri)
		}
	}

	if len(filter) == 0 {
		view, ferr := r.facets.serve(ctx, lister, uri, u)
		if ferr != nil {
			view = &facetView{Freshness: freshnessStale, Error: ferr.Error()}
		}
		if view == nil {
			return nil, errors.ErrorWithStackf(
				"read_facets %s: no facet summary available at this node", uri,
			)
		}
		attachLabels(ctx, lister, view)
		return view, nil
	}

	result, ok, err := counter.FacetCounts(ctx, u, filter)
	if err != nil {
		return nil, errors.Wrapf(err, "read facets %s (filtered)", uri)
	}
	if !ok {
		return nil, errors.ErrorWithStackf(
			"read_facets %s: no facet summary available at this node", uri,
		)
	}
	view := &facetView{
		Facets:               result.Summary,
		Complete:             result.Complete,
		ByContainer:          result.ByContainer,
		ByContainerTruncated: result.ByContainerTruncated,
		ComputedAt:           time.Now().UTC().Format(time.RFC3339),
		Freshness:            freshnessFresh,
	}
	attachLabels(ctx, lister, view)
	return view, nil
}

// ListResourceTemplates returns no templates: cutting-garden resources
// are enumerated, not parameterized by a URI pattern.
func (r *Resources) ListResourceTemplates(
	context.Context,
) ([]protocol.ResourceTemplate, error) {
	return nil, nil
}

// nodeView is the JSON projection of a Node in a read listing.
// Container and MimeType are resolved from the plugin's declared
// Types() so a client can tell a descendable node from a leaf — and
// what a leaf's bytes are — without hardcoding tag strings. MimeType is
// the node body's content type (leaf default applied); it is empty for
// containers, whose listing rendering is the server's concern.
//
// Facets and Fields carry the node's ENRICHMENT (cutting-garden#160): a
// listing is enriched by default, so both are populated whenever the
// underlying Node carries them (nil/empty omits the key entirely, so a
// caller of the bare/opt-out path — which never populates them — sees
// the pre-#160 shape byte-for-byte).
type nodeView struct {
	URI       string `json:"uri"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Container bool   `json:"container"`
	MimeType  string `json:"mimeType,omitempty"`
	// Facets projects the node's own facet membership (Node.Facets): per
	// dimension key, the value keys it belongs to. Order is a histogram
	// sort hint irrelevant per-node, so it is dropped here — the raw
	// FacetValue.Key list is what a listing consumer filters/reads by.
	Facets map[string][]string `json:"facets,omitempty"`
	// Fields projects the node's declared human-readable listing
	// projection (Node.Fields) — e.g. a caldav object's summary/due/
	// status/dtstart.
	Fields map[string]any `json:"fields,omitempty"`
}

// projectNodeFacets renders a node's facet membership map for the listing
// view: per dimension, just the value keys (Order is a histogram-sort hint,
// meaningless for a single node's membership). nil in, nil out — so an
// unenriched or facet-free node's view omits the key (omitempty).
func projectNodeFacets(
	facets map[string][]cutting_garden_plugins.FacetValue,
) map[string][]string {
	if len(facets) == 0 {
		return nil
	}
	out := make(map[string][]string, len(facets))
	for dim, values := range facets {
		keys := make([]string, 0, len(values))
		for _, v := range values {
			keys = append(keys, v.Key)
		}
		out[dim] = keys
	}
	return out
}

// bareNodeView projects n onto the cheap, pre-#160 shape: no facets, no
// fields — the list_nodes `bare` opt-out (cutting-garden#160).
func bareNodeView(
	lister cutting_garden_plugins.RootLister, n cutting_garden_plugins.Node,
) nodeView {
	nt, _ := cutting_garden_plugins.NodeTypeFor(lister, n.Type)
	return nodeView{
		URI:       n.URIString(),
		Name:      n.Name,
		Type:      n.Type,
		Container: nt.Container,
		MimeType:  nt.BodyMimeType(),
	}
}

// enrichedNodeView projects n onto the enriched (default) shape: bareNodeView
// plus whatever Facets/Fields the node carries.
func enrichedNodeView(
	lister cutting_garden_plugins.RootLister, n cutting_garden_plugins.Node,
) nodeView {
	v := bareNodeView(lister, n)
	v.Facets = projectNodeFacets(n.Facets)
	v.Fields = n.Fields
	return v
}

// nodeToResource maps a traversal Node onto an MCP resource. A container
// advertises the JSON listing mimetype (reading it yields children); a
// leaf advertises its declared body mimetype (NodeType.MimeType, leaf
// default application/octet-stream) — what the object's bytes are, even
// though resources/read does not fetch them yet (#85).
func nodeToResource(
	lister cutting_garden_plugins.RootLister,
	n cutting_garden_plugins.Node,
) protocol.Resource {
	nt, _ := cutting_garden_plugins.NodeTypeFor(lister, n.Type)
	res := protocol.Resource{
		URI:         n.URIString(),
		Name:        n.Name,
		Description: describe(nt.Container, n.Type),
	}
	if nt.Container {
		res.MimeType = mimeListing
	} else {
		res.MimeType = nt.BodyMimeType()
	}
	return res
}

// describe renders a short, human-readable resource description from the
// node's kind and type tag.
func describe(container bool, tag string) string {
	kind := "object"
	if container {
		kind = "container"
	}
	if tag == "" {
		return kind
	}
	return fmt.Sprintf("%s node (type %s)", kind, tag)
}
