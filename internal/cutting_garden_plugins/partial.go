package cutting_garden_plugins

import (
	"context"
	"net/url"
	"slices"
	"sync"
)

// PartialListing records that a container's listing returned a bounded
// subset of its children, and why — e.g. caldav lists a calendar's events
// only within a date window (cutting-garden#176/#177). A listing carries no
// completeness field of its own, so without this an empty or short result is
// indistinguishable from "there is nothing more" (cutting-garden#266).
type PartialListing struct {
	URI    string `json:"uri"`
	Reason string `json:"reason"`
}

// PartialListings collects the PartialListing reports made during one host
// operation. It is safe for concurrent use.
type PartialListings struct {
	mu    sync.Mutex
	notes []PartialListing
}

type partialListingsKey struct{}

// WithPartialListings returns a context that collects the partial-listing
// reports plugins make while serving it, and the collector to read them from
// afterwards. A host wraps the context of every listing it serves.
func WithPartialListings(ctx context.Context) (context.Context, *PartialListings) {
	p := &PartialListings{}
	return context.WithValue(ctx, partialListingsKey{}, p), p
}

// ReportPartialListing is called by a plugin while listing u when the listing
// is a bounded subset of u's children. It is a no-op when the host collects
// nothing, and a repeated report is recorded once.
func ReportPartialListing(ctx context.Context, u *url.URL, reason string) {
	p, _ := ctx.Value(partialListingsKey{}).(*PartialListings)
	if p == nil || u == nil {
		return
	}
	note := PartialListing{URI: u.String(), Reason: reason}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !slices.Contains(p.notes, note) {
		p.notes = append(p.notes, note)
	}
}

// All returns the reports made so far, in report order; nil when none.
func (p *PartialListings) All() []PartialListing {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.notes)
}
