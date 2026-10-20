package ynab

// The wire types below carry only the fields the plugin reads, with the
// nullability the live API was observed to use (FDR 0027 §Scope and API).
// Amounts are signed integer milliunits.

// Budget is a YNAB budget (the API's "plan") summary.
type Budget struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	LastModified string `json:"last_modified_on,omitempty"`
}

// Account is one account of a budget.
type Account struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type,omitempty"`
	OnBudget bool   `json:"on_budget"`
	Closed   bool   `json:"closed"`
	Balance  int64  `json:"balance"`
	Deleted  bool   `json:"deleted"`
}

// Category is one category within a group.
type Category struct {
	ID      string `json:"id"`
	GroupID string `json:"category_group_id,omitempty"`
	Name    string `json:"name"`
	Hidden  bool   `json:"hidden"`
	Deleted bool   `json:"deleted"`
}

// CategoryGroup is a category group with its categories nested.
type CategoryGroup struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Hidden     bool       `json:"hidden"`
	Deleted    bool       `json:"deleted"`
	Categories []Category `json:"categories"`
}

// SubTransaction is one leg of a split transaction.
type SubTransaction struct {
	ID                    string  `json:"id"`
	TransactionID         string  `json:"transaction_id"`
	Amount                int64   `json:"amount"`
	AmountFormatted       string  `json:"amount_formatted,omitempty"`
	Memo                  *string `json:"memo"`
	PayeeID               *string `json:"payee_id"`
	PayeeName             *string `json:"payee_name"`
	CategoryID            *string `json:"category_id"`
	CategoryName          *string `json:"category_name"`
	TransferAccountID     *string `json:"transfer_account_id"`
	TransferTransactionID *string `json:"transfer_transaction_id"`
	Deleted               bool    `json:"deleted"`
}

// Transaction is one transaction. For a split, CategoryName is the literal
// "Split" and Subtransactions holds the legs.
type Transaction struct {
	ID                    string           `json:"id"`
	Date                  string           `json:"date"`
	Amount                int64            `json:"amount"`
	AmountFormatted       string           `json:"amount_formatted,omitempty"`
	Memo                  *string          `json:"memo"`
	Cleared               string           `json:"cleared"`
	Approved              bool             `json:"approved"`
	FlagColor             *string          `json:"flag_color"`
	AccountID             string           `json:"account_id"`
	AccountName           string           `json:"account_name"`
	PayeeID               *string          `json:"payee_id"`
	PayeeName             *string          `json:"payee_name"`
	CategoryID            *string          `json:"category_id"`
	CategoryName          *string          `json:"category_name"`
	TransferAccountID     *string          `json:"transfer_account_id"`
	TransferTransactionID *string          `json:"transfer_transaction_id"`
	DebtTransactionType   *string          `json:"debt_transaction_type"`
	Deleted               bool             `json:"deleted"`
	Subtransactions       []SubTransaction `json:"subtransactions"`
}

// Response envelopes: every success body is {"data": {...}}.

type budgetsResponse struct {
	Data struct {
		Budgets []Budget `json:"budgets"`
	} `json:"data"`
}

type accountsResponse struct {
	Data struct {
		Accounts []Account `json:"accounts"`
	} `json:"data"`
}

type categoriesResponse struct {
	Data struct {
		CategoryGroups []CategoryGroup `json:"category_groups"`
	} `json:"data"`
}

type transactionsResponse struct {
	Data struct {
		Transactions []Transaction `json:"transactions"`
	} `json:"data"`
}

type transactionResponse struct {
	Data struct {
		Transaction Transaction `json:"transaction"`
	} `json:"data"`
}
