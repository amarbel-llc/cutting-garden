package list

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/command"
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/tommy/pkg/cst"
)

// countedSection is a config section registered by this test binary alone.
// Its decoder counts dispatches, so a test can assert how many times ONE
// command invocation decodes the config — the observable proxy for how many
// times a real plugin's section is decoded, validated and injected
// (invalidation-cone B1 review: loading injects, so a second
// LoadDefaultConfig is a second injection, not a cheap read).
const countedSectionName = "listcounttest"

var countedSectionDecodes int

func init() {
	cutting_garden_plugins.MustRegisterConfigSection(
		countedSectionName,
		func(sub *cst.Value) error {
			countedSectionDecodes++
			if v, ok := sub.Get("token"); ok {
				v.MarkConsumed()
			}
			return nil
		},
	)
}

// TestRun_DecodesConfigSectionsOncePerInvocation pins that `list <uri>`
// loads the config exactly once: Run's LoadAndInjectConfig threads the
// loaded value down to runList instead of runList re-reading the file for
// its `[tags]` override. Before the threading this decoded twice.
//
// It lives in a file that sorts BEFORE list_test.go on purpose, so it runs
// ahead of TestRun_WirePluginDirectURI: a Run-level invocation whose config
// declares no wire plugin must not stop a later invocation's stanzas from
// registering (cutting-garden#262).
func TestRun_DecodesConfigSectionsOncePerInvocation(t *testing.T) {
	xdg := t.TempDir()
	configDir := filepath.Join(xdg, "cutting-garden")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(configDir, "config.toml"),
		[]byte("["+countedSectionName+"]\ntoken = \"x\"\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdg)

	var buf bytes.Buffer
	u := command.MakeUtility("cg-test", nil)
	u.AddCmd("list", newWithOutput(&buf))

	// Only this test's config carries the section, so only this invocation
	// increments — but snapshot anyway, since the counter is process-global.
	before := countedSectionDecodes
	if code := u.Run([]string{"cg-test", "list", "listtest://h/dav/"}); code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, buf.String())
	}
	if got := countedSectionDecodes - before; got != 1 {
		t.Errorf("one `list <uri>` decoded the config %d times, want 1 "+
			"(each decode re-validates and re-injects every plugin's accounts)",
			got)
	}
}
