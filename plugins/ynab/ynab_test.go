package ynab

import (
	"net/url"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/config_common"
)

// mustParseURL parses raw or fails the test.
func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

// setAccounts installs the given accounts as package state for the test and
// clears them afterward.
func setAccounts(t *testing.T, accts ...config_common.Account) {
	t.Helper()
	SetConfiguredAccounts(accts)
	t.Cleanup(func() { SetConfiguredAccounts(nil) })
}

// account builds a synthetic ynab account whose URL host equals its name.
func account(name string) config_common.Account {
	return config_common.Account{
		Root:        config_common.Root{Name: name, URL: "ynab://" + name + "/"},
		PasswordEnv: "YNAB_TEST_TOKEN",
	}
}
