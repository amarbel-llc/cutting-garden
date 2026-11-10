package mcp

import (
	"hash/fnv"
	"time"
)

// maxRefreshBackoff caps the base backoff (refreshBackoff) of a failing entry.
// The per-URI spread refreshBackoffFor adds on top can take it up to 25% past
// the cap.
const maxRefreshBackoff = time.Hour

// refreshBackoffFor is how long the entry at uri, whose last `failures`
// consecutive background refreshes failed, waits before the refresher tries it
// again (cutting-garden#393): refreshBackoff plus a deterministic per-URI
// spread of up to 25%, so entries that failed together (one exhausted quota)
// do not all retry on the same tick.
func refreshBackoffFor(uri string, failures int) time.Duration {
	base := refreshBackoff(failures)
	h := fnv.New32a()
	_, _ = h.Write([]byte(uri))
	const buckets = 1024
	return base + base/4*time.Duration(h.Sum32()%buckets)/buckets
}

// refreshBackoff is the un-spread backoff: one refresh interval, doubling per
// further failure, capped at maxRefreshBackoff. Without it a failing plugin
// (a GitHub 403 on an exhausted quota) was retried on every tick for every
// cached entry.
func refreshBackoff(failures int) time.Duration {
	backoff := facetRefreshInterval
	for i := 1; i < failures && backoff < maxRefreshBackoff; i++ {
		backoff *= 2
	}
	return min(backoff, maxRefreshBackoff)
}
