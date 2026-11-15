package org

import (
	stderrors "errors"
	"slices"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/config_common"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/tommy/pkg/cst"
)

func orgAccount(name, rawURL string) config_common.Account {
	return config_common.Account{Root: config_common.Root{Name: name, URL: rawURL}}
}

func rejectedOf(rejected []cutting_garden_plugins.RejectedAccount) []string {
	var names []string
	for _, r := range rejected {
		names = append(names, r.Name)
	}
	return names
}

// validateEach keeps the valid accounts and names each rejected one, in
// request order (cutting-garden#406): a bad URL, a bad timezone, a duplicate
// name (every account sharing it) and a nameless account (by position).
func TestValidateEach_KeepsTheValidAccountsAndNamesTheRest(t *testing.T) {
	good := orgAccount("good", "http://127.0.0.1:1/")
	badURL := orgAccount("badurl", "ftp://host/")
	badZone := orgAccount("badzone", "http://127.0.0.1:2/")
	badZone.Timezone = "Mars/Olympus_Mons"

	valid, rejected := validateEach([]config_common.Account{
		good, badURL, badZone,
		orgAccount("dup", "http://127.0.0.1:3/"), orgAccount("dup", "http://127.0.0.1:4/"),
		orgAccount("", "http://127.0.0.1:5/"),
	})

	if len(valid) != 1 || valid[0].Name != "good" {
		t.Errorf("valid = %v, want only good", valid)
	}
	want := []string{"badurl", "badzone", "dup", "dup", "accounts[5]"}
	if got := rejectedOf(rejected); !slices.Equal(got, want) {
		t.Errorf("rejected = %v, want %v", got, want)
	}
}

// Through the registered decoder: one bad account is a PartialConfigError and
// the good account is still injected, so its root is listed.
func TestConfigSection_OneBadAccountDoesNotDropTheGoodOne(t *testing.T) {
	t.Cleanup(func() { setConfiguredAccounts(nil) })
	model, err := cst.DecomposeBytes([]byte(`
[[accounts]]
name = "good"
url = "http://127.0.0.1:1/"

[[accounts]]
name = "bad"
url = "ftp://host/"
`))
	if err != nil {
		t.Fatal(err)
	}
	err = decodeConfigSection(model)
	var partial *cutting_garden_plugins.PartialConfigError
	if !stderrors.As(err, &partial) {
		t.Fatalf("want a PartialConfigError, got %T: %v", err, err)
	}
	if got := rejectedOf(partial.Rejected); !slices.Equal(got, []string{"bad"}) {
		t.Errorf("rejected = %v, want only bad", got)
	}
	if len(configuredAccounts) != 1 || configuredAccounts[0].Name != "good" {
		t.Errorf("configured = %v, want only good", configuredAccounts)
	}
}
