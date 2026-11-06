package mcp

import "time"

// maxRefreshBackoff caps how long a failing entry waits between background
// refreshes.
const maxRefreshBackoff = time.Hour

// refreshBackoff is how long an entry whose last `failures` consecutive
// background refreshes failed waits before the refresher tries it again
// (cutting-garden#393): one refresh interval, doubling per further failure,
// capped at maxRefreshBackoff. Without it a failing plugin (a GitHub 403 on
// an exhausted quota) was retried on every tick for every cached entry.
func refreshBackoff(failures int) time.Duration {
	backoff := facetRefreshInterval
	for i := 1; i < failures && backoff < maxRefreshBackoff; i++ {
		backoff *= 2
	}
	return min(backoff, maxRefreshBackoff)
}
