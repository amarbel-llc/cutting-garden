package ynab

import (
	"net/url"
	"strings"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// nodeKind classifies a `ynab://` URI into one of the node kinds the tree is
// built from. Classification is PURE (no network): the URI carries
// everything needed to place it.
type nodeKind int

const (
	kindAccountRoot   nodeKind = iota // ynab://acct/
	kindBudget                        // ynab://acct/<budget>/
	kindAccounts                      // ynab://acct/<budget>/accounts/
	kindCategories                    // ynab://acct/<budget>/categories/
	kindTransactions                  // ynab://acct/<budget>/transactions/
	kindAccount                       // ynab://acct/<budget>/accounts/<account>
	kindCategoryGroup                 // ynab://acct/<budget>/categories/<group>
	kindCategory                      // ynab://acct/<budget>/categories/<group>/<category>
	kindTransaction                   // ynab://acct/<budget>/transactions/<id>
	kindPayees                        // ynab://acct/<budget>/payees/
	kindPayee                         // ynab://acct/<budget>/payees/<id>
)

// Section segment names under a budget.
const (
	sectionAccounts     = "accounts"
	sectionCategories   = "categories"
	sectionTransactions = "transactions"
	sectionPayees       = "payees"
)

// nodeRef is a classified `ynab://` URI: the config account (the URI's host
// slot — NOT a hostname), the budget name, and the section-specific names.
// `id` is the optional YNAB id tiebreak (`?id=`) for a name that is not
// unique under its parent; for a transaction URI the last segment IS the id.
type nodeRef struct {
	kind        nodeKind
	account     string // the config account (URI host slot)
	budget      string
	accountName string // the YNAB account name under accounts/
	group       string
	category    string
	txnID       string
	payeeID     string // the payee's id: a payee is addressed by id so a rename keeps its URI
	id          string
	// date is the optional `?date=` window (a year, month or day) on a
	// transaction-bearing node: the URI-addressable counterpart of a `date`
	// filter, for consumers that select by URI and cannot push a filter down
	// (organize's anchor). It bounds the transactions the node lists.
	date string
}

// classifyURI parses and classifies a `ynab://` URI. It rejects an unknown
// scheme, an empty account, an unknown section, an over-long path or a
// malformed or misplaced `?date=` as bad requests (errors.Is400BadRequest
// true) — CALLER mistakes. It does NOT check that the account is configured
// (that needs package state).
func classifyURI(u *url.URL) (nodeRef, error) {
	ref, err := classifyPath(u)
	if err != nil {
		return nodeRef{}, err
	}
	if date := u.Query().Get("date"); date != "" {
		if !bearsTransactions(ref.kind) {
			return nodeRef{}, errors.BadRequestf(
				"ynab plugin: ?date= only applies to a transactions, account or category node; %q is not one",
				u.String(),
			)
		}
		if _, _, err := dateBucketBounds(date); err != nil {
			return nodeRef{}, err
		}
		ref.date = date
	}
	return ref, nil
}

// classifyPath classifies the path (and the `?id=` tiebreak) of a `ynab://` URI.
func classifyPath(u *url.URL) (nodeRef, error) {
	if u.Scheme != schemeYNAB {
		return nodeRef{}, errors.BadRequestf(
			"ynab plugin: unsupported scheme %q in %q", u.Scheme, u.String(),
		)
	}
	if u.Host == "" {
		return nodeRef{}, errors.BadRequestf(
			"ynab plugin: empty account in %q\nhint: pass `ynab://<account>/`", u.String(),
		)
	}
	segs, err := pathSegments(u)
	if err != nil {
		return nodeRef{}, err
	}
	ref := nodeRef{account: u.Host, id: u.Query().Get("id")}

	bad := func(why string) (nodeRef, error) {
		return nodeRef{}, errors.BadRequestf("ynab plugin: %s in %q", why, u.String())
	}
	switch n := len(segs); {
	case n == 0:
		ref.kind = kindAccountRoot
		return ref, nil
	case n == 1:
		ref.kind, ref.budget = kindBudget, segs[0]
		return ref, nil
	}
	ref.budget = segs[0]
	section := segs[1]
	rest := segs[2:]
	switch section {
	case sectionAccounts:
		switch len(rest) {
		case 0:
			ref.kind = kindAccounts
		case 1:
			ref.kind, ref.accountName = kindAccount, rest[0]
		default:
			return bad("too many path segments under accounts/")
		}
	case sectionCategories:
		switch len(rest) {
		case 0:
			ref.kind = kindCategories
		case 1:
			ref.kind, ref.group = kindCategoryGroup, rest[0]
		case 2:
			ref.kind, ref.group, ref.category = kindCategory, rest[0], rest[1]
		default:
			return bad("too many path segments under categories/")
		}
	case sectionTransactions:
		switch len(rest) {
		case 0:
			ref.kind = kindTransactions
		case 1:
			ref.kind, ref.txnID = kindTransaction, rest[0]
		default:
			return bad("too many path segments under transactions/")
		}
	case sectionPayees:
		switch len(rest) {
		case 0:
			ref.kind = kindPayees
		case 1:
			ref.kind, ref.payeeID = kindPayee, rest[0]
		default:
			return bad("too many path segments under payees/")
		}
	default:
		return bad("unknown section " + `"` + section + `"` +
			" (want accounts, categories, transactions or payees)")
	}
	return ref, nil
}

// pathSegments splits a URI's escaped path into decoded name segments. An
// unescapable segment is a bad request.
func pathSegments(u *url.URL) ([]string, error) {
	p := strings.Trim(u.EscapedPath(), "/")
	if p == "" {
		return nil, nil
	}
	parts := strings.Split(p, "/")
	segs := make([]string, len(parts))
	for i, part := range parts {
		s, err := url.PathUnescape(part)
		if err != nil {
			return nil, errors.BadRequestf(
				"ynab plugin: bad path segment %q in %q: %s", part, u.String(), err,
			)
		}
		segs[i] = s
	}
	return segs, nil
}

// --- URI minting (the inverse of classifyURI) ---

// mintURI builds `ynab://<account>/<seg…>` from decoded segments, escaping
// each so a name with a reserved rune stays one segment. Container URIs end
// in `/` (the directory spelling organize anchors at); a transaction leaf
// does not. id, when non-empty, rides as the `?id=` tiebreak.
func mintURI(account string, segs []string, container bool, id string) *url.URL {
	u := &url.URL{Scheme: schemeYNAB, Host: account}
	escaped := make([]string, len(segs))
	for i, s := range segs {
		escaped[i] = url.PathEscape(s)
	}
	raw := "/" + strings.Join(escaped, "/")
	if container && len(segs) > 0 {
		raw += "/"
	}
	if parsed, err := url.Parse(raw); err == nil {
		u.Path, u.RawPath = parsed.Path, parsed.RawPath
	} else {
		u.Path = raw
	}
	if id != "" {
		u.RawQuery = url.Values{"id": {id}}.Encode()
	}
	return u
}

func accountRootURI(account string) *url.URL { return mintURI(account, nil, true, "") }

func budgetURI(account, budget string) *url.URL {
	return mintURI(account, []string{budget}, true, "")
}

func sectionURI(account, budget, section string) *url.URL {
	return mintURI(account, []string{budget, section}, true, "")
}

func accountNodeURI(account, budget, name, id string) *url.URL {
	return mintURI(account, []string{budget, sectionAccounts, name}, true, id)
}

func categoryGroupURI(account, budget, group, id string) *url.URL {
	return mintURI(account, []string{budget, sectionCategories, group}, true, id)
}

func categoryURI(account, budget, group, category, id string) *url.URL {
	return mintURI(account, []string{budget, sectionCategories, group, category}, true, id)
}

func transactionURI(account, budget, txnID string) *url.URL {
	return mintURI(account, []string{budget, sectionTransactions, txnID}, false, "")
}

func payeeURI(account, budget, payeeID string) *url.URL {
	return mintURI(account, []string{budget, sectionPayees, payeeID}, false, "")
}
