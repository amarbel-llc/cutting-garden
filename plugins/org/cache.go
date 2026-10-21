package org

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/org/orgtext"
)

// agentQuotedTag never inherits (FDR 0026 D4/D12): quoting a parent
// headline must not mark its whole subtree.
const agentQuotedTag = "zz-agent_quoted"

// parsedFile is one fetched, parsed .org file plus its headline index.
type parsedFile struct {
	rel     string
	etag    string
	modTime time.Time
	file    *orgtext.File
	top     []*hnode // top-level headlines (or the synthesized one)
}

// hnode is one headline with its minted URI.
type hnode struct {
	h        *orgtext.Headline
	uri      *url.URL
	children []*hnode
}

// The ETag-keyed parse cache (FDR 0026 D9): a file is re-fetched and
// re-parsed only when its ETag moves. Keyed by account + relative path.
var (
	cacheMu sync.Mutex
	cache   = map[string]*parsedFile{}
)

func cacheKey(account, rel string) string { return account + "\x00" + rel }

// confirmKey names a file's ETag confirmation in the host call's scope.
func confirmKey(account, rel string) string { return "org\x00" + account + "\x00" + rel }

// freshCached returns the cached parse of rel when its ETag was confirmed
// against the server during THIS host call (cutting-garden#311). One trellis
// walk lists and reads every headline of a file; without this each of those
// costs a Depth-0 PROPFIND. The boundary is the call, not a clock: the walk's
// caller sees one coherent snapshot of the file, and the next call confirms the
// ETag again, so staleness never crosses a call (it replaces a 5 s window that
// could serve data stale across separate calls). Outside a call scope (a test, a
// direct caller) nothing is confirmed, so every load asks the server.
func freshCached(ctx context.Context, account, rel string) (*parsedFile, bool) {
	if !cutting_garden_plugins.ConfirmedThisCall(ctx, confirmKey(account, rel)) {
		return nil, false
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	pf := cache[cacheKey(account, rel)]
	return pf, pf != nil
}

func resetCache() {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cache = map[string]*parsedFile{}
}

// resetCacheEntry drops one file after this process wrote it, and withdraws
// the call's confirmation of it: the write changed what the ETag described.
func resetCacheEntry(ctx context.Context, account, rel string) {
	cutting_garden_plugins.UnconfirmForThisCall(ctx, confirmKey(account, rel))
	cacheMu.Lock()
	defer cacheMu.Unlock()
	delete(cache, cacheKey(account, rel))
}

// loadFile returns rel parsed. knownETag, when non-empty (from a
// PROPFIND), lets a cache hit skip the GET entirely; either way the entry
// counts as freshly confirmed.
func loadFile(ctx context.Context, c *davClient, a account, rel, knownETag string) (*parsedFile, error) {
	key := cacheKey(a.Name, rel)
	if knownETag != "" {
		cacheMu.Lock()
		hit := cache[key]
		if hit != nil && hit.etag == knownETag {
			cacheMu.Unlock()
			// The server just reported this ETag: confirmed for this call.
			cutting_garden_plugins.ConfirmForThisCall(ctx, confirmKey(a.Name, rel))
			return hit, nil
		}
		cacheMu.Unlock()
	}
	body, etag, err := c.get(ctx, rel)
	if err != nil {
		return nil, err
	}
	pf := parseFile(a, rel, etag, body)
	if etag != "" {
		cacheMu.Lock()
		cache[key] = pf
		cacheMu.Unlock()
		// Fetched just now, so confirmed for this call.
		cutting_garden_plugins.ConfirmForThisCall(ctx, confirmKey(a.Name, rel))
	}
	return pf, nil
}

func parseFile(a account, rel, etag string, body []byte) *parsedFile {
	f := orgtext.Parse(body, orgtext.Options{
		Keywords:  a.keywords,
		NoInherit: []string{agentQuotedTag, agentCapturedTag},
	})
	pf := &parsedFile{rel: rel, etag: etag, file: f}
	heads := f.Headlines
	if len(heads) == 0 {
		heads = []*orgtext.Headline{f.SynthesizeHeadline(fileStem(rel))}
	}
	seen := map[string]int{}    // outline path → occurrences so far
	seenID := map[string]bool{} // a duplicated :ID: (copy-pasted headline)
	var build func([]*orgtext.Headline) []*hnode
	build = func(hs []*orgtext.Headline) []*hnode {
		out := make([]*hnode, 0, len(hs))
		for _, h := range hs {
			pathKey := strings.Join(h.OutlinePath, "\x00")
			seen[pathKey]++
			// Only the first holder of an :ID: is addressed by it; a copy
			// falls back to its outline path so it stays reachable.
			id := h.ID()
			if id != "" && seenID[id] {
				id = ""
			}
			seenID[h.ID()] = true
			n := &hnode{h: h, uri: headlineURI(a.Name, rel, id, h.OutlinePath, seen[pathKey])}
			n.children = build(h.Children)
			out = append(out, n)
		}
		return out
	}
	pf.top = build(heads)
	return pf
}

// find resolves a headline ref within the file: by :ID:, or by outline
// path plus ordinal.
func (pf *parsedFile) find(ref nodeRef) (*hnode, bool) {
	var found *hnode
	count := 0
	var walk func([]*hnode) bool
	walk = func(ns []*hnode) bool {
		for _, n := range ns {
			if ref.id != "" {
				if n.h.ID() == ref.id {
					found = n
					return true
				}
			} else if slices.Equal(n.h.OutlinePath, ref.outline) {
				count++
				if count == ref.nth {
					found = n
					return true
				}
			}
			if walk(n.children) {
				return true
			}
		}
		return false
	}
	walk(pf.top)
	return found, found != nil
}

// eachHeadline visits every headline in ns and their descendants.
func eachHeadline(ns []*hnode, visit func(*hnode)) {
	for _, n := range ns {
		visit(n)
		eachHeadline(n.children, visit)
	}
}
