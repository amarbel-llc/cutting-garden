package jira

import (
	"context"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

var _ cutting_garden_plugins.BulkMutator = (*Plugin)(nil)

// BulkMutate is jira's best-effort BulkMutator (RFC 0017), delegating to the
// shared best-effort dispatch helper (cutting-garden#197). Jira Cloud has no
// cross-issue transaction, so the helper rejects an atomic request with
// ErrBulkAtomicUnsupported (reject-never-downgrade); a changeset applies each
// op via jira's own NodeMutator verbs (a bulk create is as strict as
// node.create, a bulk patch reports #182 present-empty, a status change rides
// the transition workflow); and a sweep resolves its match set via jira's
// ListEnriched — a single JQL search over a project's issues, REFUSING a
// host-root decline rather than sweeping across projects.
func (p Plugin) BulkMutate(
	ctx context.Context, req cutting_garden_plugins.BulkRequest,
) (cutting_garden_plugins.BulkResult, error) {
	return cutting_garden_plugins.BestEffortBulkMutate(ctx, p, p, req)
}
