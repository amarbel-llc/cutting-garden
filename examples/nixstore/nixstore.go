// Package nixstore is a compile-checked reference implementation of an
// out-of-tree cutting-garden plugin built against the public plugin SDK
// (RFC 0009). It models the nix-binary-cache metadata layer discussed in
// circus#5 / circus FDR-0007: each .narinfo is a leaf node, its
// References parse into closure-DAG edges, and the named GC roots are the
// RootProvider roots.
//
// It imports ONLY code.linenisgreat.com/cutting-garden/pkgs/* — never
// internal/ — so its mere compilation proves the SDK is sufficient for an
// external consumer (the self-consumption forcing function, RFC 0009 §4).
// The method bodies are skeletons: they show the shape the real plugin
// (its own repo, linking the SDK) fills in, not a working cache. The
// actual narinfo storage, the store-path-hash -> madder-digest keyed
// index, and the binary-cache HTTP serving path live in that repo, not
// here.
package nixstore

import (
	"context"
	"net/url"

	cg "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// narinfoTypeTag is the leaf node type: one .narinfo record, stored
// verbatim (byte-exact, so its ed25519 signature survives) as a madder
// blob. Horizontally versioned per FDR 0014 / cutting-garden#79.
const narinfoTypeTag = "cutting_garden-nix_store-narinfo-v1"

// Plugin is the nix-store cache backend. It is read/traversal only: it
// implements Plugin + RootLister + RootProvider and registers via
// MustRegisterScheme (RFC 0005), so list/mcp discover it without it
// implementing capture/restore/diff.
type Plugin struct{}

// Compile-time proof the SDK facade is sufficient to implement the full
// traversal capability set from outside internal/.
var (
	_ cg.Plugin       = Plugin{}
	_ cg.RootLister   = Plugin{}
	_ cg.RootProvider = Plugin{}
)

func (Plugin) Schemes() []string { return []string{"nix-store"} }

// TypeTag is unused for a RootProvider-only plugin (it feeds the EntryV1
// store-group receipt path, which this plugin does not use), but the SDK
// still requires a value.
func (Plugin) TypeTag() string { return narinfoTypeTag }

// Types declares the one leaf node type. text/x-nix-narinfo is the
// verbatim narinfo body's media type.
func (Plugin) Types() []cg.NodeType {
	return []cg.NodeType{{
		Tag:       narinfoTypeTag,
		Container: false,
		MimeType:  "text/x-nix-narinfo",
	}}
}

// ListRoots enumerates the immediate children of node. For a nix cache
// the DAG IS the traversal: the children of a narinfo node are the store
// paths in its References field, resolved through the keyed
// store-path-hash -> madder-digest index. "Is the full closure present?"
// reduces to walking ListRoots and resolving every ref.
//
// Skeleton: the real impl loads the narinfo addressed by node, parses
// References, and returns one cg.Node per dependency store path. A leaf
// with no resolvable refs returns no children.
func (Plugin) ListRoots(ctx context.Context, node *url.URL) ([]cg.Node, error) {
	// TODO(nix_store repo): load narinfo for node from the keyed index,
	// parse its References, return one cg.Node{URI, Name, Type:
	// narinfoTypeTag} per dependency store path.
	return nil, nil
}

// Roots returns the named GC roots — the top-level pinned narinfos (e.g.
// "latest nixos-system", "latest dodder devshell"). Everything reachable
// from them via ListRoots is retained; the rest is reclaimable. URLs MUST
// be credential-free (RFC 0007).
//
// Skeleton: the real impl reads the configured root set.
func (Plugin) Roots(ctx context.Context) ([]*url.URL, error) {
	// TODO(nix_store repo): return the configured named GC roots as
	// nix-store://<store-path-hash> URIs.
	return nil, nil
}

// init registers the plugin under its scheme. A consumer binary
// blank-imports this package to fire it (see
// examples/nixstore/cmd/cutting-garden-nixstore). MustRegisterScheme is
// the discovery path for a plugin implementing none of
// capture/restore/diff (RFC 0005, RFC 0009 §3).
func init() {
	cg.MustRegisterScheme(Plugin{})
}
