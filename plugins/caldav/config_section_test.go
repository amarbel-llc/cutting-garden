package caldav

import (
	stderrors "errors"
	"slices"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
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

// rejectedBy decodes body as a [caldav] section and returns the accounts the
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

func rejectedNames(rejected []cutting_garden_plugins.RejectedAccount) []string {
	var names []string
	for _, r := range rejected {
		names = append(names, r.Name)
	}
	return names
}

func configuredNames() []string {
	var names []string
	for _, a := range configuredAccounts {
		names = append(names, a.Name)
	}
	return names
}

// A duplicate name rejects every account sharing it (cutting-garden#406):
// neither is safe to pick, so neither is injected.
func TestConfigSection_DuplicateNameRejectsEveryAccountSharingIt(t *testing.T) {
	setAccounts(t)
	rejected := rejectedBy(t, `
[[accounts]]
name = "dup"
url = "caldav://h/a/"

[[accounts]]
name = "dup"
url = "caldav://h/b/"
`)
	if got := rejectedNames(rejected); !slices.Equal(got, []string{"dup", "dup"}) {
		t.Errorf("rejected = %v, want both dup accounts", got)
	}
	if len(configuredAccounts) != 0 {
		t.Errorf("a rejected account must not be injected, got %+v", configuredAccounts)
	}
}

// One bad account must not drop the good one (cutting-garden#406).
func TestConfigSection_OneBadAccountDoesNotDropTheGoodOne(t *testing.T) {
	setAccounts(t)
	rejected := rejectedBy(t, `
[[accounts]]
name = "good"
url = "caldav://h/good/"

[[accounts]]
name = "bad"
url = "https://h/bad/"

[[accounts]]
url = "caldav://h/nameless/"
`)
	if got := rejectedNames(rejected); !slices.Equal(got, []string{"bad", "accounts[2]"}) {
		t.Errorf("rejected = %v, want bad and the nameless account by position", got)
	}
	if got := configuredNames(); !slices.Equal(got, []string{"good"}) {
		t.Errorf("configured = %v, want only good", got)
	}
}

// Two accounts covering one (host, path) are both rejected, since credential
// resolution could not choose between them; an account elsewhere stays.
func TestConfigSection_HostPathConflictRejectsEveryAccountInIt(t *testing.T) {
	setAccounts(t)
	rejected := rejectedBy(t, `
[[accounts]]
name = "one"
url = "caldav://h/shared/"

[[accounts]]
name = "two"
url = "caldav://h/shared/"

[[accounts]]
name = "other"
url = "caldav://h/other/"
`)
	if got := rejectedNames(rejected); !slices.Equal(got, []string{"one", "two"}) {
		t.Errorf("rejected = %v, want both accounts on the shared path", got)
	}
	if got := configuredNames(); !slices.Equal(got, []string{"other"}) {
		t.Errorf("configured = %v, want only other", got)
	}
}
