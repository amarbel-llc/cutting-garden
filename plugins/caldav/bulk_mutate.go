package caldav

import (
	"bytes"
	"context"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

var _ cutting_garden_plugins.BulkMutator = (*Plugin)(nil)

// BulkMutate is caldav's best-effort BulkMutator (RFC 0017,
// cutting-garden#191). CalDAV offers no multi-object transaction, so
// caldav advertises bulk-mutate only (never bulk-atomic) and REJECTS an
// atomic request with ErrBulkAtomicUnsupported rather than silently
// downgrading. Best-effort applies each op via the SAME per-verb
// NodeMutator logic — so the per-node write contract (strict create,
// full-replace put, #182 patch reporting, delete) is identical to a
// single-node call — recording AppliedNodes / PatchedNothing / Failed per
// node. A sweep resolves its match set with the plugin's own ListEnriched
// (the RFC 0012 §6 filter over Root's children) and applies the op
// template to each match.
func (p Plugin) BulkMutate(
	ctx context.Context, req cutting_garden_plugins.BulkRequest,
) (cutting_garden_plugins.BulkResult, error) {
	if req.Atomicity == cutting_garden_plugins.BulkAtomic {
		return cutting_garden_plugins.BulkResult{},
			cutting_garden_plugins.ErrBulkAtomicUnsupported
	}

	if req.Sweep != nil {
		return p.bulkSweep(ctx, req.Sweep)
	}

	return p.bulkOps(ctx, req.Ops)
}

func (p Plugin) bulkOps(
	ctx context.Context, ops []cutting_garden_plugins.BulkOp,
) (cutting_garden_plugins.BulkResult, error) {
	var result cutting_garden_plugins.BulkResult
	for _, op := range ops {
		p.applyBulkOp(ctx, op, &result)
	}

	return result, nil
}

func (p Plugin) bulkSweep(
	ctx context.Context, sweep *cutting_garden_plugins.BulkSweep,
) (cutting_garden_plugins.BulkResult, error) {
	matches, ok, err := p.ListEnriched(ctx, sweep.Root, sweep.Filter)
	if err != nil {
		return cutting_garden_plugins.BulkResult{}, err
	}
	if !ok {
		// ListEnriched declined: sweep.Root is a calendar-HOME whose
		// immediate children are calendar CONTAINERS, not the objects a
		// FacetFilter selects. The READ path degrades such a decline to a
		// plain ListRoots (list_nodes' host-side/no-filter fallback) — but a
		// MUTATION must never silently widen its scope from "matching" to
		// "every child", so a sweep REFUSES here rather than returning a
		// misleading empty success (the decline was NOT "nothing matched")
		// or, worse, applying the op to every calendar. Sweep each calendar
		// under the home instead.
		return cutting_garden_plugins.BulkResult{}, errors.BadRequestf(
			"caldav plugin: cannot sweep %q: it is a calendar-home whose"+
				" children are calendars, not objects — sweep each calendar"+
				" under it instead", sweep.Root,
		)
	}

	var result cutting_garden_plugins.BulkResult
	for _, match := range matches {
		op := sweep.Op
		op.URI = match.URI
		p.applyBulkOp(ctx, op, &result)
	}

	return result, nil
}

// applyBulkOp applies one op via the matching NodeMutator verb and records
// the outcome: AppliedNodes on success, PatchedNothing when a patch was
// accepted but landed no recognized field (#182), Failed (never a returned
// error, in best-effort mode) otherwise.
func (p Plugin) applyBulkOp(
	ctx context.Context,
	op cutting_garden_plugins.BulkOp,
	result *cutting_garden_plugins.BulkResult,
) {
	var (
		patchedNothing bool
		err            error
	)

	switch op.Kind {
	case cutting_garden_plugins.BulkCreate:
		err = p.CreateNode(ctx, op.URI, bytes.NewReader(op.Body), op.Type)
	case cutting_garden_plugins.BulkPut:
		err = p.PutNode(ctx, op.URI, bytes.NewReader(op.Body))
	case cutting_garden_plugins.BulkPatch:
		var applied []string
		applied, err = p.PatchNode(ctx, op.URI, bytes.NewReader(op.Body))
		patchedNothing = err == nil && len(applied) == 0
	case cutting_garden_plugins.BulkDelete:
		err = p.DeleteNode(ctx, op.URI)
	default:
		err = errors.BadRequestf(
			"caldav plugin: unknown bulk op kind %q", op.Kind,
		)
	}

	switch {
	case err != nil:
		result.Failed = append(
			result.Failed, cutting_garden_plugins.BulkFailure{
				URI: op.URI, Err: err.Error(),
			},
		)
	case patchedNothing:
		result.PatchedNothing = append(result.PatchedNothing, op.URI)
	default:
		result.AppliedNodes = append(result.AppliedNodes, op.URI)
	}
}
