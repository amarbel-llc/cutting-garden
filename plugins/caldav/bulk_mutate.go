package caldav

import (
	"context"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

var _ cutting_garden_plugins.BulkMutator = (*Plugin)(nil)

// BulkMutate is caldav's best-effort BulkMutator (RFC 0017,
// cutting-garden#191), delegating to the shared best-effort dispatch helper
// (cutting-garden#197). CalDAV offers no multi-object transaction, so the
// helper rejects an atomic request with ErrBulkAtomicUnsupported rather than
// downgrading; a changeset applies each op via caldav's own NodeMutator
// verbs — so the per-node write contract (strict create, full-replace put,
// #182 patch reporting, delete) is identical to a single-node call; and a
// sweep resolves its match set via caldav's ListEnriched (the RFC 0012 §6
// filter over Root's children), REFUSING a calendar-home decline rather than
// widening scope from "matching" to "every calendar".
func (p Plugin) BulkMutate(
	ctx context.Context, req cutting_garden_plugins.BulkRequest,
) (cutting_garden_plugins.BulkResult, error) {
	return cutting_garden_plugins.BestEffortBulkMutate(ctx, p, p, req)
}
