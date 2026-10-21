package ynab

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The write path (FDR 0027 slice 2, cutting-garden#331; payees, #333). Slice 1
// was read-only; this adds two writes: on a transaction node, change its
// category, memo, cleared state or payee; on a payee node, rename it (see
// payee_write.go). This is real money data, so the plugin refuses what is
// irreversible or ambiguous before sending anything, sends one request, and
// checks the response reflects what it asked for.
//
// What it will not do, and why:
//   - change a SPLIT parent's category: its category is its legs', and YNAB
//     rejects (or ignores) a category on a split;
//   - categorize a TRANSFER: a transfer between budget accounts has no category;
//   - repoint a TRANSFER's payee, or point any transaction at a TRANSFER payee:
//     the payee of a transfer is the other account, and changing it would turn a
//     transaction into a transfer or break one;
//   - create a payee: a transaction's payee must already exist (YNAB would
//     otherwise create one from any unmatched name, so a typo would mint a payee);
//   - touch the cleared state of a RECONCILED transaction, or set `reconciled`:
//     reconciling is a deliberate act in YNAB's own UI and unreconciling alters
//     a locked balance;
//   - create, replace or delete a transaction or a payee: not offered, by design.

// transactionPatchFields are the PatchNode keys this plugin recognizes on a
// transaction, sorted so the `applied` report is deterministic.
var transactionPatchFields = []string{"category", "cleared", "memo", "payee"}

// maxMemoRunes is YNAB's documented memo limit.
const maxMemoRunes = 200

// Cleared values a write may set. `reconciled` is read-only here.
const (
	writeCleared   = clearedCleared
	writeUncleared = clearedUncleared
)

// transactionPatch is a parsed, type-checked PatchNode body. A has* flag says
// the key was present, because a memo of null (clear it) differs from an absent
// memo (leave it).
type transactionPatch struct {
	recognized []string

	hasCategory bool
	category    string // a category id, "Group/Category", or a bare name

	hasMemo bool
	memo    *string // nil clears the memo

	hasCleared bool
	cleared    string

	hasPayee bool
	payee    string // an existing payee's name or id
}

// PatchNode changes a transaction's category, memo, cleared state and/or payee
// in ONE bulk-update request (YNAB's `PATCH /budgets/{id}/transactions`), or
// renames a payee (payee_write.go) when node is a payee:
//
//	{"category": "Dining Out"}            a bare name, when it is unique
//	{"category": "Food/Dining Out"}       Group/Category, when the name repeats
//	{"category": "<category id>"}         an id
//	{"memo": "dinner with the group"}     {"memo": null} clears it (max 200)
//	{"cleared": "cleared"}                "cleared" or "uncleared" only
//	{"payee": "Corner Cafe"}              an EXISTING payee's name, or its id
//
// applied lists the keys that CHANGED. A body naming nothing recognized, and a
// body whose values already match the transaction, apply nothing, send no
// write, and report a non-nil EMPTY applied rather than a bare success
// (cutting-garden#182). A recognized key carrying an unusable value is a bad
// request, never a tolerated field (#185). After the write the plugin checks
// the transaction YNAB returns carries the new values; a server that answered
// success without applying them is an error, not a silent false success.
func (Plugin) PatchNode(
	ctx context.Context, node *url.URL, body io.Reader,
) ([]string, error) {
	if node == nil {
		return nil, errors.ErrorWithStackf("ynab plugin: PatchNode requires a node URI")
	}
	ref, err := classifyURI(node)
	if err != nil {
		return nil, err
	}
	if ref.kind == kindPayee {
		return patchPayee(ctx, ref, body)
	}
	if ref.kind != kindTransaction {
		return nil, errors.BadRequestf(
			"ynab plugin: only transaction and payee nodes are patchable; %q is not one\n"+
				"hint: pass `ynab://<account>/<budget>/transactions/<id>` or "+
				"`ynab://<account>/<budget>/payees/<id>`", node,
		)
	}

	patch, err := parseTransactionPatch(body)
	if err != nil {
		return nil, err
	}
	if len(patch.recognized) == 0 {
		return patch.recognized, nil // nothing recognized: no fetch, honest report
	}

	c, err := resolveClient(ref)
	if err != nil {
		return nil, err
	}
	budgetID, err := resolveBudgetID(ctx, c, ref)
	if err != nil {
		return nil, err
	}
	// The guards (reconciled, split, already-as-requested) must see the
	// transaction as it is now, not as a listing cached up to a minute ago.
	current, err := c.freshTransaction(ctx, budgetID, ref.txnID)
	if err != nil {
		return nil, err
	}
	plan, err := planTransactionPatch(ctx, c, budgetID, current, patch, freshPayeeLister(c, budgetID))
	if err != nil {
		return nil, err
	}
	if len(plan.changed) == 0 {
		return []string{}, nil // already as requested: no write
	}

	saved, err := c.patchTransactions(ctx, budgetID, []map[string]any{plan.entry})
	if err != nil {
		return nil, err
	}
	if err := plan.verify(saved, current.ID); err != nil {
		return nil, err
	}
	return plan.changed, nil
}

// CreateNode is unsupported: transactions arrive from the bank import or are
// entered in YNAB; this plugin does not author them.
func (Plugin) CreateNode(_ context.Context, _ *url.URL, _ io.Reader, _ string) error {
	return errors.BadRequestf(
		"ynab plugin: CreateNode is not supported — transactions are imported or " +
			"entered in YNAB, and payees are created there too; use patch to change " +
			"a transaction's category, memo, cleared state or payee, or a payee's name",
	)
}

// PutNode is unsupported: a transaction or payee has no wholesale-replaceable
// body here.
func (Plugin) PutNode(_ context.Context, _ *url.URL, _ io.Reader) error {
	return errors.BadRequestf(
		"ynab plugin: PutNode (full replace) is not supported — use patch to " +
			"change a transaction's category, memo, cleared state or payee, or a payee's name",
	)
}

// DeleteNode is unsupported: deleting a transaction or a payee is irreversible
// and out of scope for a categorization tool.
func (Plugin) DeleteNode(_ context.Context, _ *url.URL) error {
	return errors.BadRequestf(
		"ynab plugin: DeleteNode is not supported — delete a transaction or payee in YNAB itself",
	)
}

// parseTransactionPatch reads and type-checks a PatchNode body without any
// network round trip, so a body that is wrong costs no request.
func parseTransactionPatch(body io.Reader) (transactionPatch, error) {
	var patch transactionPatch
	raw, err := io.ReadAll(body)
	if err != nil {
		return patch, errors.Wrap(err)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return patch, errors.BadRequestf("ynab plugin: PatchNode body must be JSON; got empty body")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return patch, errors.BadRequestf("ynab plugin: invalid patch JSON: %s", err)
	}
	patch.recognized = cutting_garden_plugins.RecognizedPatchFields(fields, transactionPatchFields)

	if rawCategory, ok := fields["category"]; ok {
		category, err := stringField("category", rawCategory)
		if err != nil {
			return patch, err
		}
		if category == "" {
			return patch, errors.BadRequestf(
				"ynab plugin: patch field \"category\" must not be empty\n" +
					"hint: to uncategorize, move the transaction to the \"Uncategorized\" category",
			)
		}
		patch.hasCategory, patch.category = true, category
	}
	if rawMemo, ok := fields["memo"]; ok {
		patch.hasMemo = true
		if string(bytes.TrimSpace(rawMemo)) != "null" {
			memo, err := stringField("memo", rawMemo)
			if err != nil {
				return patch, err
			}
			if utf8.RuneCountInString(memo) > maxMemoRunes {
				return patch, errors.BadRequestf(
					"ynab plugin: patch field \"memo\" is %d characters; YNAB allows at most %d",
					utf8.RuneCountInString(memo), maxMemoRunes,
				)
			}
			patch.memo = &memo
		}
	}
	if rawCleared, ok := fields["cleared"]; ok {
		cleared, err := stringField("cleared", rawCleared)
		if err != nil {
			return patch, err
		}
		switch cleared {
		case writeCleared, writeUncleared:
		case clearedReconciled:
			return patch, errors.BadRequestf(
				"ynab plugin: a transaction cannot be set to \"reconciled\" here; " +
					"reconciling is a deliberate step in YNAB itself",
			)
		default:
			return patch, errors.BadRequestf(
				"ynab plugin: patch field \"cleared\" must be %q or %q, got %q",
				writeCleared, writeUncleared, cleared,
			)
		}
		patch.hasCleared, patch.cleared = true, cleared
	}
	if rawPayee, ok := fields["payee"]; ok {
		payee, err := stringField("payee", rawPayee)
		if err != nil {
			return patch, err
		}
		if strings.TrimSpace(payee) == "" {
			return patch, errors.BadRequestf(
				"ynab plugin: patch field \"payee\" must not be empty\n" +
					"hint: a transaction cannot be left without a payee once it has one",
			)
		}
		patch.hasPayee, patch.payee = true, payee
	}
	return patch, nil
}

// stringField decodes a patch value that must be a JSON string.
func stringField(name string, raw json.RawMessage) (string, error) {
	var s string
	if string(bytes.TrimSpace(raw)) == "null" {
		return "", errors.BadRequestf("ynab plugin: patch field %q must be a string, got null", name)
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", errors.BadRequestf("ynab plugin: patch field %q must be a string: %s", name, err)
	}
	return s, nil
}

// patchPlan is a validated write: the entry to send, which keys it changes, and
// what the saved transaction must show for the write to count as applied.
type patchPlan struct {
	entry   map[string]any
	changed []string

	wantCategoryID *string
	wantMemo       **string // non-nil when the memo is part of the write
	wantCleared    string
	wantPayeeID    *string
}

// planTransactionPatch checks a patch against the transaction as it stands and
// builds the request entry, refusing what must not be written. It sends
// nothing. Values already equal to the current ones are dropped, so a request
// that changes nothing sends nothing.
func planTransactionPatch(
	ctx context.Context, c *client, budgetID string, current Transaction, patch transactionPatch,
	payees payeeLister,
) (patchPlan, error) {
	plan := patchPlan{entry: map[string]any{"id": current.ID}}
	if current.Deleted {
		return plan, errors.BadRequestf("ynab plugin: transaction %q is deleted", current.ID)
	}

	if patch.hasCategory {
		if isSplit(current) {
			return plan, errors.BadRequestf(
				"ynab plugin: transaction %q is a split; its category is its legs', "+
					"so it cannot be given one category here", current.ID,
			)
		}
		if current.TransferAccountID != nil {
			return plan, errors.BadRequestf(
				"ynab plugin: transaction %q is a transfer; a transfer is not categorized here", current.ID,
			)
		}
		category, err := resolveCategoryRef(ctx, c, budgetID, patch.category)
		if err != nil {
			return plan, err
		}
		if current.CategoryID == nil || *current.CategoryID != category.ID {
			plan.entry["category_id"] = category.ID
			plan.wantCategoryID = &category.ID
			plan.changed = append(plan.changed, "category")
		}
	}

	if patch.hasCleared {
		if current.Cleared == clearedReconciled {
			return plan, errors.BadRequestf(
				"ynab plugin: transaction %q is reconciled; its cleared state is locked "+
					"(unreconcile it in YNAB first)", current.ID,
			)
		}
		if current.Cleared != patch.cleared {
			plan.entry["cleared"] = patch.cleared
			plan.wantCleared = patch.cleared
			plan.changed = append(plan.changed, "cleared")
		}
	}

	if patch.hasMemo {
		if !sameMemo(current.Memo, patch.memo) {
			if patch.memo == nil {
				plan.entry["memo"] = nil
			} else {
				plan.entry["memo"] = *patch.memo
			}
			memo := patch.memo
			plan.wantMemo = &memo
			plan.changed = append(plan.changed, "memo")
		}
	}

	if patch.hasPayee {
		// A transfer's payee is the other account; repointing it would break the
		// transfer, so it is refused whatever the target.
		if current.TransferAccountID != nil {
			return plan, errors.BadRequestf(
				"ynab plugin: transaction %q is a transfer; its payee is the other account "+
					"and is not changed here", current.ID,
			)
		}
		payee, err := resolvePayeeRef(ctx, payees, patch.payee)
		if err != nil {
			return plan, err
		}
		if current.PayeeID == nil || *current.PayeeID != payee.ID {
			plan.entry["payee_id"] = payee.ID
			plan.wantPayeeID = &payee.ID
			plan.changed = append(plan.changed, "payee")
		}
	}

	// changed was appended in transactionPatchFields' order (category, cleared,
	// memo, payee), so the applied report is already deterministic.
	return plan, nil
}

// verify checks the saved transaction carries each value the write set. A
// server that answered success but did not apply the change would otherwise
// make a silent false success (cutting-garden#188).
func (p patchPlan) verify(saved []Transaction, id string) error {
	var got *Transaction
	for i := range saved {
		if saved[i].ID == id {
			got = &saved[i]
			break
		}
	}
	if got == nil {
		return errors.ErrorWithStackf(
			"ynab plugin: YNAB acknowledged the update but did not return transaction %q; "+
				"the write is not confirmed", id,
		)
	}
	if p.wantCategoryID != nil && (got.CategoryID == nil || *got.CategoryID != *p.wantCategoryID) {
		return errors.ErrorWithStackf(
			"ynab plugin: the category write to transaction %q was not reflected in YNAB's response", id,
		)
	}
	if p.wantCleared != "" && got.Cleared != p.wantCleared {
		return errors.ErrorWithStackf(
			"ynab plugin: the cleared write to transaction %q was not reflected in YNAB's response", id,
		)
	}
	if p.wantMemo != nil && !sameMemo(got.Memo, *p.wantMemo) {
		return errors.ErrorWithStackf(
			"ynab plugin: the memo write to transaction %q was not reflected in YNAB's response", id,
		)
	}
	if p.wantPayeeID != nil && (got.PayeeID == nil || *got.PayeeID != *p.wantPayeeID) {
		return errors.ErrorWithStackf(
			"ynab plugin: the payee write to transaction %q was not reflected in YNAB's response", id,
		)
	}
	return nil
}

// isSplit reports whether a transaction is a split parent. Only its legs say
// so: the "Split" category name is user data (a plain category may carry it).
func isSplit(t Transaction) bool {
	return len(t.Subtransactions) > 0
}

// sameMemo compares two memos, treating nil and the empty string as the same
// (YNAB returns either for no memo).
func sameMemo(a, b *string) bool {
	return deref(a) == deref(b)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// payeeLister supplies the budget's payees to resolvePayeeRef. A single patch
// reads them fresh each time; a bulk patch reads them fresh once for the whole
// batch (bulk_mutate.go), so N repoints cost one list, not N.
type payeeLister func(ctx context.Context) ([]Payee, error)

// freshPayeeLister reads the budget's payees from the server on every call,
// bypassing the freshness cache, and refreshes the cache with what it read
// (which is at least as fresh as anything already there).
func freshPayeeLister(c *client, budgetID string) payeeLister {
	return func(ctx context.Context) ([]Payee, error) {
		payees, err := c.freshPayees(ctx, budgetID)
		if err != nil {
			return nil, err
		}
		cachePut(cacheKey{scope: c.scope, kind: cachePayees, budget: budgetID}, payees)
		return payees, nil
	}
}

// resolvePayeeRef finds the existing payee a patch names, by id or by exact
// name. It never creates one: sent a `payee_name` that matches nothing, YNAB
// would mint a new payee, so a typo in a bulk edit would quietly fork a payee —
// the opposite of tidying them. A name shared by two payees is refused with the
// ids, and a transfer payee is refused outright: pointing a transaction at one
// is how YNAB represents a transfer, which is not a payee change.
//
// The payee list is read fresh, like the transaction it is checked against: a
// cleanup is done alongside YNAB's own UI (merges happen there), so a payee
// merged away or created a moment ago must not be resolved from a list cached
// before. Matching is exact on id or name; the rename guard's case-insensitive
// collision check (payee_write.go) is deliberately stricter.
func resolvePayeeRef(ctx context.Context, payees payeeLister, ref string) (Payee, error) {
	list, err := payees(ctx)
	if err != nil {
		return Payee{}, err
	}
	var byID, byName []Payee
	for _, p := range livePayees(list) {
		switch {
		case p.ID == ref:
			byID = append(byID, p)
		case p.Name == ref:
			byName = append(byName, p)
		}
	}
	matches := byID
	if len(matches) == 0 {
		matches = byName
	}
	switch len(matches) {
	case 0:
		return Payee{}, errors.BadRequestf(
			"ynab plugin: no payee named %q\nhint: this plugin does not create payees; "+
				"create it in YNAB first, or list ynab://<account>/<budget>/payees/", ref,
		)
	case 1:
		if matches[0].isTransfer() {
			return Payee{}, errors.BadRequestf(
				"ynab plugin: %q is a transfer payee (the other side of a transfer between "+
					"accounts), not a counterparty; enter transfers in YNAB", matches[0].Name,
			)
		}
		return matches[0], nil
	}
	ids := make([]string, len(matches))
	for i, p := range matches {
		ids[i] = p.ID
	}
	return Payee{}, errors.BadRequestf(
		"ynab plugin: %d payees are named %q; use the id (candidates: %s)",
		len(matches), ref, strings.Join(ids, ", "),
	)
}

// resolveCategoryRef finds the live category a patch names: an id, a
// "Group/Category" path, or a bare name. A bare name shared by categories in
// different groups is ambiguous and refused with the qualified candidates, so
// a write can never land on a guess.
func resolveCategoryRef(ctx context.Context, c *client, budgetID, ref string) (Category, error) {
	groups, err := c.categoryGroups(ctx, budgetID)
	if err != nil {
		return Category{}, err
	}
	type candidate struct {
		group string
		cat   Category
	}
	var all []candidate
	// Hidden groups and categories are retired by the user: they neither
	// receive a write nor make a live name ambiguous.
	for _, g := range liveGroups(groups) {
		if g.Hidden {
			continue
		}
		for _, cat := range liveCategories(g.Categories) {
			if cat.Hidden {
				continue
			}
			all = append(all, candidate{g.Name, cat})
		}
	}
	describe := func(cs []candidate) string {
		parts := make([]string, len(cs))
		for i, cand := range cs {
			parts[i] = cand.group + "/" + cand.cat.Name + " (" + cand.cat.ID + ")"
		}
		return strings.Join(parts, ", ")
	}
	pickOne := func(cs []candidate, what string) (Category, bool, error) {
		switch len(cs) {
		case 0:
			return Category{}, false, nil
		case 1:
			return cs[0].cat, true, nil
		}
		return Category{}, false, errors.BadRequestf(
			"ynab plugin: %d categories match %s %q; use the id (candidates: %s)",
			len(cs), what, ref, describe(cs),
		)
	}

	var byID, byPath, byName []candidate
	for _, cand := range all {
		switch {
		case cand.cat.ID == ref:
			byID = append(byID, cand)
		case cand.group+"/"+cand.cat.Name == ref:
			byPath = append(byPath, cand)
		case cand.cat.Name == ref:
			byName = append(byName, cand)
		}
	}
	if cat, ok, err := pickOne(byID, "the id"); err != nil || ok {
		return cat, err
	}
	if cat, ok, err := pickOne(byPath, "the path"); err != nil || ok {
		return cat, err
	}
	switch len(byName) {
	case 0:
		return Category{}, errors.BadRequestf("ynab plugin: no category named %q", ref)
	case 1:
		return byName[0].cat, nil
	}
	return Category{}, errors.BadRequestf(
		"ynab plugin: %d categories are named %q; use Group/Category or the id (candidates: %s)",
		len(byName), ref, describe(byName),
	)
}
