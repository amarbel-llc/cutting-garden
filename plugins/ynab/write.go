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

// The write path (FDR 0027 slice 2, cutting-garden#331). Slice 1 was
// read-only; this adds ONE write, on transaction nodes: change a transaction's
// category, memo or cleared state. This is real money data, so the plugin
// refuses what is irreversible or ambiguous before sending anything, sends one
// request, and checks the response reflects what it asked for.
//
// What it will not do, and why:
//   - change a SPLIT parent's category: its category is its legs', and YNAB
//     rejects (or ignores) a category on a split;
//   - categorize a TRANSFER: a transfer between budget accounts has no category;
//   - touch the cleared state of a RECONCILED transaction, or set `reconciled`:
//     reconciling is a deliberate act in YNAB's own UI and unreconciling alters
//     a locked balance;
//   - create, replace or delete a transaction: not offered, by design.

// transactionPatchFields are the PatchNode keys this plugin recognizes, sorted
// so the `applied` report is deterministic.
var transactionPatchFields = []string{"category", "cleared", "memo"}

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
}

// PatchNode changes a transaction's category, memo and/or cleared state in
// ONE bulk-update request (YNAB's `PATCH /budgets/{id}/transactions`):
//
//	{"category": "Dining Out"}            a bare name, when it is unique
//	{"category": "Food/Dining Out"}       Group/Category, when the name repeats
//	{"category": "<category id>"}         an id
//	{"memo": "dinner with the group"}     {"memo": null} clears it (max 200)
//	{"cleared": "cleared"}                "cleared" or "uncleared" only
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
	if ref.kind != kindTransaction {
		return nil, errors.BadRequestf(
			"ynab plugin: only transaction nodes are patchable; %q is not one\n"+
				"hint: pass `ynab://<account>/<budget>/transactions/<id>`", node,
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
	current, err := c.transaction(ctx, budgetID, ref.txnID)
	if err != nil {
		return nil, err
	}
	plan, err := planTransactionPatch(ctx, c, budgetID, current, patch)
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
			"entered in YNAB; use patch to change a category, memo or cleared state",
	)
}

// PutNode is unsupported: a transaction has no wholesale-replaceable body here.
func (Plugin) PutNode(_ context.Context, _ *url.URL, _ io.Reader) error {
	return errors.BadRequestf(
		"ynab plugin: PutNode (full replace) is not supported — use patch to " +
			"change a category, memo or cleared state",
	)
}

// DeleteNode is unsupported: deleting a transaction is irreversible and out of
// scope for a categorization tool.
func (Plugin) DeleteNode(_ context.Context, _ *url.URL) error {
	return errors.BadRequestf(
		"ynab plugin: DeleteNode is not supported — delete a transaction in YNAB itself",
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
}

// planTransactionPatch checks a patch against the transaction as it stands and
// builds the request entry, refusing what must not be written. It sends
// nothing. Values already equal to the current ones are dropped, so a request
// that changes nothing sends nothing.
func planTransactionPatch(
	ctx context.Context, c *client, budgetID string, current Transaction, patch transactionPatch,
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

	// changed was appended in transactionPatchFields' order (category, cleared,
	// memo), so the applied report is already deterministic.
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
	return nil
}

// isSplit reports whether a transaction is a split parent.
func isSplit(t Transaction) bool {
	return len(t.Subtransactions) > 0 ||
		(t.CategoryName != nil && *t.CategoryName == "Split")
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
	for _, g := range liveGroups(groups) {
		for _, cat := range liveCategories(g.Categories) {
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
