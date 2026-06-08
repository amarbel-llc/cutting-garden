package cutting_garden_plugins

import (
	"context"
	"net/url"
)

// NodeType is one entry in a RootLister plugin's declared type list. It
// is the unit of self-description for the traversal tree: a Node names
// its type by Tag, and a consumer resolves that Tag against the
// plugin's Types() to learn whether the node can be descended and which
// format version it speaks.
//
// Tag is a hyphenated, horizontally-versioned identifier in the
// madder/dodder scheme (e.g. "cutting_garden-caldav-calendar-v1"). The
// declared list is the plugin's backwards-compatibility surface: a
// format change adds a "-v2" entry while the "-v1" entry stays
// readable, so a consumer built against -v1 keeps working when -v2
// nodes appear beside it (see amarbel-llc/cutting-garden#79).
type NodeType struct {
	// Tag is the hyphenated, horizontally-versioned type identifier.
	Tag string
	// Container is true when nodes of this type can be descended (have
	// children) and false for leaves — capturable objects with none.
	Container bool
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
