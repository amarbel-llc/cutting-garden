package org

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

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

	// checked is when the server last confirmed etag; guarded by cacheMu.
	checked time.Time
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

// freshnessWindow is how long a confirmed ETag is trusted without asking
// the server again. One trellis walk lists and reads every headline of a
// file; without the window each of those costs a Depth-0 PROPFIND. Orgzly
// syncs every few minutes at best, so a few seconds of staleness is
// invisible, and this process's own quote drops the entry it wrote.
var freshnessWindow = 5 * time.Second

func cacheKey(account, rel string) string { return account + "\x00" + rel }

// freshCached returns the cached parse of rel when its ETag was confirmed
// within freshnessWindow.
func freshCached(account, rel string) (*parsedFile, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	pf := cache[cacheKey(account, rel)]
	if pf == nil || time.Since(pf.checked) >= freshnessWindow {
		return nil, false
	}
	return pf, true
}

func resetCache() {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cache = map[string]*parsedFile{}
}

// resetCacheEntry drops one file after this process wrote it.
func resetCacheEntry(account, rel string) {
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
			hit.checked = time.Now()
			cacheMu.Unlock()
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
		pf.checked = time.Now()
		cacheMu.Lock()
		cache[key] = pf
		cacheMu.Unlock()
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
