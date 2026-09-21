package fastmail

import (
	"slices"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/tommy/pkg/cst"
)

// sectionModel decomposes the body of a `[fastmail]` table — what the SDK's
// config-section dispatch hands the registered decoder (a
// `[[fastmail.accounts]]` entry arrives as `[[accounts]]` inside it).
func sectionModel(t *testing.T, body string) *cst.Value {
	t.Helper()
	model, err := cst.DecomposeBytes([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return model
}

func TestConfigSection_RegisteredUnderScheme(t *testing.T) {
	if !slices.Contains(cutting_garden_plugins.RegisteredConfigSections(), schemeFastmail) {
		t.Errorf("init() did not register the %q config section", schemeFastmail)
	}
}

func TestConfigSection_DecodesAccountsAndInjects(t *testing.T) {
	setAccounts(t)
	model := sectionModel(t, `
[[accounts]]
name = "personal"
url = "fastmail://personal/"
password_env = "FASTMAIL_API_TOKEN"
`)
	if err := decodeConfigSection(model); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(configuredAccounts) != 1 {
		t.Fatalf("want 1 configured account, got %d", len(configuredAccounts))
	}
	a := configuredAccounts[0]
	if a.Name != "personal" || a.URL != "fastmail://personal/" ||
		a.PasswordEnv != "FASTMAIL_API_TOKEN" {
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
name = "x"
url = "fastmail://y/"
`))
	if err == nil {
		t.Fatal("want error for url host != account name")
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("want EX_USAGE (400 bad request), got %v", err)
	}
	if len(configuredAccounts) != 0 {
		t.Errorf("a rejected section must inject nothing, got %+v", configuredAccounts)
	}
}
