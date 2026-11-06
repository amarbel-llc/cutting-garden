package mcp

import (
	"net/url"
	"slices"
	"time"
)

// backgroundRefreshOff reports, for a cached URI, whether its scheme is
// opted out of background maintenance (`[mcp] no_background_refresh`,
// cutting-garden#393): such an entry is neither warmed at startup nor
// refreshed on the timer, and refreshes lazily on a read past the TTL
// instead. "*" opts out every scheme; an empty list opts out none.
func backgroundRefreshOff(schemes []string) func(uri string) bool {
	if len(schemes) == 0 {
		return nil
	}
	all := slices.Contains(schemes, "*")
	return func(uri string) bool {
		if all {
			return true
		}
		u, err := url.Parse(uri)
		return err == nil && slices.Contains(schemes, u.Scheme)
	}
}

// staleForLazyRead reports whether a lazily refreshed entry computed at
// computedAt must be recomputed by the read at now.
func staleForLazyRead(lazy func(string) bool, uri string, computedAt, now time.Time) bool {
	return lazy != nil && lazy(uri) && now.Sub(computedAt) >= facetTTL
}
