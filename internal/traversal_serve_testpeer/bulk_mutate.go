package traversal_serve_testpeer

import (
	"bytes"
	"context"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// BulkMutate is the testpeer's best-effort BulkMutator (RFC 0017,
// cutting-garden#196). It exists so the conformance driver's caseBulkMutate
// has a peer that advertises bulk-mutate, and so the wire round-trip is
// exercised end to end against a peer INDISTINGUISHABLE from a linked
// BulkMutator. Its per-op semantics are the linked reference's verbatim:
// each op runs through the SAME NodeMutator verb the single-node path uses,
// so a bulk create is as strict as node.create, a bulk patch reports #182
// present-empty as PatchedNothing, and so on.
//
// The testpeer is best-effort ONLY (it advertises bulk-mutate, never
// bulk-atomic) and so REJECTS an atomic request with
// ErrBulkAtomicUnsupported rather than downgrading — the reject-never-
// downgrade rule the conformance case pins for any peer that cannot
// transact (fj-cg among them).
func (p *TreePlugin) BulkMutate(
	ctx context.Context, req cutting_garden_plugins.BulkRequest,
) (cutting_garden_plugins.BulkResult, error) {
	if req.Atomicity == cutting_garden_plugins.BulkAtomic {
		return cutting_garden_plugins.BulkResult{},
			cutting_garden_plugins.ErrBulkAtomicUnsupported
	}

	if req.Sweep != nil {
		return p.bulkSweep(ctx, req.Sweep)
	}

	var result cutting_garden_plugins.BulkResult
	for _, op := range req.Ops {
		p.applyBulkOp(ctx, op, &result)
	}

	return result, nil
}

func (p *TreePlugin) bulkSweep(
	ctx context.Context, sweep *cutting_garden_plugins.BulkSweep,
) (cutting_garden_plugins.BulkResult, error) {
	matches, ok, err := p.ListEnriched(ctx, sweep.Root, sweep.Filter)
	if err != nil {
		return cutting_garden_plugins.BulkResult{}, err
	}
	if !ok {
		// The testpeer's ListEnriched never declines, so this is defensive
		// parity with the linked reference (caldav): a mutation must refuse
		// a decline, never widen its scope to every child.
		return cutting_garden_plugins.BulkResult{}, errors.BadRequestf(
			"testpeer: cannot sweep %q: enriched listing declined at this"+
				" level", sweep.Root,
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
// accepted but landed no recognized field (#182), Failed otherwise (never a
// returned error, in best-effort mode).
func (p *TreePlugin) applyBulkOp(
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
		err = errors.BadRequestf("testpeer: unknown bulk op kind %q", op.Kind)
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
