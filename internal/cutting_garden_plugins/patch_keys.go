package cutting_garden_plugins

import (
	"net/url"
	"slices"
)

// NodeTypePatchKeys is the static set of patch body keys one node type
// accepts: the keys a patch to a node of that type can apply. A key outside it
// is one the plugin ignores.
type NodeTypePatchKeys struct {
	// Tag is the NodeType.Tag this describes.
	Tag string
	// Keys are the accepted top-level patch body keys, sorted.
	Keys []string
}

// PatchKeysDescriber is the OPTIONAL capability a write-capable plugin
// implements to declare, per node type, the patch keys it accepts
// (cutting-garden#404). The host validates every patch body against the
// declaration before dispatch: by default it strips an undeclared key and
// reports it as ignored; for a strict call it refuses the op before anything
// is written. The plugin never sees an undeclared key, nor the strict flag.
//
// The declaration is static per node type. A key whose acceptance depends on
// the node's state or the value sent (ynab refusing a category on a split
// transaction) stays the plugin's to check at write time.
//
// It is probed by type assertion, like BodyDescriber. A plugin that omits it
// gets no host validation: its patches are dispatched as sent, and a strict
// call refuses them as unverifiable.
type PatchKeysDescriber interface {
	Plugin

	// DescribePatchKeys returns one NodeTypePatchKeys per patchable node type.
	DescribePatchKeys() []NodeTypePatchKeys

	// PatchTarget answers which node type a patch to uri targets, from the
	// URI alone (no network call). ok is false when it cannot tell. The host
	// asks only when no node type's URITemplate matches uri (RFC 0018 §4), so a
	// plugin whose types all declare a template can return ok == false.
	PatchTarget(uri *url.URL) (tag string, ok bool)
}

// ResolvePatchKeys answers, for a patch to uri, the node type's tag and its
// declared patch keys: the type comes from the declared URITemplates when one
// matches, else from PatchTarget. ok is false when lister does not implement
// PatchKeysDescriber, the type cannot be told, or it declares no patch keys.
func ResolvePatchKeys(
	lister RootLister, uri *url.URL,
) (tag string, keys []string, ok bool) {
	describer, isDescriber := lister.(PatchKeysDescriber)
	if !isDescriber || uri == nil {
		return "", nil, false
	}

	if resolved, found := ResolveNodeTypeByURI(lister, uri.String()); found {
		tag = resolved.Type.Tag
	} else if targeted, found := describer.PatchTarget(uri); found {
		tag = targeted
	} else {
		return "", nil, false
	}

	for _, declared := range describer.DescribePatchKeys() {
		if declared.Tag == tag {
			return tag, slices.Clone(declared.Keys), true
		}
	}

	return "", nil, false
}
