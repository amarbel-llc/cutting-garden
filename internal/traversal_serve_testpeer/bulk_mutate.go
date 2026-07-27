package traversal_serve_testpeer

import (
	"context"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// BulkMutate is the testpeer's best-effort BulkMutator (RFC 0017,
// cutting-garden#196), delegating to the shared best-effort dispatch helper
// (cutting-garden#197) exactly as the linked reference plugins (caldav, jira)
// do — so the wire round-trip exercises a peer INDISTINGUISHABLE from a
// linked BulkMutator. Best-effort ONLY: the helper rejects an atomic request
// with the sentinel (reject-never-downgrade), dispatches each op through the
// TreePlugin's own NodeMutator verbs (so a bulk create is as strict as
// node.create, a bulk patch reports #182 present-empty as PatchedNothing),
// and resolves a sweep via the TreePlugin's own ListEnriched.
func (p *TreePlugin) BulkMutate(
	ctx context.Context, req cutting_garden_plugins.BulkRequest,
) (cutting_garden_plugins.BulkResult, error) {
	return cutting_garden_plugins.BestEffortBulkMutate(ctx, p, p, req)
}
