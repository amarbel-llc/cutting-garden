package fastmail

import (
	"context"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/config_common"
)

func account(name, rawURL string) config_common.Account {
	return config_common.Account{Root: config_common.Root{Name: name, URL: rawURL}}
}

func TestValidate_Accepts(t *testing.T) {
	cfg := AccountsConfig{Accounts: []config_common.Account{
		account("personal", "fastmail://personal/"),
		account("work", "fastmail://work/"),
	}}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestValidate_Rejects(t *testing.T) {
	cases := map[string]AccountsConfig{
		"empty name": {Accounts: []config_common.Account{account("", "fastmail://x/")}},
		"duplicate name": {Accounts: []config_common.Account{
			account("dup", "fastmail://dup/"), account("dup", "fastmail://dup/"),
		}},
		"empty url":    {Accounts: []config_common.Account{account("x", "")}},
		"wrong scheme": {Accounts: []config_common.Account{account("x", "caldav://x/")}},
		"host != name": {Accounts: []config_common.Account{account("x", "fastmail://y/")}},
	}
	for name, cfg := range cases {
		if err := cfg.Validate(); err == nil {
			t.Errorf("Validate(%s) = nil, want error", name)
		}
	}
}

func TestRoots_CredentialFree(t *testing.T) {
	setAccounts(t, acct("personal", "FASTMAIL_A"), acct("work", "FASTMAIL_B"))
	roots, err := (Plugin{}).Roots(context.Background())
	if err != nil {
		t.Fatalf("Roots: %v", err)
	}
	if len(roots) != 2 {
		t.Fatalf("want 2 roots, got %d", len(roots))
	}
	for _, r := range roots {
		if r.Scheme != schemeFastmail {
			t.Errorf("root scheme = %q", r.Scheme)
		}
		if r.User != nil {
			t.Errorf("root %s carries userinfo", r)
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
		t.Errorf("want 0 roots, got %d", len(roots))
	}
}

func TestRootLabels(t *testing.T) {
	setAccounts(t, acct("personal", "FASTMAIL_A"))
	labels, err := (Plugin{}).RootLabels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := accountRootURI("personal").String()
	if labels[want] != "personal" {
		t.Errorf("RootLabels[%q] = %q, want personal", want, labels[want])
	}
}
