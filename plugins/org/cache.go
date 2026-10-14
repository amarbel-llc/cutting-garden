package org

import (
	"context"
	"net/url"
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

func resetCache() {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cache = map[string]*parsedFile{}
}

// loadFile returns rel parsed. knownETag, when non-empty (from a folder
// PROPFIND), lets a cache hit skip the GET entirely.
func loadFile(ctx context.Context, c *davClient, a account, rel, knownETag string) (*parsedFile, error) {
	key := a.Name + "\x00" + rel
	if knownETag != "" {
		cacheMu.Lock()
		hit := cache[key]
		cacheMu.Unlock()
		if hit != nil && hit.etag == knownETag {
			return hit, nil
		}
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
	}
	return pf, nil
}

func parseFile(a account, rel, etag string, body []byte) *parsedFile {
	f := orgtext.Parse(body, orgtext.Options{
		Keywords:  a.keywords,
		NoInherit: []string{agentQuotedTag},
	})
	pf := &parsedFile{rel: rel, etag: etag, file: f}
	heads := f.Headlines
	if len(heads) == 0 {
		heads = []*orgtext.Headline{f.SynthesizeHeadline(fileStem(rel))}
	}
	seen := map[string]int{} // outline path → occurrences so far
	var build func([]*orgtext.Headline) []*hnode
	build = func(hs []*orgtext.Headline) []*hnode {
		out := make([]*hnode, 0, len(hs))
		for _, h := range hs {
			pathKey := strings.Join(h.OutlinePath, "\x00")
			seen[pathKey]++
			n := &hnode{h: h, uri: headlineURI(a.Name, rel, h.ID(), h.OutlinePath, seen[pathKey])}
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
			} else if equalPath(n.h.OutlinePath, ref.outline) {
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

func equalPath(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// eachHeadline visits every headline in ns and their descendants.
func eachHeadline(ns []*hnode, visit func(*hnode)) {
	for _, n := range ns {
		visit(n)
		eachHeadline(n.children, visit)
	}
}
