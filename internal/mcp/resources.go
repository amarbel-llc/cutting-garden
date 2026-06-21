package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/cutting-garden/internal/command_components"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/server"
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
}

var _ server.ResourceProvider = (*Resources)(nil)

// newResources builds a provider over the given root endpoints, wired to
// the real plugin registry. writer is the optional blob sink for raw leaf
// bytes (nil when no store is configured).
func newResources(roots []*url.URL, writer capture_plugin.Writer) *Resources {
	return &Resources{
		roots:   roots,
		resolve: command_components.ResolveRootListerPlugin,
		writer:  writer,
	}
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

// ReadResource descends one level under uri: it lists the node's
// immediate children and returns them as a JSON array of node views, so
// a client traverses the tree lazily by reading successively deeper
// container URIs.
//
// When the node has no children it is a leaf or an empty container. The
// plugin is then probed for the LeafReader capability (#85): if it can
// fetch the object's body, the read returns that object's structured
// fields as JSON rather than an empty array. A plugin without LeafReader,
// or a node the plugin does not recognize as a leaf, still reads as an
// empty array — honest for a genuinely empty container.
func (r *Resources) ReadResource(
	ctx context.Context,
	uri string,
) (*protocol.ResourceReadResult, error) {
	u, lister, err := r.resolve(uri)
	if err != nil {
		return nil, errors.Wrapf(err, "read resource %s", uri)
	}
	nodes, err := lister.ListRoots(ctx, u)
	if err != nil {
		return nil, errors.Wrapf(err, "list roots under %s", uri)
	}

	// No children: the node is a leaf or an empty container. Offer the
	// leaf's body when the plugin can fetch it; otherwise fall through to
	// the (empty) listing.
	if len(nodes) == 0 {
		if result, ok, lerr := r.readLeaf(ctx, lister, uri, u); lerr != nil {
			return nil, lerr
		} else if ok {
			return result, nil
		}
	}

	views := make([]nodeView, 0, len(nodes))
	for _, n := range nodes {
		nt, _ := cutting_garden_plugins.NodeTypeFor(lister, n.Type)
		views = append(views, nodeView{
			URI:       n.URIString(),
			Name:      n.Name,
			Type:      n.Type,
			Container: nt.Container,
			MimeType:  nt.BodyMimeType(),
		})
	}
	body, err := json.MarshalIndent(views, "", "  ")
	if err != nil {
		return nil, errors.Wrap(err)
	}

	contents := []protocol.ResourceContent{{
		URI:      uri,
		MimeType: mimeListing,
		Text:     string(body),
	}}
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

	return &protocol.ResourceReadResult{Contents: contents}, true, nil
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

// facetView is the JSON projection of a container's hoisted facet summary: the
// per-dimension histograms plus whether the summary covers the whole subtree.
type facetView struct {
	Facets   cutting_garden_plugins.FacetSummary `json:"facets"`
	Complete bool                                `json:"complete"`
}

// facetContent computes a container's hoisted facet summary via the plugin's
// FacetCounter and renders it as a facet content block (RFC 0012 §7). It
// returns nil when the plugin is not a FacetCounter or declines to summarize
// this node (ok=false). A FacetCounts error is fatal to the read (RFC 0012
// §9): a declared facet surface that errors is surfaced, not silently dropped.
func (r *Resources) facetContent(
	ctx context.Context,
	lister cutting_garden_plugins.RootLister,
	uri string,
	u *url.URL,
) (*protocol.ResourceContent, error) {
	counter, ok := lister.(cutting_garden_plugins.FacetCounter)
	if !ok {
		return nil, nil
	}
	result, ok, err := counter.FacetCounts(ctx, u, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "facet summary for %s", uri)
	}
	if !ok {
		return nil, nil
	}

	body, err := json.MarshalIndent(facetView{
		Facets:   result.Summary,
		Complete: result.Complete,
	}, "", "  ")
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return &protocol.ResourceContent{
		URI:      uri,
		MimeType: mimeFacets,
		Text:     string(body),
	}, nil
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
type nodeView struct {
	URI       string `json:"uri"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Container bool   `json:"container"`
	MimeType  string `json:"mimeType,omitempty"`
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
