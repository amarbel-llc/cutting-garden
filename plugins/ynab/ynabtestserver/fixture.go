package ynabtestserver

// Token is the bearer token the standalone testserver (and the bats lane's
// config) agree on. It is a placeholder, not a secret.
const Token = "test-token"

// SeedFixture seeds the deterministic, PII-free fixture the bats lane
// (zz-tests_bats/ynab.bats) asserts against. All dates are fixed (2026-06 to
// 2026-08), so lane vectors never drift with the host clock; the lane names an
// explicit date window rather than relying on the plugin's rolling default.
//
//	Household (b-household)             Archive (b-archive, empty)
//	  accounts:   Checking, Savings
//	  categories: Food{Groceries, Dining Out}, Bills{Rent}
//	  transactions, in server order:
//	    06-03 Grocer       -54.20  Checking  Groceries  cleared
//	    06-12 Landlord    -900.00  Checking  Rent       reconciled
//	    07-02 Corner Cafe  -12.34  Checking  Dining Out cleared
//	    07-09 Grocer       -61.05  Checking  Groceries  uncleared
//	    07-15 Paycheck   +2500.00  Checking  (none)     cleared
//	    07-31 Landlord    -900.00  Checking  Rent       cleared
//	    08-05 Transfer     -200.00 Checking  (none)     cleared   transfer
//	    08-05 Transfer     +200.00 Savings   (none)     cleared   transfer
func SeedFixture(s *Server) {
	s.AddBudget(Budget{ID: "b-household", Name: "Household", LastModified: "2026-08-05T12:00:00Z"})
	s.AddBudget(Budget{ID: "b-archive", Name: "Archive", LastModified: "2024-01-01T00:00:00Z"})

	s.AddAccount(Account{BudgetID: "b-household", ID: "a-chk", Name: "Checking", Type: "checking", OnBudget: true})
	s.AddAccount(Account{BudgetID: "b-household", ID: "a-sav", Name: "Savings", Type: "savings", OnBudget: true})

	s.AddCategoryGroup(CategoryGroup{BudgetID: "b-household", ID: "g-food", Name: "Food"})
	s.AddCategoryGroup(CategoryGroup{BudgetID: "b-household", ID: "g-bills", Name: "Bills"})
	s.AddCategory(Category{BudgetID: "b-household", GroupID: "g-food", ID: "c-groc", Name: "Groceries"})
	s.AddCategory(Category{BudgetID: "b-household", GroupID: "g-food", ID: "c-dine", Name: "Dining Out"})
	s.AddCategory(Category{BudgetID: "b-household", GroupID: "g-bills", ID: "c-rent", Name: "Rent"})

	str := func(v string) *string { return &v }
	txn := func(id, date string, amount int64, formatted, cleared, account, accountName, payee string) Transaction {
		return Transaction{
			BudgetID: "b-household", ID: id, Date: date, Amount: amount, AmountFormatted: formatted,
			Cleared: cleared, Approved: true, AccountID: account, AccountName: accountName,
			PayeeName: str(payee),
		}
	}
	categorize := func(t Transaction, id, name string) Transaction {
		t.CategoryID, t.CategoryName = str(id), str(name)
		return t
	}

	s.AddTransaction(categorize(
		txn("t-0603", "2026-06-03", -54200, "-$54.20", "cleared", "a-chk", "Checking", "Grocer"),
		"c-groc", "Groceries"))
	s.AddTransaction(categorize(
		txn("t-0612", "2026-06-12", -900000, "-$900.00", "reconciled", "a-chk", "Checking", "Landlord"),
		"c-rent", "Rent"))
	s.AddTransaction(categorize(
		txn("t-0702", "2026-07-02", -12340, "-$12.34", "cleared", "a-chk", "Checking", "Corner Cafe"),
		"c-dine", "Dining Out"))
	s.AddTransaction(categorize(
		txn("t-0709", "2026-07-09", -61050, "-$61.05", "uncleared", "a-chk", "Checking", "Grocer"),
		"c-groc", "Groceries"))
	s.AddTransaction(
		txn("t-0715", "2026-07-15", 2500000, "$2,500.00", "cleared", "a-chk", "Checking", "Paycheck"))
	s.AddTransaction(categorize(
		txn("t-0731", "2026-07-31", -900000, "-$900.00", "cleared", "a-chk", "Checking", "Landlord"),
		"c-rent", "Rent"))

	out := txn("t-0805a", "2026-08-05", -200000, "-$200.00", "cleared", "a-chk", "Checking", "Transfer : Savings")
	out.TransferAccountID = str("a-sav")
	s.AddTransaction(out)
	in := txn("t-0805b", "2026-08-05", 200000, "$200.00", "cleared", "a-sav", "Savings", "Transfer : Checking")
	in.TransferAccountID = str("a-chk")
	s.AddTransaction(in)
}
