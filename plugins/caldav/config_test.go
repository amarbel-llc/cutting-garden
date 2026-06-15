package caldav

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/pkgs/config_common"
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
		acct("personal", "caldav://dav.host/dav/me/", "me", "CALDAV_PERSONAL"),
		acct("team", "caldav://dav.host/dav/team/", "me", "CALDAV_TEAM"),
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
		if r.Scheme != schemeCalDAV {
			t.Errorf("root scheme = %q, want %q", r.Scheme, schemeCalDAV)
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
		acct("root", "caldav://dav.host/dav/", "u", ""),
		acct("me", "caldav://dav.host/dav/me/", "u", ""),
	)
	if got, ok := matchAccount("dav.host", "/dav/me/work/"); !ok || got.Name != "me" {
		t.Errorf("longest prefix: got %q ok=%v, want me", got.Name, ok)
	}
	if got, ok := matchAccount("dav.host", "/dav/other/"); !ok || got.Name != "root" {
		t.Errorf("shorter prefix: got %q ok=%v, want root", got.Name, ok)
	}
	if _, ok := matchAccount("other.host", "/dav/me/"); ok {
		t.Error("want no match on a different host")
	}
}

func TestConnectionFromArg_Precedence(t *testing.T) {
	setAccounts(t, acct("me", "caldav://dav.host/dav/me/", "acctuser", "CALDAV_TEST_PW"))
	t.Setenv("CALDAV_TEST_PW", "acctsecret")
	t.Setenv(envUsername, "envuser")
	t.Setenv(envPassword, "envsecret")

	// Step 2: node matches the account → account credentials.
	u, _ := url.Parse("caldav://dav.host/dav/me/work/")
	if _, user, pw, err := connectionFromArg(u); err != nil {
		t.Fatal(err)
	} else if user != "acctuser" || pw != "acctsecret" {
		t.Errorf("account creds: got %q/%q, want acctuser/acctsecret", user, pw)
	}

	// Step 3: non-matching host → global env fallback.
	u, _ = url.Parse("caldav://other.host/dav/x/")
	if _, user, pw, err := connectionFromArg(u); err != nil {
		t.Fatal(err)
	} else if user != "envuser" || pw != "envsecret" {
		t.Errorf("env fallback: got %q/%q, want envuser/envsecret", user, pw)
	}

	// Step 1: explicit URI userinfo overrides the account, and the
	// returned base is stripped of credentials.
	u, _ = url.Parse("caldav://uriuser:uripass@dav.host/dav/me/")
	base, user, pw, err := connectionFromArg(u)
	if err != nil {
		t.Fatal(err)
	}
	if user != "uriuser" || pw != "uripass" {
		t.Errorf("userinfo: got %q/%q, want uriuser/uripass", user, pw)
	}
	if strings.Contains(base, "uriuser") {
		t.Errorf("base leaks userinfo: %q", base)
	}
}
