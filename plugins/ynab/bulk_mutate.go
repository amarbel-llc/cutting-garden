package ynab

import (
	"bytes"
	"context"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

var _ cutting_garden_plugins.BulkMutator = (*Plugin)(nil)

// BulkMutate is ynab's best-effort BulkMutator (RFC 0017, cutting-garden#334).
// Its point is the request budget: YNAB allows 200 requests an hour per token,
// and N single patches cost about three requests EACH (a fresh read of the
// transaction, the payee or category lookup, the write). A changeset of N
// transaction patches costs a handful: one fresh listing that serves the
// targets (client.go freshTransactionsByID; a read by id only for a target older
// than the listing's window), the category tree when an op names a category, one
// payee list when any op names a payee, and ONE bulk PATCH.
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
//
// Several patches of one transaction are merged into one entry, as applying them
// one after another would have done; two that set the same field to different
// values conflict, and the later one is a BulkFailure (the bulk update has no
// defined order for one id). The same transaction named through two spellings of
// its budget (a name and an id) is caught the same way, after the budget is
// resolved, so it can never be written by two requests.
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

// bulkKey identifies the budget an op's URI names, as spelled; ops sharing one
// are resolved and batched together.
type bulkKey struct{ account, budget string }

// bulkMember is one transaction patch queued for its budget's batch.
type bulkMember struct {
	op    cutting_garden_plugins.BulkOp
	txnID string
	patch transactionPatch
}

// bulkGroup is the transaction patches destined for one spelling of a budget.
type bulkGroup struct {
	ref     nodeRef // any member's ref: it names the account and budget
	members []bulkMember
}

// bulkFailure builds a failure for op, keeping the URI and the temp-id Ref the
// caller addressed it by so every failure can be matched back to its op.
func bulkFailure(op cutting_garden_plugins.BulkOp, err error) cutting_garden_plugins.BulkFailure {
	return cutting_garden_plugins.BulkFailure{URI: op.URI, Ref: op.Ref, Err: err.Error()}
}

func (p Plugin) bulkOps(
	ctx context.Context, ops []cutting_garden_plugins.BulkOp,
) cutting_garden_plugins.BulkResult {
	var result cutting_garden_plugins.BulkResult
	groups := map[bulkKey]*bulkGroup{}
	var order []bulkKey

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
			result.Failed = append(result.Failed, bulkFailure(op, err))
			continue
		}
		if ref.kind != kindTransaction {
			cutting_garden_plugins.ApplyBulkOp(ctx, p, op, &result)
			continue
		}
		patch, err := parseTransactionPatch(bytes.NewReader(op.Body))
		if err != nil {
			result.Failed = append(result.Failed, bulkFailure(op, err))
			continue
		}
		if len(patch.recognized) == 0 {
			// Nothing recognized: no read, no write, an honest empty (#182).
			result.PatchedNothing = append(result.PatchedNothing, op.URI)
			continue
		}
		key := bulkKey{ref.account, ref.budget}
		group, ok := groups[key]
		if !ok {
			group = &bulkGroup{ref: ref}
			groups[key] = group
			order = append(order, key)
		}
		group.members = append(group.members, bulkMember{op: op, txnID: ref.txnID, patch: patch})
	}

	// claimed records every transaction a group has taken, by the RESOLVED
	// budget, so one budget spelled two ways cannot have a transaction written by
	// two requests.
	claimed := map[string]bool{}
	for _, key := range order {
		applyBulkGroup(ctx, groups[key], claimed, &result)
	}
	return result
}

// applyBulkGroup plans every member of one budget's batch against fresh
// transactions, sends ONE bulk update for those that change something, and
// checks each saved transaction against what its op asked for.
func applyBulkGroup(
	ctx context.Context, group *bulkGroup, claimed map[string]bool,
	result *cutting_garden_plugins.BulkResult,
) {
	failOne := func(m bulkMember, err error) {
		result.Failed = append(result.Failed, bulkFailure(m.op, err))
	}
	failAll := func(err error) {
		for _, m := range group.members {
			failOne(m, err)
		}
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

	members := mergeMembers(group.members, failOne)
	live := members[:0:0]
	for _, m := range members {
		key := group.ref.account + "\x00" + budgetID + "\x00" + m.txnID
		if claimed[key] {
			failOne(m, errors.BadRequestf(
				"ynab plugin: transaction %q is patched twice in one bulk request "+
					"(through two spellings of its budget); the bulk update has no defined "+
					"order for one id, so send one patch carrying every field", m.txnID,
			))
			continue
		}
		claimed[key] = true
		live = append(live, m)
	}
	if len(live) == 0 {
		return
	}

	ids := make([]string, len(live))
	for i, m := range live {
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
	for _, m := range live {
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

// mergeMembers folds the patches of one transaction into the first of them, in
// request order, so a caller that sends a field per op gets what sequential
// patches would have done. A later patch that sets a field the merged patch
// already holds to a DIFFERENT value conflicts and is reported through fail,
// leaving the transaction with what was merged so far. The result keeps the
// order of each transaction's first appearance.
func mergeMembers(
	members []bulkMember, fail func(bulkMember, error),
) []bulkMember {
	var merged []bulkMember
	index := map[string]int{}
	for _, m := range members {
		i, seen := index[m.txnID]
		if !seen {
			index[m.txnID] = len(merged)
			merged = append(merged, m)
			continue
		}
		combined, err := mergePatches(merged[i].patch, m.patch)
		if err != nil {
			fail(m, err)
			continue
		}
		merged[i].patch = combined
	}
	return merged
}

// mergePatches combines two patches of one transaction. A field set by both to
// different values is a conflict; equal values, or a field only one sets, merge.
func mergePatches(a, b transactionPatch) (transactionPatch, error) {
	conflict := func(field string) error {
		return errors.BadRequestf(
			"ynab plugin: the same transaction is patched twice in one bulk request with "+
				"different %s values; send one patch carrying the value you want", field,
		)
	}
	out := a
	if b.hasCategory {
		if a.hasCategory && a.category != b.category {
			return a, conflict("category")
		}
		out.hasCategory, out.category = true, b.category
	}
	if b.hasCleared {
		if a.hasCleared && a.cleared != b.cleared {
			return a, conflict("cleared")
		}
		out.hasCleared, out.cleared = true, b.cleared
	}
	if b.hasMemo {
		if a.hasMemo && !sameMemoValue(a.memo, b.memo) {
			return a, conflict("memo")
		}
		out.hasMemo, out.memo = true, b.memo
	}
	if b.hasPayee {
		if a.hasPayee && a.payee != b.payee {
			return a, conflict("payee")
		}
		out.hasPayee, out.payee = true, b.payee
	}
	out.recognized = out.recognizedFields()
	return out, nil
}

// recognizedFields lists the fields the patch sets, in transactionPatchFields'
// order, so the applied report stays deterministic after a merge.
func (p transactionPatch) recognizedFields() []string {
	has := map[string]bool{
		"category": p.hasCategory, "cleared": p.hasCleared, "memo": p.hasMemo, "payee": p.hasPayee,
	}
	out := make([]string, 0, len(transactionPatchFields))
	for _, field := range transactionPatchFields {
		if has[field] {
			out = append(out, field)
		}
	}
	return out
}

// sameMemoValue compares two memo patch values exactly: unlike sameMemo, which
// treats nil and "" alike because YNAB returns either for no memo, a patch of
// null (clear it) and a patch of "" are different requests.
func sameMemoValue(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// memoPayeeLister makes a payeeLister read once and serve the same list to every
// op of a batch. A failed first read is served to the rest of the batch too: the
// batch does not retry, and each op reports the same cause.
func memoPayeeLister(inner payeeLister) payeeLister {
	var (
		done bool
		list []Payee
		err  error
	)
	return func(ctx context.Context) ([]Payee, error) {
		if !done {
			list, err = inner(ctx)
			done = true
		}
		return list, err
	}
}
