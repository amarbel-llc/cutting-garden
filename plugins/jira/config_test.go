package jira

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/config_common"
)

func setAccounts(t *testing.T, accts ...config_common.Account) {
	t.Helper()
	SetConfiguredAccounts(accts)
	t.Cleanup(func() { SetConfiguredAccounts(nil) })
}

func acct(name, rawURL, user, pwEnv string) config_common.Account {
	return config_common.Account{
		Root:        config_common.Root{Name: name, URL: rawURL},
		Username:    user,
		PasswordEnv: pwEnv,
	}
}

func TestRoots_CredentialFreeAndParsed(t *testing.T) {
	setAccounts(
		t,
		acct("acme", "jira://acme.atlassian.net/PROJ", "me@x.io", "JIRA_ACME"),
		acct("other", "jira://acme.atlassian.net/OTHER", "me@x.io", "JIRA_OTHER"),
	)
	roots, err := (Plugin{}).Roots(context.Background())
	if err != nil {
		t.Fatalf("Roots: %v", err)
	}
	if len(roots) != 2 {
		t.Fatalf("want 2 roots, got %d", len(roots))
	}
	for _, r := range roots {
		if r.User != nil {
			t.Errorf("surfaced root %s carries userinfo", r)
		}
		if r.Scheme != schemeJira {
			t.Errorf("root scheme = %q, want %q", r.Scheme, schemeJira)
		}
	}
}

func TestRoots_EmptyWhenNoAccounts(t *testing.T) {
	setAccounts(t)
	roots, err := (Plugin{}).Roots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 0 {
		t.Errorf("want 0 roots with no accounts, got %d", len(roots))
	}
}

func TestMatchAccount_LongestPrefixAndHost(t *testing.T) {
	setAccounts(
		t,
		acct("host", "jira://acme.atlassian.net", "u", ""),
		acct("proj", "jira://acme.atlassian.net/PROJ", "u", ""),
	)
	if got, ok := matchAccount("acme.atlassian.net", "PROJ/PROJ-1"); !ok || got.Name != "proj" {
		t.Errorf("longest prefix: got %q ok=%v, want proj", got.Name, ok)
	}
	if got, ok := matchAccount("acme.atlassian.net", "OTHER/OTHER-1"); !ok || got.Name != "host" {
		t.Errorf("shorter prefix: got %q ok=%v, want host", got.Name, ok)
	}
	if _, ok := matchAccount("other.host", "PROJ"); ok {
		t.Error("want no match on a different host")
	}
}

func TestConnectionFromArg_Precedence(t *testing.T) {
	setAccounts(t, acct("proj", "jira://acme.atlassian.net/PROJ", "acctuser", "JIRA_TEST_TOKEN"))
	t.Setenv("JIRA_TEST_TOKEN", "acctsecret")
	t.Setenv(envUsername, "envuser")
	t.Setenv(envToken, "envsecret")

	// Step 2: node matches the account → account credentials.
	u, _ := url.Parse("jira://acme.atlassian.net/PROJ/PROJ-1")
	if _, user, tok, err := connectionFromArg(u); err != nil {
		t.Fatal(err)
	} else if user != "acctuser" || tok != "acctsecret" {
		t.Errorf("account creds: got %q/%q, want acctuser/acctsecret", user, tok)
	}

	// Step 3: non-matching host → global env fallback.
	u, _ = url.Parse("jira://other.host/X")
	if _, user, tok, err := connectionFromArg(u); err != nil {
		t.Fatal(err)
	} else if user != "envuser" || tok != "envsecret" {
		t.Errorf("env fallback: got %q/%q, want envuser/envsecret", user, tok)
	}

	// Step 1: explicit URI userinfo overrides the account, and the
	// returned base is stripped of credentials.
	u, _ = url.Parse("jira://uriuser:uritok@acme.atlassian.net/PROJ")
	base, user, tok, err := connectionFromArg(u)
	if err != nil {
		t.Fatal(err)
	}
	if user != "uriuser" || tok != "uritok" {
		t.Errorf("userinfo: got %q/%q, want uriuser/uritok", user, tok)
	}
	if strings.Contains(base, "uriuser") {
		t.Errorf("base leaks userinfo: %q", base)
	}
}

func TestValidate_RejectsBadAccounts(t *testing.T) {
	cases := []struct {
		name string
		cfg  AccountsConfig
	}{
		{"empty name", AccountsConfig{Accounts: []config_common.Account{acct("", "jira://h/X", "u", "")}}},
		{"empty url", AccountsConfig{Accounts: []config_common.Account{acct("a", "", "u", "")}}},
		{"wrong scheme", AccountsConfig{Accounts: []config_common.Account{acct("a", "https://h/X", "u", "")}}},
		{"dup name", AccountsConfig{Accounts: []config_common.Account{
			acct("a", "jira://h/X", "u", ""), acct("a", "jira://h/Y", "u", ""),
		}}},
		{"dup host+path", AccountsConfig{Accounts: []config_common.Account{
			acct("a", "jira://h/X", "u", ""), acct("b", "jira://h/X", "u", ""),
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cfg.Validate(); err == nil {
				t.Errorf("Validate(%s) = nil, want error", tc.name)
			}
		})
	}
}
