package cutting_garden_plugins

import (
	"context"
	"net/url"
)

// MimeTypeDefault is the mimetype a leaf NodeType speaks when its
// declaration leaves MimeType empty: opaque bytes, dodder's null-type
// posture. Consumers MUST apply this default rather than propagating
// the empty string.
const MimeTypeDefault = "application/octet-stream"

// NodeType is one entry in a RootLister plugin's declared type list. It
// is the unit of self-description for the traversal tree: a Node names
// its type by Tag, and a consumer resolves that Tag against the
// plugin's Types() to learn whether the node can be descended, which
// format version it speaks, and what its bytes are.
//
// Tag is a hyphenated, horizontally-versioned identifier in the
// madder/dodder scheme (e.g. "cutting_garden-caldav-calendar-v1"). The
// declared list is the plugin's backwards-compatibility surface: a
// format change adds a "-v2" entry while the "-v1" entry stays
// readable, so a consumer built against -v1 keeps working when -v2
// nodes appear beside it (see amarbel-llc/cutting-garden#79).
//
// The shape deliberately grows toward dodder's type definitions
// (dodder FDR 0010; the `!toml-type-v2` blob with binary /
// file-extension / mime-type / formatter fields), one field at a time
// as a consumer needs it — MimeType is the first.
type NodeType struct {
	// Tag is the hyphenated, horizontally-versioned type identifier.
	Tag string
	// Container is true when nodes of this type can be descended (have
	// children) and false for leaves — capturable objects with none.
	Container bool
	// MimeType is the content type of a node's body — what a capture
	// of one leaf of this type yields (e.g. "text/calendar" for a
	// CalDAV object). Empty means unspecified: consumers resolve a
	// leaf's empty MimeType to MimeTypeDefault (use BodyMimeType).
	// Containers have no body of their own (their "content" is their
	// child listing, a consumer-side rendering concern), so a
	// container's MimeType is conventionally empty and consumers MUST
	// NOT apply the leaf default to containers.
	MimeType string
}

// BodyMimeType resolves the declared MimeType per the contract: a
// leaf's empty MimeType defaults to MimeTypeDefault; a container has
// no body, so its MimeType (conventionally empty) passes through
// unchanged.
func (t NodeType) BodyMimeType() string {
	if t.MimeType == "" && !t.Container {
		return MimeTypeDefault
	}
	return t.MimeType
}

// NodeTypeFor resolves a Node.Type tag against the plugin's declared
// Types() — the one lookup every traversal consumer performs. ok is
// false for an unknown tag; the zero NodeType then behaves as a leaf of
// unspecified mimetype, so a consumer built against the declared list
// neither invents descendability nor fabricates a content type beyond
// the leaf default.
func NodeTypeFor(lister RootLister, tag string) (NodeType, bool) {
	for _, t := range lister.Types() {
		if t.Tag == tag {
			return t, true
		}
	}
	return NodeType{}, false
}

// Node is one addressable point in a RootLister plugin's capturable
// tree, as returned by ListRoots.
type Node struct {
	// URI re-classifies as a capture root: `capture <URI>` captures
	// exactly this node — one object for a leaf, the whole subtree for
	// a container (the bulk behavior a plain CaptureRoot already has).
	URI *url.URL
	// Name is a short display label (a calendar's display-name, a
	// video's title, a file's name).
	Name string
	// Type is a Tag from this plugin's Types(); resolve it there for
	// descendability and format version.
	Type string
	// Facets is the node's facet membership, keyed by FacetDimension.Key — the
	// cheap, declared values a plugin attaches during ListRoots (from data
	// already in hand, never a per-node re-fetch) so the framework can fold
	// them into a container's summary (RFC 0012 §1). nil/empty means the node
	// contributes nothing to any facet; several values under one key is a
	// multi-valued contribution. MUST be free of credentials or secrets.
	Facets map[string][]FacetValue
}

// URIString renders the node's URI, tolerating a nil URL (rendered as
// the empty string). The shared helper every traversal consumer (the
// `list` command, the MCP server) uses to project a Node's address
// into text.
func (n Node) URIString() string {
	if n.URI == nil {
		return ""
	}
	return n.URI.String()
}

// RootLister is the OPTIONAL traversal capability a Plugin implements
// when its scheme has meaningful sub-structure a user may want to
// discover, address, diff, or restore independently (a CalDAV
// endpoint's calendars, a yt-dlp channel's videos, a Drive folder's
// files). It is probed by type assertion on an already-resolved plugin,
// exactly as the orchestrator probes ProtocolCapturePlugin — there is
// no separate registry. Plugins without sub-structure (the file plugin)
// omit it, and consumers fall back to today's one-arg-one-root model.
//
// ListRoots is read-only and lazy: it returns the immediate children of
// one node, and a consumer descends a container by calling again with
// that container's URI. This hierarchical, one-level-per-call shape is
// the shape MCP `resources/list` consumes, it bounds work on large
// trees, and it lets a consumer stop at whatever depth it cares about.
//
// It is the single source of traversal truth for a plugin: capture,
// the `list` discovery command, the planned `health` command (#80), and
// a future MCP traversal server all walk the same enumeration rather
// than re-deriving the tree independently. See FDR 0014.
type RootLister interface {
	Plugin

	// Types declares every node type this plugin can emit, leaf and
	// container. The returned slice is stable for the life of the
	// plugin and is the authority for resolving a Node.Type.
	Types() []NodeType

	// ListRoots returns the immediate children of node — the URI whose
	// children to enumerate. A consumer begins a walk at the
	// user-supplied endpoint URI and descends a container child by
	// calling again with that child's URI; a leaf has no children.
	//
	// node is the addressable target, never a plugin-held cursor:
	// RootLister plugins are stateless (Plugin is a zero-size value), so
	// the node to enumerate — including the top-level endpoint — is
	// always identified by the URI passed in. node MUST be non-nil.
	//
	// Read-only: ListRoots never mutates the source and never writes to
	// a blob store.
	ListRoots(ctx context.Context, node *url.URL) ([]Node, error)
}

// RootProvider is the OPTIONAL capability of a RootLister that can
// enumerate its own top-level roots with no input node — the entry points
// a no-argument `mcp` / `list` surfaces (RFC 0007). It is probed by type
// assertion exactly as RootLister is.
//
// The source of the roots is plugin-defined and invisible to the caller:
// intrinsic ambient state (the file plugin's working directory),
// configured credentialed accounts (caldav), or configured preferred
// roots (a future web/yt-dlp plugin). A plugin that can enumerate none
// returns an empty slice — it then contributes nothing and does not appear
// in the aggregated listing.
type RootProvider interface {
	RootLister

	// Roots returns the plugin's top-level roots. Each URL MUST be
	// credential-free (no userinfo): these are surfaced to clients (e.g.
	// as MCP resource URIs). An empty slice (not an error) means the
	// plugin has no roots to offer.
	Roots(ctx context.Context) ([]*url.URL, error)
}

// LeafContent is one leaf node's fetched content, returned by ReadLeaf. It
// carries two views of the same object: a structured, JSON-marshalable
// projection a client reads (the parsed fields), and the verbatim source
// bytes (the content-addressable original).
type LeafContent struct {
	// Structured is the parsed, JSON-marshalable projection of the leaf —
	// what an MCP client reads (e.g. a caldav Event/Task). The consumer
	// marshals it to JSON. nil means the plugin offers no structured view,
	// in which case the consumer surfaces Raw directly.
	Structured any
	// Raw is the leaf's verbatim source bytes (e.g. the .ics body) — the
	// exact content-addressable form, suitable for storing as a blob and
	// linking by digest. May be nil when the plugin has no raw form to
	// offer.
	Raw []byte
	// RawMimeType is Raw's IANA content type (e.g. "text/calendar"). Empty
	// when Raw is nil.
	RawMimeType string
}

// LeafReader is the OPTIONAL capability a RootLister implements to fetch a
// single leaf node's content on demand — the per-object body-fetch backing
// MCP resources/read (cutting-garden#85). It is probed by type assertion on
// an already-resolved plugin, exactly as RootProvider is; a plugin whose
// scheme has no individually-fetchable objects omits it, and consumers fall
// back to the structure-only listing.
//
// ReadLeaf is consulted only after ListRoots reports a node has no children
// (a leaf or an empty container), so it never has to re-derive that a
// populated container is not a leaf. It is read-only: it never mutates the
// source and never writes to a blob store.
type LeafReader interface {
	RootLister

	// ReadLeaf fetches the content of leaf node, identified by URI. ok is
	// false when node is not a fetchable leaf — an empty container, or a
	// URI whose body is not retrievable or not parsable — so the consumer
	// falls back to the (empty) child listing rather than erroring. A
	// non-nil error is reserved for an unexpected failure the consumer
	// should surface. node MUST be non-nil.
	ReadLeaf(
		ctx context.Context, node *url.URL,
	) (content LeafContent, ok bool, err error)
}
