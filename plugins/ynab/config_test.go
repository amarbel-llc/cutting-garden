package ynab

import (
	"context"
	stderrors "errors"
	"slices"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/config_common"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/tommy/pkg/cst"
)

func withSessionURL(a config_common.Account, sessionURL string) config_common.Account {
	a.SessionURL = sessionURL
	return a
}

func plainAccount(name, rawURL string) config_common.Account {
	return config_common.Account{Root: config_common.Root{Name: name, URL: rawURL}}
}

// sectionModel decomposes the body of a `[ynab]` table — what the SDK's
// config-section dispatch hands the registered decoder.
func sectionModel(t *testing.T, body string) *cst.Value {
	t.Helper()
	model, err := cst.DecomposeBytes([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return model
}

func TestValidate_Accepts(t *testing.T) {
	cfg := AccountsConfig{Accounts: []config_common.Account{
		plainAccount("personal", "ynab://personal/"),
		plainAccount("work", "ynab://work/"),
		withSessionURL(plainAccount("test", "ynab://test/"), "http://127.0.0.1:43114/v1"),
		withSessionURL(plainAccount("local", "ynab://local/"), "http://localhost:43114/v1"),
		withSessionURL(plainAccount("v6", "ynab://v6/"), "http://[::1]:43114/v1"),
		withSessionURL(plainAccount("mirror", "ynab://mirror/"), "https://proxy.example/v1"),
	}}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestValidate_Rejects(t *testing.T) {
	cases := map[string]AccountsConfig{
		"empty name": {Accounts: []config_common.Account{plainAccount("", "ynab://x/")}},
		"duplicate name": {Accounts: []config_common.Account{
			plainAccount("dup", "ynab://dup/"), plainAccount("dup", "ynab://dup/"),
		}},
		"empty url":    {Accounts: []config_common.Account{plainAccount("x", "")}},
		"wrong scheme": {Accounts: []config_common.Account{plainAccount("x", "caldav://x/")}},
		"host != name": {Accounts: []config_common.Account{plainAccount("x", "ynab://y/")}},
		"relative session_url": {Accounts: []config_common.Account{
			withSessionURL(plainAccount("x", "ynab://x/"), "/v1"),
		}},
		"non-http session_url": {Accounts: []config_common.Account{
			withSessionURL(plainAccount("x", "ynab://x/"), "ftp://host/v1"),
		}},
		"plaintext http to a non-loopback host": {Accounts: []config_common.Account{
			withSessionURL(plainAccount("x", "ynab://x/"), "http://example.com/v1"),
		}},
	}
	for name, cfg := range cases {
		err := cfg.Validate()
		if err == nil {
			t.Errorf("Validate(%s) = nil, want error", name)
			continue
		}
		if !errors.Is400BadRequest(err) {
			t.Errorf("Validate(%s): want EX_USAGE (400), got %v", name, err)
		}
	}
}

func TestConfigSection_RegisteredUnderScheme(t *testing.T) {
	if !slices.Contains(cutting_garden_plugins.RegisteredConfigSections(), schemeYNAB) {
		t.Errorf("init() did not register the %q config section", schemeYNAB)
	}
}

func TestConfigSection_DecodesAccountsAndInjects(t *testing.T) {
	setAccounts(t)
	model := sectionModel(t, `
[[accounts]]
name = "personal"
url = "ynab://personal/"
password_env = "YNAB_API_TOKEN"
`)
	if err := decodeConfigSection(model); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(configuredAccounts) != 1 {
		t.Fatalf("want 1 configured account, got %d", len(configuredAccounts))
	}
	a := configuredAccounts[0]
	if a.Name != "personal" || a.URL != "ynab://personal/" || a.PasswordEnv != "YNAB_API_TOKEN" {
		t.Errorf("decoded account wrong: %+v", a)
	}
	if unused := model.Undecoded(); len(unused) != 0 {
		t.Errorf("Undecoded() = %v, want none", unused)
	}
}

// rejectedBy decodes body as a [ynab] section and returns the accounts the
// decoder rejected, failing the test if the error is not a PartialConfigError.
func rejectedBy(t *testing.T, body string) []cutting_garden_plugins.RejectedAccount {
	t.Helper()
	err := decodeConfigSection(sectionModel(t, body))
	if err == nil {
		return nil
	}
	var partial *cutting_garden_plugins.PartialConfigError
	if !stderrors.As(err, &partial) {
		t.Fatalf("want a PartialConfigError, got %T: %v", err, err)
	}
	return partial.Rejected
}

func TestConfigSection_ValidateFailureSurfaces(t *testing.T) {
	setAccounts(t)
	rejected := rejectedBy(t, `
[[accounts]]
name = "x"
url = "ynab://y/"
`)
	if len(rejected) != 1 || rejected[0].Name != "x" ||
		!strings.Contains(rejected[0].Reason, "url host") {
		t.Fatalf("rejected = %+v, want account x refused for its url host", rejected)
	}
	if len(configuredAccounts) != 0 {
		t.Errorf("a rejected account must not be injected, got %+v", configuredAccounts)
	}
}

// One bad account must not drop the good ones (cutting-garden#406): the good
// account is injected and its root listed, the bad one is named.
func TestConfigSection_OneBadAccountDoesNotDropTheGoodOne(t *testing.T) {
	setAccounts(t)
	rejected := rejectedBy(t, `
[[accounts]]
name = "personal"
url = "ynab://personal/"

[[accounts]]
name = "x"
url = "ynab://y/"
`)
	if len(rejected) != 1 || rejected[0].Name != "x" {
		t.Fatalf("rejected = %+v, want only x", rejected)
	}
	if len(configuredAccounts) != 1 || configuredAccounts[0].Name != "personal" {
		t.Fatalf("configured = %+v, want only personal", configuredAccounts)
	}
	roots, err := (Plugin{}).Roots(context.Background())
	if err != nil || len(roots) != 1 || roots[0].String() != "ynab://personal/" {
		t.Errorf("roots = %v, %v; want the good account's root", roots, err)
	}
}

// A duplicate name rejects every account sharing it (neither is safe to pick),
// and an account with no name is reported by its position.
func TestConfigSection_DuplicateAndNamelessAccountsAreRejectedByName(t *testing.T) {
	setAccounts(t)
	rejected := rejectedBy(t, `
[[accounts]]
name = "dup"
url = "ynab://dup/"

[[accounts]]
name = "ok"
url = "ynab://ok/"

[[accounts]]
name = "dup"
url = "ynab://dup/"

[[accounts]]
url = "ynab://nameless/"
`)
	var got []string
	for _, r := range rejected {
		got = append(got, r.Name)
	}
	if want := []string{"dup", "dup", "accounts[3]"}; !slices.Equal(got, want) {
		t.Errorf("rejected names = %v, want %v", got, want)
	}
	if len(configuredAccounts) != 1 || configuredAccounts[0].Name != "ok" {
		t.Errorf("configured = %+v, want only ok", configuredAccounts)
	}
}

// Through the real registry: a later load whose [ynab] section is skipped
// leaves the plugin with no configured accounts, not the earlier load's
// (cutting-garden#412).
func TestConfigSection_SkippedLoadDropsAnEarlierLoadsAccounts(t *testing.T) {
	setAccounts(t)
	good := sectionModel(t, `
[ynab]
[[ynab.accounts]]
name = "personal"
url = "ynab://personal/"
`)
	if failures := cutting_garden_plugins.DecodeRegisteredConfigSectionsEach(good); len(failures) != 0 {
		t.Fatalf("valid section failed: %v", failures)
	}
	if len(configuredAccounts) != 1 {
		t.Fatalf("after a valid load: %d accounts, want 1", len(configuredAccounts))
	}

	bad := sectionModel(t, `
[ynab]
[[ynab.accounts]]
name = "x"
url = "ynab://y/"
`)
	failures := cutting_garden_plugins.DecodeRegisteredConfigSectionsEach(bad)
	if len(failures) != 1 || failures[0].Section != schemeYNAB {
		t.Fatalf("invalid section failures = %v, want the ynab section skipped", failures)
	}
	if len(configuredAccounts) != 0 {
		t.Errorf("a skipped section left the earlier load's accounts: %+v", configuredAccounts)
	}
}

func TestAPIBaseFor(t *testing.T) {
	if got := apiBaseFor(plainAccount("p", "ynab://p/")); got != defaultAPIBase {
		t.Errorf("apiBaseFor default = %q, want %q", got, defaultAPIBase)
	}
	override := withSessionURL(plainAccount("t", "ynab://t/"), "http://127.0.0.1:43114/v1")
	if got := apiBaseFor(override); got != "http://127.0.0.1:43114/v1" {
		t.Errorf("apiBaseFor override = %q", got)
	}
}

func TestRoots_CredentialFree(t *testing.T) {
	setAccounts(t, account("personal"), account("work"))
	roots, err := (Plugin{}).Roots(context.Background())
	if err != nil {
		t.Fatalf("Roots: %v", err)
	}
	if len(roots) != 2 {
		t.Fatalf("want 2 roots, got %d", len(roots))
	}
	for _, r := range roots {
		if r.Scheme != schemeYNAB {
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
	if err != nil || len(roots) != 0 {
		t.Errorf("Roots = %v, %v; want none", roots, err)
	}
}

func TestRootLabelsAndNames(t *testing.T) {
	setAccounts(t, account("personal"))
	want := accountRootURI("personal").String()
	labels, err := (Plugin{}).RootLabels(context.Background())
	if err != nil || labels[want] != "personal" {
		t.Errorf("RootLabels[%q] = %q, %v", want, labels[want], err)
	}
	names, err := (Plugin{}).RootNames(context.Background())
	if err != nil || names[want] != "personal" {
		t.Errorf("RootNames[%q] = %q, %v", want, names[want], err)
	}
}

func TestValidate_PluginRejectsUnknownAccount(t *testing.T) {
	setAccounts(t, account("personal"))
	if err := (Plugin{}).Validate(mustParseURL(t, "ynab://personal/B/"), ""); err != nil {
		t.Errorf("configured account rejected: %v", err)
	}
	err := (Plugin{}).Validate(mustParseURL(t, "ynab://nobody/B/"), "")
	if err == nil || !errors.Is400BadRequest(err) {
		t.Errorf("unknown account: want 400, got %v", err)
	}
}
