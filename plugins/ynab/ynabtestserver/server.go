// Package ynabtestserver is a minimal in-memory YNAB REST server for the ynab
// plugin's tests (and any future bats lane). It answers the read endpoints the
// plugin calls — budgets, a budget's accounts, categories and transactions
// (whole budget, per account, per category, and one by id) — under the same
// `/v1/budgets/...` paths the live API still serves, with the same `data`
// wrapper, `server_knowledge` field and error body, and it enforces the bearer
// token and counts requests so a test can pin the plugin's rate-limit economy
// (the live API allows 200 requests per hour per token).
//
// It is a pure net/http listener (TCP, no socketpair) so it runs inside the nix
// test sandbox, mirroring fastmailtestserver's shape (Start/URL/Close + seed
// helpers). It defines its OWN seed types rather than importing the ynab
// package, so the plugin's internal tests can import it without an import
// cycle. It is NOT a conformant YNAB implementation and is not shipped in any
// release.
package ynabtestserver

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// Budget is one seeded budget.
type Budget struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	LastModified string `json:"last_modified_on"`
}

// Account is one seeded account of a budget.
type Account struct {
	BudgetID string `json:"-"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	OnBudget bool   `json:"on_budget"`
	Closed   bool   `json:"closed"`
	Balance  int64  `json:"balance"`
	Deleted  bool   `json:"deleted"`
}

// CategoryGroup is one seeded category group of a budget.
type CategoryGroup struct {
	BudgetID string `json:"-"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Hidden   bool   `json:"hidden"`
	Deleted  bool   `json:"deleted"`
}

// Category is one seeded category, belonging to a group.
type Category struct {
	BudgetID string `json:"-"`
	GroupID  string `json:"category_group_id"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Hidden   bool   `json:"hidden"`
	Deleted  bool   `json:"deleted"`
}

// Transaction is one seeded transaction. Amount is signed milliunits; Date is
// YYYY-MM-DD.
type Transaction struct {
	BudgetID          string  `json:"-"`
	ID                string  `json:"id"`
	Date              string  `json:"date"`
	Amount            int64   `json:"amount"`
	AmountFormatted   string  `json:"amount_formatted"`
	Memo              *string `json:"memo"`
	Cleared           string  `json:"cleared"`
	Approved          bool    `json:"approved"`
	FlagColor         *string `json:"flag_color"`
	AccountID         string  `json:"account_id"`
	AccountName       string  `json:"account_name"`
	PayeeName         *string `json:"payee_name"`
	CategoryID        *string `json:"category_id"`
	CategoryName      *string `json:"category_name"`
	TransferAccountID *string `json:"transfer_account_id"`
	Deleted           bool    `json:"deleted"`
	// Subtransactions is always serialized (an empty array for a plain
	// transaction), matching the live API.
	Subtransactions []any `json:"subtransactions"`
}

// Server is a running in-memory YNAB test server.
type Server struct {
	mu       sync.Mutex
	httptest *httptest.Server
	token    string

	budgets      []Budget
	accounts     []Account
	groups       []CategoryGroup
	categories   []Category
	transactions []Transaction

	requests int
}

// Start launches a server that requires the given bearer token (empty
// disables the check) and returns it. Close it when done.
func Start(token string) *Server { return StartAt(token, "") }

// StartAt is Start bound to a FIXED listen address (e.g. "127.0.0.1:43114")
// instead of an ephemeral port, for a bats lane that writes the base URL into
// its config. An empty addr keeps the ephemeral default; a bind failure is a
// panic (test-only code).
func StartAt(token, addr string) *Server {
	s := &Server{token: token}
	if addr == "" {
		s.httptest = httptest.NewServer(http.HandlerFunc(s.handle))
		return s
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		panic(fmt.Sprintf("ynabtestserver: listen %s: %v", addr, err))
	}
	s.httptest = httptest.NewUnstartedServer(http.HandlerFunc(s.handle))
	_ = s.httptest.Listener.Close()
	s.httptest.Listener = listener
	s.httptest.Start()
	return s
}

// URL is the server's base http URL.
func (s *Server) URL() string { return s.httptest.URL }

// BaseURL is the API base the plugin calls — the value an account's
// `session_url` carries, mirroring https://api.ynab.com/v1.
func (s *Server) BaseURL() string { return s.httptest.URL + "/v1" }

// Close shuts the server down.
func (s *Server) Close() { s.httptest.Close() }

// Requests is the number of API requests served so far (authorized or not).
func (s *Server) Requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

// AddBudget seeds a budget.
func (s *Server) AddBudget(b Budget) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.budgets = append(s.budgets, b)
}

// AddAccount seeds an account.
func (s *Server) AddAccount(a Account) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts = append(s.accounts, a)
}

// AddCategoryGroup seeds a category group.
func (s *Server) AddCategoryGroup(g CategoryGroup) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.groups = append(s.groups, g)
}

// AddCategory seeds a category.
func (s *Server) AddCategory(c Category) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.categories = append(s.categories, c)
}

// AddTransaction seeds a transaction.
func (s *Server) AddTransaction(t Transaction) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.Subtransactions == nil {
		t.Subtransactions = []any{}
	}
	s.transactions = append(s.transactions, t)
}

// handle routes one request.
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++

	if s.token != "" && r.Header.Get("Authorization") != "Bearer "+s.token {
		writeError(w, http.StatusUnauthorized, "401", "not_authorized", "Unauthorized")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "405", "method_not_allowed", "read-only server")
		return
	}

	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1"), "/")
	segs := strings.Split(path, "/")
	if segs[0] != "budgets" {
		writeError(w, http.StatusNotFound, "404.1", "not_found", "Resource not found")
		return
	}
	if len(segs) == 1 {
		s.serveBudgets(w, r)
		return
	}
	budgetID, ok := s.resolveBudgetID(segs[1])
	if !ok {
		writeError(w, http.StatusNotFound, "404.2", "resource_not_found", "budget not found")
		return
	}
	rest := segs[2:]
	switch {
	case len(rest) == 1 && rest[0] == "accounts":
		s.serveAccounts(w, budgetID)
	case len(rest) == 1 && rest[0] == "categories":
		s.serveCategories(w, budgetID)
	case len(rest) == 1 && rest[0] == "transactions":
		s.serveTransactions(w, r, budgetID, func(Transaction) bool { return true })
	case len(rest) == 2 && rest[0] == "transactions":
		s.serveTransaction(w, budgetID, rest[1])
	case len(rest) == 3 && rest[0] == "accounts" && rest[2] == "transactions":
		s.serveTransactions(w, r, budgetID, func(t Transaction) bool { return t.AccountID == rest[1] })
	case len(rest) == 3 && rest[0] == "categories" && rest[2] == "transactions":
		s.serveTransactions(w, r, budgetID, func(t Transaction) bool {
			return t.CategoryID != nil && *t.CategoryID == rest[1]
		})
	default:
		writeError(w, http.StatusNotFound, "404.1", "not_found", "Resource not found")
	}
}

// resolveBudgetID maps a path id (or the live API's `last-used` / `default`
// aliases, both the first seeded budget) to a seeded budget id.
func (s *Server) resolveBudgetID(id string) (string, bool) {
	if id == "last-used" || id == "default" {
		if len(s.budgets) == 0 {
			return "", false
		}
		return s.budgets[0].ID, true
	}
	for _, b := range s.budgets {
		if b.ID == id {
			return b.ID, true
		}
	}
	return "", false
}

func (s *Server) serveBudgets(w http.ResponseWriter, r *http.Request) {
	type budgetOut struct {
		Budget
		Accounts []Account `json:"accounts,omitempty"`
	}
	out := make([]budgetOut, 0, len(s.budgets))
	for _, b := range s.budgets {
		o := budgetOut{Budget: b}
		if r.URL.Query().Get("include_accounts") == "true" {
			for _, a := range s.accounts {
				if a.BudgetID == b.ID {
					o.Accounts = append(o.Accounts, a)
				}
			}
		}
		out = append(out, o)
	}
	writeData(w, map[string]any{"budgets": out, "default_budget": nil})
}

func (s *Server) serveAccounts(w http.ResponseWriter, budgetID string) {
	out := []Account{}
	for _, a := range s.accounts {
		if a.BudgetID == budgetID {
			out = append(out, a)
		}
	}
	writeData(w, map[string]any{"accounts": out, "server_knowledge": 1})
}

func (s *Server) serveCategories(w http.ResponseWriter, budgetID string) {
	type groupOut struct {
		CategoryGroup
		Categories []Category `json:"categories"`
	}
	out := []groupOut{}
	for _, g := range s.groups {
		if g.BudgetID != budgetID {
			continue
		}
		o := groupOut{CategoryGroup: g, Categories: []Category{}}
		for _, c := range s.categories {
			if c.BudgetID == budgetID && c.GroupID == g.ID {
				o.Categories = append(o.Categories, c)
			}
		}
		out = append(out, o)
	}
	writeData(w, map[string]any{"category_groups": out, "server_knowledge": 1})
}

// serveTransactions honors since_date (default: one year of history is NOT
// modeled — an omitted since_date returns everything) and until_date, as the
// live API does, and the keep predicate for the per-account / per-category
// endpoints.
func (s *Server) serveTransactions(
	w http.ResponseWriter, r *http.Request, budgetID string, keep func(Transaction) bool,
) {
	since := r.URL.Query().Get("since_date")
	until := r.URL.Query().Get("until_date")
	out := []Transaction{}
	for _, t := range s.transactions {
		if t.BudgetID != budgetID || !keep(t) {
			continue
		}
		if since != "" && t.Date < since {
			continue
		}
		if until != "" && t.Date > until {
			continue
		}
		out = append(out, t)
	}
	writeData(w, map[string]any{"transactions": out, "server_knowledge": 1})
}

func (s *Server) serveTransaction(w http.ResponseWriter, budgetID, id string) {
	for _, t := range s.transactions {
		if t.BudgetID == budgetID && t.ID == id {
			writeData(w, map[string]any{"transaction": t})
			return
		}
	}
	writeError(w, http.StatusNotFound, "404.2", "resource_not_found", "transaction not found")
}

func writeData(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func writeError(w http.ResponseWriter, status int, id, name, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"id": id, "name": name, "detail": detail},
	})
}
