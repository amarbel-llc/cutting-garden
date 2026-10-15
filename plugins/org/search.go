package org

import (
	"context"
	"regexp"
	"strings"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Search (RFC 0016) finds the files and headlines under req.Root whose
// text matches, searching exactly what `_body` reads (leaf.go): a file's
// whole bytes and a headline's whole subtree. Files come from the parse
// cache, so a search over a folder costs one PROPFIND per folder plus a GET
// per changed file, against one PROPFIND per headline for the walk.
// Substring matching is case-sensitive and regex is Go's RE2, both as the
// evaluator's own `*=` and `~=`. An unreadable file or subfolder is skipped
// and the result marked incomplete; a 401 or 403 fails the search.
func (Plugin) Search(
	ctx context.Context, req cutting_garden_plugins.SearchRequest,
) (cutting_garden_plugins.SearchResult, error) {
	if req.Root == nil {
		return cutting_garden_plugins.SearchResult{}, errors.BadRequestf("org plugin: search needs a root")
	}
	if req.Query == "" {
		return cutting_garden_plugins.SearchResult{}, errors.BadRequestf("org plugin: search needs a query")
	}
	var match func(string) bool
	switch req.Mode {
	case cutting_garden_plugins.SearchSubstring:
		match = func(s string) bool { return strings.Contains(s, req.Query) }
	case cutting_garden_plugins.SearchRegex:
		re, err := regexp.Compile(req.Query)
		if err != nil {
			return cutting_garden_plugins.SearchResult{}, errors.BadRequestf(
				"org plugin: invalid search regex %q: %s", req.Query, err)
		}
		match = re.MatchString
	case cutting_garden_plugins.SearchWords:
		// No word index: `_words` is refused for org; use `_body*=`.
		return cutting_garden_plugins.SearchResult{}, cutting_garden_plugins.ErrSearchModeUnsupported
	default:
		return cutting_garden_plugins.SearchResult{}, errors.BadRequestf(
			"org plugin: unknown search mode %q", req.Mode)
	}
	t, err := resolve(req.Root)
	if err != nil {
		return cutting_garden_plugins.SearchResult{}, err
	}

	s := &orgSearch{match: match, filter: req.Filter, limit: req.Limit, now: time.Now(), complete: true}
	switch t.ref.kind {
	case kindFolder:
		err = s.folder(ctx, t)
	case kindFile:
		var pf *parsedFile
		if pf, err = t.file(ctx); err == nil {
			s.file(fileNodeFor(t.acct.Name, pf.rel), pf)
		}
	default:
		pf, n, herr := t.headline(ctx)
		if err = herr; err == nil {
			s.headlines(pf, []*hnode{n})
		}
	}
	if err != nil {
		return cutting_garden_plugins.SearchResult{}, err
	}
	return cutting_garden_plugins.SearchResult{Nodes: s.nodes, Complete: s.complete}, nil
}

var _ cutting_garden_plugins.Searcher = (*Plugin)(nil)

type orgSearch struct {
	match    func(string) bool
	filter   cutting_garden_plugins.FacetFilter
	limit    int
	now      time.Time
	nodes    []cutting_garden_plugins.Node
	complete bool
	full     bool // the limit was reached
}

func (s *orgSearch) add(n cutting_garden_plugins.Node, text []byte) {
	if s.full || !s.match(string(text)) || !s.filter.Matches(n.Facets) {
		return
	}
	if s.limit > 0 && len(s.nodes) == s.limit {
		s.full, s.complete = true, false
		return
	}
	s.nodes = append(s.nodes, n)
}

func (s *orgSearch) folder(ctx context.Context, t target) error {
	entries, err := t.listFolder(ctx)
	if err != nil {
		return s.tolerate(ctx, err)
	}
	for _, e := range entries {
		if e.dir {
			sub := t
			sub.ref = nodeRef{account: t.ref.account, kind: kindFolder, path: e.rel, nth: 1}
			if err := s.folder(ctx, sub); err != nil {
				return err
			}
			continue
		}
		pf, err := loadFile(ctx, t.client, t.acct, e.rel, e.etag)
		if err != nil {
			if err := s.tolerate(ctx, err); err != nil {
				return err
			}
			continue
		}
		s.file(entryNode(t.acct.Name, e), pf)
	}
	return nil
}

func (s *orgSearch) file(node cutting_garden_plugins.Node, pf *parsedFile) {
	s.add(node, pf.file.Bytes())
	s.headlines(pf, pf.top)
}

func (s *orgSearch) headlines(pf *parsedFile, ns []*hnode) {
	eachHeadline(ns, func(n *hnode) {
		s.add(headlineNode(pf, n, s.now), pf.file.Source(n.h.SubtreeSpan))
	})
}

// tolerate skips an unreadable file or folder, marking the result
// incomplete; cancellation and credential failures still fail.
func (s *orgSearch) tolerate(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if isAuthFailure(err) {
		return err
	}
	s.complete = false
	return nil
}

// fileNodeFor is a file's node when the search root is the file itself.
func fileNodeFor(acct, rel string) cutting_garden_plugins.Node {
	return entryNode(acct, davEntry{rel: rel})
}
