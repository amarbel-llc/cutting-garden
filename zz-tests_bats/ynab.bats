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
