package caldav

import (
	"slices"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/tommy/pkg/cst"
)

// sectionModel decomposes the body of a `[caldav]` table — what the SDK's
// config-section dispatch hands the registered decoder (a
// `[[caldav.accounts]]` entry arrives as `[[accounts]]` inside it).
func sectionModel(t *testing.T, body string) *cst.Value {
	t.Helper()
	model, err := cst.DecomposeBytes([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return model
}

func TestConfigSection_RegisteredUnderScheme(t *testing.T) {
	if !slices.Contains(cutting_garden_plugins.RegisteredConfigSections(), schemeCalDAV) {
		t.Errorf("init() did not register the %q config section", schemeCalDAV)
	}
}

// The registered decoder decodes `[[caldav.accounts]]` entries into the
// plugin's configured accounts (the fixture that used to be seeded through
// the framework loader) and marks every key consumed.
func TestConfigSection_DecodesAccountsAndInjects(t *testing.T) {
	setAccounts(t)
	model := sectionModel(t, `
[[accounts]]
name = "personal"
url = "caldav://dav.host/dav/me/"
username = "me"
password_env = "CALDAV_PERSONAL_PASSWORD"
`)
	if err := decodeConfigSection(model); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(configuredAccounts) != 1 {
		t.Fatalf("want 1 configured account, got %d", len(configuredAccounts))
	}
	a := configuredAccounts[0]
	if a.Name != "personal" || a.URL != "caldav://dav.host/dav/me/" ||
		a.Username != "me" || a.PasswordEnv != "CALDAV_PERSONAL_PASSWORD" {
		t.Errorf("decoded account wrong: %+v", a)
	}
	if unused := model.Undecoded(); len(unused) != 0 {
		t.Errorf("Undecoded() = %v, want none", unused)
	}
}

// A Validate failure surfaces from the decoder as a bad request and injects
// nothing.
func TestConfigSection_ValidateFailureSurfaces(t *testing.T) {
	setAccounts(t)
	err := decodeConfigSection(sectionModel(t, `
[[accounts]]
name = "dup"
url = "caldav://h/a/"

[[accounts]]
name = "dup"
url = "caldav://h/b/"
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
