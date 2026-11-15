package jira

import (
	stderrors "errors"
	"slices"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
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

// rejectedBy decodes body as a [jira] section and returns the accounts the
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
url = "jira://h/X"

[[accounts]]
name = "dup"
url = "jira://h/Y"
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
url = "jira://h/GOOD"

[[accounts]]
name = "bad"
url = "https://h/BAD"

[[accounts]]
url = "jira://h/NAMELESS"
`)
	if got := rejectedNames(rejected); !slices.Equal(got, []string{"bad", "accounts[2]"}) {
		t.Errorf("rejected = %v, want bad and the nameless account by position", got)
	}
	if got := configuredNames(); !slices.Equal(got, []string{"good"}) {
		t.Errorf("configured = %v, want only good", got)
	}
}

// Two accounts covering one (host, project) are both rejected, since
// credential resolution could not choose between them; an account on another
// project stays.
func TestConfigSection_HostPathConflictRejectsEveryAccountInIt(t *testing.T) {
	setAccounts(t)
	rejected := rejectedBy(t, `
[[accounts]]
name = "one"
url = "jira://h/SHARED"

[[accounts]]
name = "two"
url = "jira://h/SHARED"

[[accounts]]
name = "other"
url = "jira://h/OTHER"
`)
	if got := rejectedNames(rejected); !slices.Equal(got, []string{"one", "two"}) {
		t.Errorf("rejected = %v, want both accounts on the shared project", got)
	}
	if got := configuredNames(); !slices.Equal(got, []string{"other"}) {
		t.Errorf("configured = %v, want only other", got)
	}
}
