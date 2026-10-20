package ynab

import (
	"context"
	"strings"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// budgetAliases are the ids the YNAB API accepts for "the budget the token
// last used" / "the account's default budget". They pass straight through as
// the budget id, with no lookup.
var budgetAliases = map[string]bool{"last-used": true, "default": true}

// isUUID reports whether s has the 8-4-4-4-12 shape of a YNAB id, so a URI
// that already carries a budget id skips the name lookup (one request saved
// against the 200-per-hour limit).
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return false
			}
		}
	}
	return true
}

// pick chooses the one item named name (or, failing that, whose id equals
// name) from items. Several items with the same name are ambiguous unless the
// URI's `?id=` tiebreak names one. what describes the entity for diagnostics.
func pick[T any](
	items []T,
	name, tiebreakID, what string,
	nameOf, idOf func(T) string,
) (T, error) {
	var zero T
	var named []T
	for _, it := range items {
		if nameOf(it) == name {
			named = append(named, it)
		}
	}
	switch len(named) {
	case 1:
		return named[0], nil
	case 0:
		for _, it := range items {
			if idOf(it) == name {
				return it, nil
			}
		}
		return zero, errors.BadRequestf("ynab plugin: no %s named %q", what, name)
	}
	if tiebreakID != "" {
		for _, it := range named {
			if idOf(it) == tiebreakID {
				return it, nil
			}
		}
	}
	ids := make([]string, len(named))
	for i, it := range named {
		ids[i] = idOf(it)
	}
	return zero, errors.BadRequestf(
		"ynab plugin: %d %ss are named %q; append ?id=<id> (candidates: %s)",
		len(named), what, name, strings.Join(ids, ", "),
	)
}

// repeatedNames returns the set of names that more than one sibling item
// carries — the siblings whose minted URI must carry an id (a tiebreak, or the
// id as the segment).
func repeatedNames[T any](items []T, nameOf func(T) string) map[string]bool {
	count := make(map[string]int, len(items))
	for _, it := range items {
		count[nameOf(it)]++
	}
	dups := map[string]bool{}
	for name, n := range count {
		if n > 1 {
			dups[name] = true
		}
	}
	return dups
}

// resolveBudgetID maps the URI's budget segment to a YNAB budget id: an alias
// or an id passes through, anything else is a name looked up in the budget
// list. When two budgets share a name, listing mints the budget ID as the
// segment (there is no room for a query on a deeper URI), so a name that is
// still ambiguous here is a hand-typed one.
func resolveBudgetID(ctx context.Context, c *client, ref nodeRef) (string, error) {
	if budgetAliases[ref.budget] || isUUID(ref.budget) {
		return ref.budget, nil
	}
	budgets, err := c.budgets(ctx)
	if err != nil {
		return "", err
	}
	tiebreak := ""
	if ref.kind == kindBudget {
		tiebreak = ref.id
	}
	b, err := pick(budgets, ref.budget, tiebreak, "budget",
		func(b Budget) string { return b.Name }, func(b Budget) string { return b.ID })
	if err != nil {
		return "", err
	}
	return b.ID, nil
}

// resolveAccount finds the YNAB account a kindAccount URI names.
func resolveAccount(ctx context.Context, c *client, budgetID string, ref nodeRef) (Account, error) {
	accounts, err := c.accounts(ctx, budgetID)
	if err != nil {
		return Account{}, err
	}
	return pick(liveAccounts(accounts), ref.accountName, ref.id, "account",
		func(a Account) string { return a.Name }, func(a Account) string { return a.ID })
}

// resolveCategoryGroup finds the category group a kindCategoryGroup /
// kindCategory URI names.
func resolveCategoryGroup(
	ctx context.Context, c *client, budgetID string, ref nodeRef,
) (CategoryGroup, error) {
	groups, err := c.categoryGroups(ctx, budgetID)
	if err != nil {
		return CategoryGroup{}, err
	}
	tiebreak := ""
	if ref.kind == kindCategoryGroup {
		tiebreak = ref.id
	}
	return pick(liveGroups(groups), ref.group, tiebreak, "category group",
		func(g CategoryGroup) string { return g.Name }, func(g CategoryGroup) string { return g.ID })
}

// resolveCategory finds the category a kindCategory URI names within group.
func resolveCategory(group CategoryGroup, ref nodeRef) (Category, error) {
	return pick(liveCategories(group.Categories), ref.category, ref.id, "category",
		func(c Category) string { return c.Name }, func(c Category) string { return c.ID })
}

func liveAccounts(in []Account) []Account {
	out := make([]Account, 0, len(in))
	for _, a := range in {
		if !a.Deleted {
			out = append(out, a)
		}
	}
	return out
}

func liveGroups(in []CategoryGroup) []CategoryGroup {
	out := make([]CategoryGroup, 0, len(in))
	for _, g := range in {
		if !g.Deleted {
			out = append(out, g)
		}
	}
	return out
}

func liveCategories(in []Category) []Category {
	out := make([]Category, 0, len(in))
	for _, c := range in {
		if !c.Deleted {
			out = append(out, c)
		}
	}
	return out
}
