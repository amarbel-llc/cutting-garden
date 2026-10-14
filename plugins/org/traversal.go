package org

import (
	"context"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// target is a resolved org:// node: its account, a ready DAV client, and
// the classified ref.
type target struct {
	acct   account
	client *davClient
	ref    nodeRef
}

func resolve(u *url.URL) (target, error) {
	ref, err := classifyURI(u)
	if err != nil {
		return target{}, err
	}
	a, ok := accountByName(ref.account)
	if !ok {
		return target{}, errors.BadRequestf("org plugin: unknown account %q", ref.account)
	}
	c, err := newDavClient(a)
	if err != nil {
		return target{}, err
	}
	return target{acct: a, client: c, ref: ref}, nil
}

// file loads the target's file (for a file or headline ref). A Depth-0
// PROPFIND fetches the current ETag first, so an unchanged file is served
// from the parse cache without a GET (D9).
func (t target) file(ctx context.Context) (*parsedFile, error) {
	etag := ""
	if entries, err := t.client.propfind(ctx, t.ref.path, "0"); err == nil && len(entries) > 0 {
		etag = entries[0].etag
	}
	return loadFile(ctx, t.client, t.acct, t.ref.path, etag)
}

// headline resolves the target's headline; a stale or unknown reference
// is a bad request naming the file, never a closest match (FDR 0026
// Story 5).
func (t target) headline(ctx context.Context) (*parsedFile, *hnode, error) {
	pf, err := t.file(ctx)
	if err != nil {
		return nil, nil, err
	}
	n, ok := pf.find(t.ref)
	if !ok {
		what := "id " + t.ref.id
		if t.ref.id == "" {
			what = "outline path " + strings.Join(t.ref.outline, " / ")
		}
		return nil, nil, errors.BadRequestf(
			"org plugin: no headline with %s in %s (renamed or moved? try a title~= search)",
			what, t.ref.path)
	}
	return pf, n, nil
}

// ListRoots lists a folder's subfolders and .org files, a file's
// top-level headlines, or a headline's children.
func (Plugin) ListRoots(ctx context.Context, node *url.URL) ([]cutting_garden_plugins.Node, error) {
	t, err := resolve(node)
	if err != nil {
		return nil, err
	}
	switch t.ref.kind {
	case kindFolder:
		entries, err := t.listFolder(ctx)
		if err != nil {
			return nil, err
		}
		nodes := make([]cutting_garden_plugins.Node, 0, len(entries))
		for _, e := range entries {
			nodes = append(nodes, entryNode(t.acct.Name, e))
		}
		return nodes, nil
	case kindFile:
		pf, err := t.file(ctx)
		if err != nil {
			return nil, err
		}
		return headlineNodes(pf, pf.top, time.Now()), nil
	default:
		pf, n, err := t.headline(ctx)
		if err != nil {
			return nil, err
		}
		return headlineNodes(pf, n.children, time.Now()), nil
	}
}

// listFolder returns the folder's immediate subfolders and .org files
// (other files are skipped by design, FDR 0026 D2), sorted.
func (t target) listFolder(ctx context.Context) ([]davEntry, error) {
	all, err := t.client.propfind(ctx, t.ref.path, "1")
	if err != nil {
		return nil, err
	}
	var out []davEntry
	for _, e := range all {
		if e.rel == t.ref.path {
			continue // the folder itself
		}
		if e.dir || strings.HasSuffix(e.rel, ".org") {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b davEntry) int { return strings.Compare(a.rel, b.rel) })
	return out, nil
}

func entryNode(acct string, e davEntry) cutting_garden_plugins.Node {
	if e.dir {
		return cutting_garden_plugins.Node{
			URI:  folderURI(acct, e.rel),
			Name: path.Base(strings.TrimSuffix(e.rel, "/")),
			Type: typeFolder,
		}
	}
	n := cutting_garden_plugins.Node{
		URI:  fileURI(acct, e.rel),
		Name: path.Base(e.rel),
		Type: typeFile,
	}
	if !e.modTime.IsZero() {
		n.Fields = map[string]any{fieldModified: e.modTime.UTC().Format(time.RFC3339)}
	}
	return n
}

func headlineNodes(pf *parsedFile, ns []*hnode, now time.Time) []cutting_garden_plugins.Node {
	out := make([]cutting_garden_plugins.Node, 0, len(ns))
	for _, n := range ns {
		out = append(out, headlineNode(pf, n, now))
	}
	return out
}
