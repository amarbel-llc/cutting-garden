#! /usr/bin/env bats

# shellcheck disable=SC2016  # the expected output carries a literal `$` (currency)

# The ynab plugin's read lane (FDR 0027 slice 1c, cutting-garden#328): cg list
# and cg mcp over the `test` account, backed by the in-memory YNAB server seeded
# with its deterministic fixture (see lib/ynab.bash). Every test starts a fresh
# server. The fixture's dates are fixed in 2026-06..08, so every assertion that
# touches transactions names an explicit date window; the plugin's rolling
# 30-day default is covered only for what it must NOT claim (completeness).

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/vectors.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/ynab.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/mcp.bash"
  export output
  start_ynab_server
  write_ynab_config
  init_store
}

teardown() {
  stop_ynab_server
}

# bats file_tags=ynab

function ynab_list_root_shows_budgets { # @test
  run_cg list -format json ynab://test/
  assert_success
  run jq -c '[.uri, .name, .type]' <<<"$output"
  assert_output - <<-'EOM'
	["ynab://test/Household/","Household","cutting_garden-ynab-budget-v1"]
	["ynab://test/Archive/","Archive","cutting_garden-ynab-budget-v1"]
	EOM
}

function ynab_list_budget_shows_its_sections { # @test
  run_cg list -format json ynab://test/Household/
  assert_success
  run jq -r '.name' <<<"$output"
  assert_output - <<-'EOM'
	accounts
	categories
	transactions
	payees
	EOM
}

function ynab_list_payees_by_id { # @test
  run_cg list -format json ynab://test/Household/payees/
  assert_success
  run jq -c '[.uri, .name, .type]' <<<"$output"
  assert_output - <<-'EOM'
	["ynab://test/Household/payees/p-grocer","Grocer","cutting_garden-ynab-payee-v1"]
	["ynab://test/Household/payees/p-landlord","Landlord","cutting_garden-ynab-payee-v1"]
	["ynab://test/Household/payees/p-cafe","Corner Cafe","cutting_garden-ynab-payee-v1"]
	["ynab://test/Household/payees/p-cafe-twin","The Corner Cafe","cutting_garden-ynab-payee-v1"]
	["ynab://test/Household/payees/p-paycheck","Paycheck","cutting_garden-ynab-payee-v1"]
	["ynab://test/Household/payees/p-xfer-sav","Transfer : Savings","cutting_garden-ynab-payee-v1"]
	["ynab://test/Household/payees/p-xfer-chk","Transfer : Checking","cutting_garden-ynab-payee-v1"]
	EOM
}

function ynab_list_accounts_and_categories { # @test
  run_cg list -format json ynab://test/Household/accounts/
  assert_success
  run jq -r '.name' <<<"$output"
  assert_output - <<-'EOM'
	Checking
	Savings
	EOM

  run_cg list -format json ynab://test/Household/categories/
  assert_success
  run jq -r '.name' <<<"$output"
  assert_output - <<-'EOM'
	Food
	Bills
	EOM

  run_cg list -format json ynab://test/Household/categories/Food/
  assert_success
  run jq -r '.uri' <<<"$output"
  assert_output - <<-'EOM'
	ynab://test/Household/categories/Food/Groceries/
	ynab://test/Household/categories/Food/Dining%20Out/
	EOM
}

# A date predicate is pushed down to the server as since_date/until_date: only
# July's four transactions are fetched, and the plugin (not the host) applies
# the filter.
function ynab_mcp_list_nodes_pushes_a_month_down { # @test
  ynab_mcp_list ynab://test/Household/transactions/ date=2026-07
  text="$(mcp_result_text "$output" 3)"
  run jq -r '.filterMode' <<<"$text"
  assert_output plugin
  run jq -r '.nodes[].name' <<<"$text"
  assert_output - <<-'EOM'
	2026-07-02 Corner Cafe -$12.34
	2026-07-09 Grocer -$61.05
	2026-07-15 Paycheck $2,500.00
	2026-07-31 Landlord -$900.00
	EOM
}

# Listing nodes carry their facets and listing fields inline.
function ynab_mcp_list_nodes_carries_facets_and_fields { # @test
  ynab_mcp_list ynab://test/Household/transactions/ date=2026-07-02
  text="$(mcp_result_text "$output" 3)"
  run jq -c '.nodes[0] | [.facets.category, .facets.cleared, .facets.amount_band, .fields.payee, .fields.amount]' <<<"$text"
  assert_output '[["Dining Out"],["cleared"],["10_to_50"],"Corner Cafe","-$12.34"]'
}

function ynab_mcp_read_facets_summarizes_a_month_completely { # @test
  ynab_mcp_facets ynab://test/Household/transactions/ date=2026-07
  text="$(mcp_result_text "$output" 3)"
  run jq -c '[.complete, .facets.account, .facets.cleared, .facets.category]' <<<"$text"
  assert_output '[true,{"Checking":4},{"cleared":3,"uncleared":1},{"Dining Out":1,"Groceries":1,"Rent":1,"uncategorized":1}]'
}

function ynab_mcp_read_facets_over_a_year_counts_transfers { # @test
  ynab_mcp_facets ynab://test/Household/transactions/ date=2026
  text="$(mcp_result_text "$output" 3)"
  run jq -c '[.complete, .facets.account, .facets.transfer]' <<<"$text"
  assert_output '[true,{"Checking":7,"Savings":1},{"no":6,"yes":2}]'
}

# Without a date predicate the window is the short rolling default, so the
# summary must never claim to cover the whole history — whatever today's date.
function ynab_mcp_read_facets_default_window_is_incomplete { # @test
  ynab_mcp_facets ynab://test/Household/transactions/ ""
  run jq -r '.complete' <<<"$(mcp_result_text "$output" 3)"
  assert_output false
}

function ynab_mcp_account_drill_down_uses_its_own_scope { # @test
  ynab_mcp_list ynab://test/Household/accounts/Checking/ date=2026-08
  run jq -r '.nodes[].name' <<<"$(mcp_result_text "$output" 3)"
  assert_output '2026-08-05 Transfer : Savings -$200.00'

  ynab_mcp_list ynab://test/Household/accounts/Savings/ date=2026-08
  run jq -r '.nodes[].name' <<<"$(mcp_result_text "$output" 3)"
  assert_output '2026-08-05 Transfer : Checking $200.00'
}

function ynab_mcp_category_drill_down { # @test
  ynab_mcp_list 'ynab://test/Household/categories/Bills/Rent/' date=2026
  run jq -r '.nodes[].name' <<<"$(mcp_result_text "$output" 3)"
  assert_output - <<-'EOM'
	2026-06-12 Landlord -$900.00
	2026-07-31 Landlord -$900.00
	EOM
}

function ynab_mcp_read_node_returns_the_transaction { # @test
  mcp_drive ynab://test/ "$(tools_call 3 read_node '{"uri":"ynab://test/Household/transactions/t-0702"}')"
  run jq -r '[.id, .amount, .cleared, .category_name, .payee_name] | @tsv' \
    <<<"$(mcp_result_text "$output" 3 | sed '/^raw bytes:/d')"
  assert_output "$(printf 't-0702\t-12340\tcleared\tDining Out\tCorner Cafe')"
}

# Slice 2 (cutting-garden#331): the write path through the real binary. One
# mutate_nodes patch re-categorizes a transaction and sets its memo, and the
# next read, from a fresh process, sees both.
function ynab_mcp_patch_recategorizes_a_transaction { # @test
  mcp_drive ynab://test/ "$(tools_call 3 mutate_nodes '{"groups":[{"anchor":"ynab://test/Household/transactions/","ops":[{"kind":"patch","id":"t-0702","body":"{\"category\":\"Groceries\",\"memo\":\"moved by bats\"}"}]}]}')"
  run jq -r '.groups[0].applied | join(",")' <<<"$(mcp_result_text "$output" 3)"
  assert_output t-0702

  ynab_mcp_list ynab://test/Household/transactions/ date=2026-07-02
  run jq -c '.nodes[0] | [.facets.category, .fields.category, .fields.memo]' <<<"$(mcp_result_text "$output" 3)"
  assert_output '[["Groceries"],"Groceries","moved by bats"]'
}

# A reconciled transaction's cleared state is locked: the patch is refused and
# nothing changes.
function ynab_mcp_patch_refuses_a_reconciled_cleared_change { # @test
  mcp_drive ynab://test/ "$(tools_call 3 mutate_nodes '{"groups":[{"anchor":"ynab://test/Household/transactions/","ops":[{"kind":"patch","id":"t-0612","body":"{\"cleared\":\"uncleared\"}"}]}]}')"
  assert_equal "$(mcp_is_error "$output" 3)" "true"
  text="$(mcp_result_text "$output" 3)"
  [[ $text == *reconciled* ]] || fail "the refusal does not say the transaction is reconciled: $text"

  ynab_mcp_list ynab://test/Household/transactions/ date=2026-06-12
  run jq -r '.nodes[0].facets.cleared[0]' <<<"$(mcp_result_text "$output" 3)"
  assert_output reconciled
}

# A bare category name shared by no other category is enough; one that matches
# nothing is refused, not guessed.
function ynab_mcp_patch_refuses_an_unknown_category { # @test
  mcp_drive ynab://test/ "$(tools_call 3 mutate_nodes '{"groups":[{"anchor":"ynab://test/Household/transactions/","ops":[{"kind":"patch","id":"t-0702","body":"{\"category\":\"Nonesuch\"}"}]}]}')"
  assert_equal "$(mcp_is_error "$output" 3)" "true"
  text="$(mcp_result_text "$output" 3)"
  [[ $text == *"no category named"* ]] || fail "unknown category not refused by name: $text"

  ynab_mcp_list ynab://test/Household/transactions/ date=2026-07-02
  run jq -r '.nodes[0].facets.category[0]' <<<"$(mcp_result_text "$output" 3)"
  assert_output "Dining Out"
}

# Payees (cutting-garden#333). A payee reads as its JSON, and a transaction
# can be repointed to ANOTHER EXISTING payee by name; the read-back goes through
# cg itself.
function ynab_mcp_read_node_returns_a_payee { # @test
  mcp_drive ynab://test/ "$(tools_call 3 read_node '{"uri":"ynab://test/Household/payees/p-cafe-twin"}')"
  run jq -r '[.id, .name] | @tsv' <<<"$(mcp_result_text "$output" 3 | sed '/^raw bytes:/d')"
  assert_output "$(printf 'p-cafe-twin\tThe Corner Cafe')"
}

function ynab_mcp_patch_repoints_a_transactions_payee { # @test
  mcp_drive ynab://test/ "$(tools_call 3 mutate_nodes '{"groups":[{"anchor":"ynab://test/Household/transactions/","ops":[{"kind":"patch","id":"t-0702","body":"{\"payee\":\"The Corner Cafe\"}"}]}]}')"
  run jq -r '.groups[0].applied | join(",")' <<<"$(mcp_result_text "$output" 3)"
  assert_output t-0702

  ynab_mcp_list ynab://test/Household/transactions/ date=2026-07-02
  run jq -c '.nodes[0] | [.facets.payee, .fields.payee, .name]' <<<"$(mcp_result_text "$output" 3)"
  assert_output '[["The Corner Cafe"],"The Corner Cafe","2026-07-02 The Corner Cafe -$12.34"]'
}

# A payee name that exists nowhere is refused, never created: YNAB would mint a
# payee from an unmatched name, so a typo would fork one.
function ynab_mcp_patch_refuses_an_unknown_payee { # @test
  mcp_drive ynab://test/ "$(tools_call 3 mutate_nodes '{"groups":[{"anchor":"ynab://test/Household/transactions/","ops":[{"kind":"patch","id":"t-0702","body":"{\"payee\":\"Corner Cafee\"}"}]}]}')"
  assert_equal "$(mcp_is_error "$output" 3)" "true"
  text="$(mcp_result_text "$output" 3)"
  [[ $text == *"no payee named"* ]] || fail "unknown payee not refused by name: $text"

  run_cg list -format json ynab://test/Household/payees/
  assert_success
  run jq -s 'length' <<<"$output"
  assert_output 7
}

# Renaming is the other payee write. The payee keeps its URI (it is addressed by
# id), and its transactions read under the new name.
function ynab_mcp_rename_a_payee { # @test
  mcp_drive ynab://test/ "$(tools_call 3 mutate_nodes '{"groups":[{"anchor":"ynab://test/Household/payees/","ops":[{"kind":"patch","id":"p-cafe-twin","body":"{\"name\":\"Corner Cafe (Downtown)\"}"}]}]}')"
  run jq -r '.groups[0].applied | join(",")' <<<"$(mcp_result_text "$output" 3)"
  assert_output p-cafe-twin

  run_cg list -format json ynab://test/Household/payees/
  assert_success
  run jq -r 'select(.uri | endswith("/p-cafe-twin")) | .name' <<<"$output"
  assert_output "Corner Cafe (Downtown)"
}

# A rename onto a name another payee holds is refused: what YNAB does with it
# is undocumented, so the plugin never asks.
function ynab_mcp_rename_refuses_a_name_another_payee_holds { # @test
  mcp_drive ynab://test/ "$(tools_call 3 mutate_nodes '{"groups":[{"anchor":"ynab://test/Household/payees/","ops":[{"kind":"patch","id":"p-cafe-twin","body":"{\"name\":\"Corner Cafe\"}"}]}]}')"
  assert_equal "$(mcp_is_error "$output" 3)" "true"
  text="$(mcp_result_text "$output" 3)"
  [[ $text == *"would collide"* ]] || fail "the collision is not named: $text"

  run_cg list -format json ynab://test/Household/payees/
  run jq -r 'select(.uri | endswith("/p-cafe-twin")) | .name' <<<"$output"
  assert_output "The Corner Cafe"
}

# organize anchors at a URI, so the month is addressed as `?date=` on the
# anchor (a `date=` query predicate is not pushed down on that path).
generate_month() {
  run_cg organize -group-by category= 'ynab://test/Household/transactions/?date=2026-07'
  assert_success
  assert_vector - <<-'EOM'
	---
	% generated: `cg organize '"ynab://test/Household/transactions/?date=2026-07"' category=`
	- _base = @blake2b256-f3s50c6zyf5q5r63scgurucvc8593fc4cvv0e8sgdfhm2s8dvzqql84m8y
	- _anchor = ynab://test/Household/transactions/?date=2026-07
	- _type = !cutting_garden-ynab-transaction-v1
	! organize-base-v1
	---

	# category=

	## ="Dining Out"

	- [t-0702] 2026-07-02 Corner Cafe -$12.34

	## =Groceries

	- [t-0709] 2026-07-09 Grocer -$61.05

	## =Rent

	- [t-0731] 2026-07-31 Landlord -$900.00

	## =uncategorized

	- [t-0715] 2026-07-15 Paycheck $2,500.00
	EOM
}

function ynab_organize_groups_a_month_by_category { # @test
  generate_month
}

# Moving a line under another category heading re-categorizes the transaction
# in YNAB; the read-back goes through cg itself.
function ynab_organize_apply_recategorizes_by_moving_a_line { # @test
  generate_month
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize '"ynab://test/Household/transactions/?date=2026-07"' category=`
	- _base = @blake2b256-f3s50c6zyf5q5r63scgurucvc8593fc4cvv0e8sgdfhm2s8dvzqql84m8y
	- _anchor = ynab://test/Household/transactions/?date=2026-07
	- _type = !cutting_garden-ynab-transaction-v1
	! organize-base-v1
	---

	# category=

	## ="Dining Out"

	## =Groceries

	- [t-0702] 2026-07-02 Corner Cafe -$12.34
	- [t-0709] 2026-07-09 Grocer -$61.05

	## =Rent

	- [t-0731] 2026-07-31 Landlord -$900.00

	## =uncategorized

	- [t-0715] 2026-07-15 Paycheck $2,500.00
	EOM

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_vector - <<'EOF'
organize: 1 change(s):

  - [t-0702 category=[-"Dining Out"-]{+Groceries+}] 2026-07-02 Corner Cafe -$12.34

organize: wrote 1 change(s)
EOF

  ynab_mcp_list ynab://test/Household/transactions/ date=2026-07-02
  run jq -r '.nodes[0].facets.category[0]' <<<"$(mcp_result_text "$output" 3)"
  assert_output Groceries
}

# The payee cleanup in one organize pass: group a month by payee, drag a line
# under another payee's heading, apply.
generate_month_by_payee() {
  run_cg organize -group-by payee= 'ynab://test/Household/transactions/?date=2026-07'
  assert_success
  assert_vector - <<-'EOM'
	---
	% generated: `cg organize '"ynab://test/Household/transactions/?date=2026-07"' payee=`
	- _base = @blake2b256-9469xht5748ynt7n8lk53ld4esde3rfpym0rj0g7xq4348c5a44sc3rhfx
	- _anchor = ynab://test/Household/transactions/?date=2026-07
	- _type = !cutting_garden-ynab-transaction-v1
	! organize-base-v1
	---

	# payee=

	## ="Corner Cafe"

	- [t-0702] 2026-07-02 Corner Cafe -$12.34

	## =Grocer

	- [t-0709] 2026-07-09 Grocer -$61.05

	## =Landlord

	- [t-0731] 2026-07-31 Landlord -$900.00

	## =Paycheck

	- [t-0715] 2026-07-15 Paycheck $2,500.00
	EOM
}

function ynab_organize_groups_a_month_by_payee { # @test
  generate_month_by_payee
}

# Moving a line under another payee's heading repoints the transaction to that
# existing payee: the redundant "Corner Cafe" line joins its twin. The twin has
# no transactions in the month, so the edit adds its heading.
function ynab_organize_apply_repoints_the_payee_by_moving_a_line { # @test
  generate_month_by_payee
  local edited="$BATS_TEST_TMPDIR/edited.txt"
  cat >"$edited" <<-'EOM'
	---
	% generated: `cg organize '"ynab://test/Household/transactions/?date=2026-07"' payee=`
	- _base = @blake2b256-9469xht5748ynt7n8lk53ld4esde3rfpym0rj0g7xq4348c5a44sc3rhfx
	- _anchor = ynab://test/Household/transactions/?date=2026-07
	- _type = !cutting_garden-ynab-transaction-v1
	! organize-base-v1
	---

	# payee=

	## ="Corner Cafe"

	## ="The Corner Cafe"

	- [t-0702] 2026-07-02 Corner Cafe -$12.34

	## =Grocer

	- [t-0709] 2026-07-09 Grocer -$61.05

	## =Landlord

	- [t-0731] 2026-07-31 Landlord -$900.00

	## =Paycheck

	- [t-0715] 2026-07-15 Paycheck $2,500.00
	EOM

  run_cg organize -apply "$edited" -commit
  assert_success
  assert_vector - <<'EOF'
organize: 1 change(s):

  - [t-0702 payee=[-"Corner Cafe"-]{+"The Corner Cafe"+}] 2026-07-02 Corner Cafe -$12.34

organize: wrote 1 change(s)
EOF

  ynab_mcp_list ynab://test/Household/transactions/ date=2026-07-02
  run jq -r '.nodes[0].facets.payee[0]' <<<"$(mcp_result_text "$output" 3)"
  assert_output "The Corner Cafe"
}

function ynab_a_rejected_token_names_the_token { # @test
  write_ynab_config wrong-token
  run_cg list ynab://test/
  assert_failure
  assert_output --partial 'access token was rejected'
}

function ynab_unknown_budget_is_a_usage_error { # @test
  run_cg list ynab://test/Nonesuch/
  assert_failure
  assert_output --partial 'no budget named'
}
