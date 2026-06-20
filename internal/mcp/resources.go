package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/amarbel-llc/cutting-garden/internal/command_components"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/server"
)

// mimeListing is the content type of a container's child listing: a JSON
// array of node views. Containers advertise it so a client knows a
// `resources/read` yields more structure to descend rather than object
// bytes.
const mimeListing = "application/json"

// mimeObject is the content type of a leaf object's structured body: a JSON
// object (the parsed fields a client reads), distinct from mimeListing's
// JSON array of child views only by content, not media type.
const mimeObject = "application/json"

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
// position). It never captures or writes a blob store: a leaf read fetches
// the object's body live and returns its parsed fields, but persisting the
// raw bytes (and linking them by digest) is out of scope here.
type Resources struct {
	roots   []*url.URL
	resolve resolveFunc
}

var _ server.ResourceProvider = (*Resources)(nil)

// newResources builds a provider over the given root endpoints, wired to
// the real plugin registry.
func newResources(roots []*url.URL) *Resources {
	return &Resources{
		roots:   roots,
		resolve: command_components.ResolveRootListerPlugin,
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

	return &protocol.ResourceReadResult{
		Contents: []protocol.ResourceContent{{
			URI:      uri,
			MimeType: mimeListing,
			Text:     string(body),
		}},
	}, nil
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
	return &protocol.ResourceReadResult{
		Contents: []protocol.ResourceContent{{
			URI:      uri,
			MimeType: mimeObject,
			Text:     string(body),
		}},
	}, true, nil
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
