package fastmail

import (
	stderrors "errors"
	"slices"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
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

func TestConfigSection_SessionURLOverridesTheFixedEndpoint(t *testing.T) {
	setAccounts(t)
	model := sectionModel(t, `
[[accounts]]
name = "test"
url = "fastmail://test/"
session_url = "http://127.0.0.1:43113/jmap/session"
`)
	if err := decodeConfigSection(model); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if unused := model.Undecoded(); len(unused) != 0 {
		t.Errorf("Undecoded() = %v, want none", unused)
	}
	acct, ok := accountByName("test")
	if !ok {
		t.Fatal("account test not injected")
	}
	if got, want := sessionURLFor(acct), "http://127.0.0.1:43113/jmap/session"; got != want {
		t.Errorf("sessionURLFor = %q, want %q", got, want)
	}
}

func TestSessionURLFor_DefaultsToFastmail(t *testing.T) {
	if got := sessionURLFor(account("personal", "fastmail://personal/")); got != defaultSessionURL {
		t.Errorf("sessionURLFor = %q, want %q", got, defaultSessionURL)
	}
}

// rejectedBy decodes body as a [fastmail] section and returns the accounts the
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

func TestConfigSection_RejectsConflictingPasswordFields(t *testing.T) {
	setAccounts(t)
	rejected := rejectedBy(t, `
[[accounts]]
name = "x"
url = "fastmail://x/"
password_env = "FOO"
password_source = "env"
password_key = "FOO"
`)
	if len(rejected) != 1 || rejected[0].Name != "x" {
		t.Fatalf("rejected = %+v, want x refused: password_env conflicts with password_source/password_key", rejected)
	}
}

func TestConfigSection_ValidateFailureSurfaces(t *testing.T) {
	setAccounts(t)
	rejected := rejectedBy(t, `
[[accounts]]
name = "x"
url = "fastmail://y/"
`)
	if len(rejected) != 1 || rejected[0].Name != "x" || !strings.Contains(rejected[0].Reason, "url host") {
		t.Fatalf("rejected = %+v, want account x refused for its url host", rejected)
	}
	if len(configuredAccounts) != 0 {
		t.Errorf("a rejected account must not be injected, got %+v", configuredAccounts)
	}
}

// One bad account must not drop the good one, and a duplicate name rejects
// every account sharing it (cutting-garden#406).
func TestConfigSection_OneBadAccountDoesNotDropTheGoodOne(t *testing.T) {
	setAccounts(t)
	rejected := rejectedBy(t, `
[[accounts]]
name = "good"
url = "fastmail://good/"

[[accounts]]
name = "bad"
url = "fastmail://elsewhere/"

[[accounts]]
name = "dup"
url = "fastmail://dup/"

[[accounts]]
name = "dup"
url = "fastmail://dup/"

[[accounts]]
url = "fastmail://nameless/"
`)
	if got := rejectedNames(rejected); !slices.Equal(got, []string{"bad", "dup", "dup", "accounts[4]"}) {
		t.Errorf("rejected = %v, want bad, both dups and the nameless account by position", got)
	}
	if got := configuredNames(); !slices.Equal(got, []string{"good"}) {
		t.Errorf("configured = %v, want only good", got)
	}
}
