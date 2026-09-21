package jira

import (
	"slices"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/tommy/pkg/cst"
)

// sectionModel decomposes the body of a `[jira]` table — what the SDK's
// config-section dispatch hands the registered decoder (a
// `[[jira.accounts]]` entry arrives as `[[accounts]]` inside it).
func sectionModel(t *testing.T, body string) *cst.Value {
	t.Helper()
	model, err := cst.DecomposeBytes([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return model
}

func TestConfigSection_RegisteredUnderScheme(t *testing.T) {
	if !slices.Contains(cutting_garden_plugins.RegisteredConfigSections(), schemeJira) {
		t.Errorf("init() did not register the %q config section", schemeJira)
	}
}

func TestConfigSection_DecodesAccountsAndInjects(t *testing.T) {
	setAccounts(t)
	model := sectionModel(t, `
[[accounts]]
name = "acme"
url = "jira://acme.atlassian.net/PROJ"
username = "me@x.io"
password_env = "JIRA_ACME"
`)
	if err := decodeConfigSection(model); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(configuredAccounts) != 1 {
		t.Fatalf("want 1 configured account, got %d", len(configuredAccounts))
	}
	a := configuredAccounts[0]
	if a.Name != "acme" || a.URL != "jira://acme.atlassian.net/PROJ" ||
		a.Username != "me@x.io" || a.PasswordEnv != "JIRA_ACME" {
		t.Errorf("decoded account wrong: %+v", a)
	}
	if unused := model.Undecoded(); len(unused) != 0 {
		t.Errorf("Undecoded() = %v, want none", unused)
	}
}

func TestConfigSection_ValidateFailureSurfaces(t *testing.T) {
	setAccounts(t)
	err := decodeConfigSection(sectionModel(t, `
[[accounts]]
name = "dup"
url = "jira://h/X"

[[accounts]]
name = "dup"
url = "jira://h/Y"
`))
	if err == nil {
		t.Fatal("want error for duplicate account name")
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("want EX_USAGE (400 bad request), got %v", err)
	}
	if len(configuredAccounts) != 0 {
		t.Errorf("a rejected section must inject nothing, got %+v", configuredAccounts)
	}
}
