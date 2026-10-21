package ynab

import (
	"bytes"
	"context"
	"sync"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

var _ cutting_garden_plugins.BulkMutator = (*Plugin)(nil)

// BulkMutate is ynab's best-effort BulkMutator (RFC 0017, cutting-garden#334).
// Its point is the request budget: YNAB allows 200 requests an hour per token,
// and N single patches cost about three requests EACH (a fresh read of the
// transaction, the payee or category lookup, the write). A changeset of N
// transaction patches costs a handful, however large N is: one fresh read of the
// targets, one payee list when any op names a payee, and ONE bulk PATCH.
//
// It is the same write as PatchNode, not a looser one. Every op is parsed and
// planned by the single-patch machinery against a FRESH read of its transaction,
// so each keeps its guards (a reconciled transaction's cleared state, a split's
// or transfer's category, a transfer payee, an unknown or ambiguous category or
// payee) and its own failure: a refused op is a BulkFailure and the others still
// go ahead. A patch that changes nothing is PatchedNothing, and a patch whose
// effect the saved transaction does not show is a BulkFailure, as for PatchNode.
//
// What it does not do:
//   - Atomic requests are rejected (ErrBulkAtomicUnsupported): YNAB documents no
//     all-or-nothing guarantee for the bulk update, so claiming one would be a
//     silent downgrade waiting to happen.
//   - Only transaction patches batch. A payee rename is one request each in the
//     API and goes through PatchNode; create, put and delete are refused there.
//   - Two ops on one transaction in a request are refused (the second is a
//     BulkFailure): the bulk update has no defined order for the same id.
//
// A request that fails as a whole fails every op in that batch (one HTTP call);
// ops in another budget's batch are unaffected.
func (p Plugin) BulkMutate(
	ctx context.Context, req cutting_garden_plugins.BulkRequest,
) (cutting_garden_plugins.BulkResult, error) {
	if err := req.Validate(); err != nil {
		return cutting_garden_plugins.BulkResult{}, err
	}
	if req.Atomicity == cutting_garden_plugins.BulkAtomic {
		return cutting_garden_plugins.BulkResult{}, cutting_garden_plugins.ErrBulkAtomicUnsupported
	}
	if req.Sweep != nil {
		return p.bulkSweep(ctx, req.Sweep)
	}
	return p.bulkOps(ctx, req.Ops), nil
}

// bulkSweep resolves a sweep's match set with ListEnriched and batches the op
// template over it. Like every bulk write it REFUSES when the enriched listing
// declines at this level, rather than widening "matching" to "every child"
// (cutting-garden#191).
func (p Plugin) bulkSweep(
	ctx context.Context, sweep *cutting_garden_plugins.BulkSweep,
) (cutting_garden_plugins.BulkResult, error) {
	matches, ok, err := p.ListEnriched(ctx, sweep.Root, sweep.Filter)
	if err != nil {
		return cutting_garden_plugins.BulkResult{}, err
	}
	if !ok {
		return cutting_garden_plugins.BulkResult{}, errors.BadRequestf(
			"bulk_mutate: cannot sweep %q: the enriched listing declined at this level "+
				"(its children are containers, not the transactions a filter selects) — "+
				"sweep a transactions, account or category node", sweep.Root,
		)
	}
	ops := make([]cutting_garden_plugins.BulkOp, 0, len(matches))
	for _, match := range matches {
		op := sweep.Op
		op.URI = match.URI
		ops = append(ops, op)
	}
	if len(ops) == 0 {
		return cutting_garden_plugins.BulkResult{}, nil
	}
	return p.bulkOps(ctx, ops), nil
}

// bulkKey identifies the YNAB budget an op writes to; ops sharing one are
// batched into one request.
type bulkKey struct{ account, budget string }

// bulkMember is one transaction patch queued for its budget's batch.
type bulkMember struct {
	op    cutting_garden_plugins.BulkOp
	txnID string
	patch transactionPatch
}

// bulkGroup is the transaction patches destined for one budget.
type bulkGroup struct {
	ref     nodeRef // any member's ref: it names the account and budget
	members []bulkMember
}

func (p Plugin) bulkOps(
	ctx context.Context, ops []cutting_garden_plugins.BulkOp,
) cutting_garden_plugins.BulkResult {
	var result cutting_garden_plugins.BulkResult
	fail := func(op cutting_garden_plugins.BulkOp, err error) {
		result.Failed = append(result.Failed, cutting_garden_plugins.BulkFailure{
			URI: op.URI, Ref: op.Ref, Err: err.Error(),
		})
	}

	groups := map[bulkKey]*bulkGroup{}
	var order []bulkKey
	seen := map[string]bool{}

	for _, op := range ops {
		// Anything that is not a patch of a transaction node (a payee rename, a
		// temp-id create, a verb ynab refuses) takes the single-node path, which
		// reports its own refusal.
		if op.Kind != cutting_garden_plugins.BulkPatch || op.URI == nil {
			cutting_garden_plugins.ApplyBulkOp(ctx, p, op, &result)
			continue
		}
		ref, err := classifyURI(op.URI)
		if err != nil {
			fail(op, err)
			continue
		}
		if ref.kind != kindTransaction {
			cutting_garden_plugins.ApplyBulkOp(ctx, p, op, &result)
			continue
		}
		patch, err := parseTransactionPatch(bytes.NewReader(op.Body))
		if err != nil {
			fail(op, err)
			continue
		}
		if len(patch.recognized) == 0 {
			// Nothing recognized: no read, no write, an honest empty (#182).
			result.PatchedNothing = append(result.PatchedNothing, op.URI)
			continue
		}
		key := bulkKey{ref.account, ref.budget}
		once := key.account + "\x00" + key.budget + "\x00" + ref.txnID
		if seen[once] {
			fail(op, errors.BadRequestf(
				"ynab plugin: transaction %q is patched twice in one bulk request; "+
					"the bulk update has no defined order for one id, so send one patch "+
					"carrying every field", ref.txnID,
			))
			continue
		}
		seen[once] = true
		group, ok := groups[key]
		if !ok {
			group = &bulkGroup{ref: ref}
			groups[key] = group
			order = append(order, key)
		}
		group.members = append(group.members, bulkMember{op: op, txnID: ref.txnID, patch: patch})
	}

	for _, key := range order {
		applyBulkGroup(ctx, groups[key], &result)
	}
	return result
}

// applyBulkGroup plans every member of one budget's batch against fresh
// transactions, sends ONE bulk update for those that change something, and
// checks each saved transaction against what its op asked for.
func applyBulkGroup(
	ctx context.Context, group *bulkGroup, result *cutting_garden_plugins.BulkResult,
) {
	failAll := func(err error) {
		for _, m := range group.members {
			result.Failed = append(result.Failed, cutting_garden_plugins.BulkFailure{
				URI: m.op.URI, Err: err.Error(),
			})
		}
	}
	failOne := func(m bulkMember, err error) {
		result.Failed = append(result.Failed, cutting_garden_plugins.BulkFailure{
			URI: m.op.URI, Err: err.Error(),
		})
	}

	c, err := resolveClient(group.ref)
	if err != nil {
		failAll(err)
		return
	}
	budgetID, err := resolveBudgetID(ctx, c, group.ref)
	if err != nil {
		failAll(err)
		return
	}

	ids := make([]string, len(group.members))
	for i, m := range group.members {
		ids[i] = m.txnID
	}
	rows, readErrs := c.freshTransactionsByID(ctx, budgetID, ids)
	payees := memoPayeeLister(freshPayeeLister(c, budgetID))

	type planned struct {
		member bulkMember
		plan   patchPlan
	}
	var (
		batch   []planned
		entries []map[string]any
	)
	for _, m := range group.members {
		if err, bad := readErrs[m.txnID]; bad {
			failOne(m, err)
			continue
		}
		plan, err := planTransactionPatch(ctx, c, budgetID, rows[m.txnID], m.patch, payees)
		if err != nil {
			failOne(m, err)
			continue
		}
		if len(plan.changed) == 0 {
			result.PatchedNothing = append(result.PatchedNothing, m.op.URI) // already as requested
			continue
		}
		batch = append(batch, planned{m, plan})
		entries = append(entries, plan.entry)
	}
	if len(batch) == 0 {
		return
	}

	saved, err := c.patchTransactions(ctx, budgetID, entries)
	if err != nil {
		for _, b := range batch {
			failOne(b.member, err)
		}
		return
	}
	for _, b := range batch {
		if err := b.plan.verify(saved, b.member.txnID); err != nil {
			failOne(b.member, err)
			continue
		}
		result.AppliedNodes = append(result.AppliedNodes, b.member.op.URI)
	}
}

// memoPayeeLister makes a payeeLister read once and serve the same list to every
// op of a batch.
func memoPayeeLister(inner payeeLister) payeeLister {
	var (
		once sync.Once
		list []Payee
		err  error
	)
	return func(ctx context.Context) ([]Payee, error) {
		once.Do(func() { list, err = inner(ctx) })
		return list, err
	}
}
