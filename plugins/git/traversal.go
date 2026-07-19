package cutting_garden_plugin_git

import (
	"context"
	"net/url"
	"sort"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/memory"
)

// typeBranch is the single node type the git traversal tree is built
// from: one leaf per remote branch. A branch has no further
// sub-structure this plugin exposes — its objects are captured as one
// merkle payload, never traversed individually — so it is a leaf, not
// a container (FDR 0014).
const typeBranch = "git-branch-v1"

// facetDefault is the only facet dimension the git binding declares:
// whether a branch is the remote's resolved default (HEAD). Its value
// is read directly off the SAME ref advertisement ListRoots already
// fetches to enumerate branches — no per-node re-fetch (RFC 0012 §1).
const facetDefault = "default"

var (
	_ cutting_garden_plugins.RootLister     = (*Plugin)(nil)
	_ cutting_garden_plugins.LeafReader     = (*Plugin)(nil)
	_ cutting_garden_plugins.FacetDescriber = (*Plugin)(nil)
	_ cutting_garden_plugins.FacetCounter   = (*Plugin)(nil)
)

// Types declares the git binding's single traversal node type — a
// leaf per branch, opaque body (a branch's "content" is a merkle tree
// of objects, not a single blob, so MimeType is left unspecified).
func (Plugin) Types() []cutting_garden_plugins.NodeType {
	return []cutting_garden_plugins.NodeType{
		{Tag: typeBranch, Container: false},
	}
}

// DescribeFacets declares the "default" dimension on a branch leaf: a
// closed two-value categorical, so a repo with no non-default branches
// still renders an informative zero (RFC 0012 §2/§3).
func (Plugin) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	return []cutting_garden_plugins.NodeTypeFacets{
		{
			Tag: typeBranch,
			Dimensions: []cutting_garden_plugins.FacetDimension{
				{
					Key:   facetDefault,
					Label: "Default branch",
					Kind:  cutting_garden_plugins.FacetCategorical,
					Values: []cutting_garden_plugins.FacetValue{
						{Key: "true"},
						{Key: "false"},
					},
				},
			},
		},
	}
}

// ListRoots enumerates a git endpoint's branches over the wire, with
// no object transfer — the same underlying go-git list-remote call
// listRemoteTip (remote.go) uses for the diff freshness probe,
// generalized here to return every branch instead of resolving one.
//
// node is either the bare endpoint (no #fragment) — a container whose
// children are one leaf Node per remote branch — or an already
// branch-scoped URI (a leaf, per remoteAndBranchFromArg), which has no
// children.
//
// Tags are deliberately NOT enumerated. CaptureProtocol resolves a
// node's #fragment as a BRANCH reference only
// (remoteAndBranchFromArg -> plumbing.NewBranchReferenceName), so a
// listed tag node's URI would not round-trip through
// `capture <node.URI>` per FDR 0014's per-node capture-root contract
// ("URI re-classifies as a capture root"). Surfacing only what capture
// can actually re-resolve keeps that contract exact; capturing tags is
// a capture-side feature gap, not a traversal gap (see plugins/git's
// audit notes).
func (Plugin) ListRoots(
	ctx context.Context, node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	if node == nil {
		return nil, errors.ErrorWithStackf(
			"git plugin: ListRoots requires a node URI",
		)
	}
	remote, branch, err := remoteAndBranchFromArg(node)
	if err != nil {
		return nil, err
	}
	if branch != "" {
		// A branch-scoped node is a leaf: no children.
		return nil, nil
	}

	branches, defaultBranch, err := listRemoteBranches(ctx, remote)
	if err != nil {
		return nil, err
	}

	nodes := make([]cutting_garden_plugins.Node, 0, len(branches))
	for _, name := range branches {
		nodes = append(nodes, cutting_garden_plugins.Node{
			URI:  branchNodeURI(remote, name),
			Name: name,
			Type: typeBranch,
			Facets: map[string][]cutting_garden_plugins.FacetValue{
				facetDefault: {{Key: boolFacetKey(name == defaultBranch)}},
			},
		})
	}
	return nodes, nil
}

// ReadLeaf fetches a branch leaf's cheap identity: its resolved tip
// oid, via the SAME no-object-transfer ref advertisement diff's
// freshness probe uses (listRemoteTip). node MUST name a branch (a
// non-empty #fragment); the bare endpoint is a container, not a leaf,
// so ok is false for it. There is no verbatim "Raw" form to offer — a
// branch's content is the merkle tree CaptureProtocol builds, not a
// single fetchable body — so only the Structured view is populated.
func (Plugin) ReadLeaf(
	ctx context.Context, node *url.URL,
) (cutting_garden_plugins.LeafContent, bool, error) {
	if node == nil {
		return cutting_garden_plugins.LeafContent{}, false, errors.ErrorWithStackf(
			"git plugin: ReadLeaf requires a node URI",
		)
	}
	remote, branch, err := remoteAndBranchFromArg(node)
	if err != nil {
		return cutting_garden_plugins.LeafContent{}, false, err
	}
	if branch == "" {
		// The bare endpoint is a container, not a leaf.
		return cutting_garden_plugins.LeafContent{}, false, nil
	}

	resolvedBranch, tip, err := listRemoteTip(ctx, remote, branch)
	if err != nil {
		return cutting_garden_plugins.LeafContent{}, false, err
	}

	return cutting_garden_plugins.LeafContent{
		Structured: map[string]string{
			"remote": remote,
			"branch": resolvedBranch,
			"tip":    tip,
		},
	}, true, nil
}

// FacetCounts summarizes an endpoint's branches in one shot, reusing the
// SAME listRemoteBranches call ListRoots makes (RFC 0012 §4.1's preferred
// one-shot path, size-agnostic and no extra wire round trip beyond what
// browsing already pays). Declines (ok=false) for an already branch-scoped
// leaf node, which has no facets of its own — mirroring ListRoots's
// leaf-has-no-children posture. The result is always Complete: one
// list-remote call enumerates every branch, with no source-imposed cap.
func (Plugin) FacetCounts(
	ctx context.Context,
	node *url.URL,
	filter cutting_garden_plugins.FacetFilter,
) (cutting_garden_plugins.FacetResult, bool, error) {
	if node == nil {
		return cutting_garden_plugins.FacetResult{}, false, errors.ErrorWithStackf(
			"git plugin: FacetCounts requires a node URI",
		)
	}
	remote, branch, err := remoteAndBranchFromArg(node)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, err
	}
	if branch != "" {
		// A branch-scoped leaf has no facets of its own.
		return cutting_garden_plugins.FacetResult{}, false, nil
	}

	branches, defaultBranch, err := listRemoteBranches(ctx, remote)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, err
	}

	// Informative zeros (RFC 0012 §3): facetDefault is a closed two-value
	// domain, so both buckets are present even when every branch (or none)
	// is the default.
	hist := cutting_garden_plugins.FacetHistogram{"true": 0, "false": 0}
	for _, name := range branches {
		key := boolFacetKey(name == defaultBranch)
		if !filter.Matches(map[string][]cutting_garden_plugins.FacetValue{
			facetDefault: {{Key: key}},
		}) {
			continue
		}
		hist[key]++
	}

	return cutting_garden_plugins.FacetResult{
		Summary:  cutting_garden_plugins.FacetSummary{facetDefault: hist},
		Complete: true,
	}, true, nil
}

// branchNodeURI builds the node URI a listed branch capture-roots to:
// the opaque `git:<remote>#<branch>` form, mirroring canonicalSource's
// fragment convention (url.go). Emitting the opaque form unconditionally
// (rather than preserving a hierarchical `git://host/path` remote's own
// shape) is deliberate: net/url round-trips an opaque payload exactly
// regardless of what it contains — including one that itself begins
// with "git://" — whereas re-deriving the hierarchical form here would
// duplicate remoteAndBranchFromArg's parsing rules. The result still
// re-parses to the identical (remote, branch) pair via
// remoteAndBranchFromArg, which is the only round-trip FDR 0014
// requires; it need not match the argument syntax a user originally
// typed.
func branchNodeURI(remote, branch string) *url.URL {
	return &url.URL{Scheme: "git", Opaque: remote, Fragment: branch}
}

// boolFacetKey renders a bool as the facet Key strings declared in
// DescribeFacets's closed "default" domain.
func boolFacetKey(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// listRemoteBranches lists every branch head at remote and resolves
// the default branch's name, in one wire round trip with no object
// transfer. Shares its underlying mechanism with listRemoteTip
// (remote.go) — a bare `git.NewRemote(...).ListContext` ref
// advertisement — generalized to collect every refs/heads/* entry
// instead of resolving a single named (or HEAD-implied) branch.
func listRemoteBranches(
	ctx context.Context, remote string,
) (branches []string, defaultBranch string, err error) {
	auth, err := authMethod(remote)
	if err != nil {
		return nil, "", err
	}
	rem := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{
		Name: "origin",
		URLs: []string{remote},
	})
	refs, err := rem.ListContext(ctx, &git.ListOptions{Auth: auth})
	if err != nil {
		return nil, "", errors.Wrapf(err, "git plugin: list-remote %s", remote)
	}

	var headTarget plumbing.ReferenceName
	for _, r := range refs {
		switch {
		case r.Name().IsBranch():
			branches = append(branches, r.Name().Short())
		case r.Name() == plumbing.HEAD && r.Type() == plumbing.SymbolicReference:
			headTarget = r.Target()
		}
	}
	if headTarget != "" {
		defaultBranch = headTarget.Short()
	}
	sort.Strings(branches)
	return branches, defaultBranch, nil
}
