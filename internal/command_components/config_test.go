package command_components

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/tommy/pkg/cst"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The test's own registered config section — the framework loader's
// plugin-section contract is pinned against this fake rather than a real
// plugin, which this package no longer links (invalidation-cone B1). Its
// decoder records the `token_env` leaf it consumed and fails on a
// `reject = true` entry, standing in for a real plugin's Validate.
const (
	testSectionName   = "cctest_section"
	testSectionReject = "cctest_section: reject requested"
)

var testSectionTokenEnv string

// testOtherSection is a second fake section, so a test can check that one
// section's failure leaves the others loaded (cutting-garden#325).
const testOtherSection = "cctest_other"

var testOtherValue string

func init() {
	cutting_garden_plugins.MustRegisterConfigSection(testOtherSection,
		func(sub *cst.Value) error {
			if v, ok := sub.Get("value"); ok && v.Kind == cst.VLeaf {
				if s, sok := cst.ExtractString(v.Leaf); sok {
					v.MarkConsumed()
					testOtherValue = s
				}
			}
			return nil
		})
}

func init() {
	cutting_garden_plugins.MustRegisterConfigSection(testSectionName,
		func(sub *cst.Value) error {
			if v, ok := sub.Get("reject"); ok {
				v.MarkConsumed()
				return errors.BadRequestf("%s", testSectionReject)
			}
			v, ok := sub.Get("token_env")
			if !ok || v.Kind != cst.VLeaf {
				return nil
			}
			s, sok := cst.ExtractString(v.Leaf)
			if !sok {
				return nil
			}
			v.MarkConsumed()
			testSectionTokenEnv = s
			return nil
		})
}

func TestLoadConfig_MissingFileIsEmpty(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "nope.toml"), nil)
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if cfg == nil || len(cfg.Plugins) != 0 || len(cfg.TraversalPlugins) != 0 {
		t.Errorf("want empty config, got %+v", cfg)
	}
}

// A registered section is dispatched to its decoder over the same parsed
// model, so its consumed keys do not surface as unknown.
func TestLoadConfig_DispatchesRegisteredSection(t *testing.T) {
	testSectionTokenEnv = ""
	path := writeTempConfig(t, `
[cctest_section]
token_env = "CCTEST_TOKEN"
`)
	var warn bytes.Buffer
	if _, err := LoadConfig(path, &warn); err != nil {
		t.Fatalf("load: %v", err)
	}
	if testSectionTokenEnv != "CCTEST_TOKEN" {
		t.Errorf("section decoder not dispatched: token_env = %q", testSectionTokenEnv)
	}
	if warn.Len() != 0 {
		t.Errorf("a fully-consumed registered section must not warn, got %q", warn.String())
	}
}

func TestLoadConfig_UnknownKeyWarns(t *testing.T) {
	path := writeTempConfig(t, `
bogus_top_key = 1

[cctest_section]
token_env = "X"
`)
	var warn bytes.Buffer
	if _, err := LoadConfig(path, &warn); err != nil {
		t.Fatalf("load: %v", err)
	}
	if !strings.Contains(warn.String(), "bogus_top_key") {
		t.Errorf("want warning naming the unknown key, got %q", warn.String())
	}
	if strings.Contains(warn.String(), testSectionName) {
		t.Errorf("registered section wrongly reported unknown: %q", warn.String())
	}
}

// D2: a top-level table no linked plugin registered — e.g. a `[caldav]`
// section in a binary that never linked caldav — is an unknown key: a
// warning, not an error.
func TestLoadConfig_UnregisteredSectionWarnsOnly(t *testing.T) {
	path := writeTempConfig(t, `
[[nosuchplugin.accounts]]
name = "x"
url = "nosuchplugin://h/p/"
`)
	var warn bytes.Buffer
	cfg, err := LoadConfig(path, &warn)
	if err != nil {
		t.Fatalf("an unregistered section must not fail the load: %v", err)
	}
	if cfg == nil {
		t.Fatal("want a config")
	}
	if !strings.Contains(warn.String(), "nosuchplugin") {
		t.Errorf("want warning naming the unregistered section, got %q", warn.String())
	}
}

// A section a stanza claims via config_section is consumed raw by the wire
// plugin, so it is not an unknown key, whichever table declares the stanza:
// the generalized [[plugins]] or the [[traversal_plugins]] alias
// (cutting-garden#347, where a nebulous section warned). A capture-only
// [[plugins]] stanza's section counts as claimed too: the key is spoken for
// whichever protocol the stanza speaks.
func TestLoadConfig_StanzaClaimedSectionDoesNotWarn(t *testing.T) {
	for name, header := range map[string]string{
		"plugins":           "[[plugins]]",
		"traversal_plugins": "[[traversal_plugins]]",
		"plugins capture":   "[[plugins]]\nprotocols = [\"capture\"]",
	} {
		t.Run(name, func(t *testing.T) {
			path := writeTempConfig(t, `
`+header+`
name = "newsblur"
command = ["nebulous"]
schemes = ["newsblur"]
config_section = "nebulous"

[[nebulous.accounts]]
name = "x"
`)
			var warn bytes.Buffer
			if _, err := LoadConfig(path, &warn); err != nil {
				t.Fatalf("load: %v", err)
			}
			if warn.Len() != 0 {
				t.Errorf("a stanza-claimed section must not warn, got %q", warn.String())
			}
		})
	}
}

// A plugin section whose decoder fails is skipped with a warning, and the
// rest of the config loads (cutting-garden#325): one bad account must not
// take every plugin down. The warning names the file, the section and the
// decoder's error.
func TestLoadConfig_FailingSectionIsSkippedWithAWarning(t *testing.T) {
	testOtherValue = ""
	path := writeTempConfig(t, `
[cctest_section]
reject = true

[cctest_other]
value = "loaded"
`)
	var warn bytes.Buffer
	if _, err := LoadConfig(path, &warn); err != nil {
		t.Fatalf("a failing plugin section must not fail the load: %v", err)
	}
	if testOtherValue != "loaded" {
		t.Errorf("the other section was not decoded: value = %q", testOtherValue)
	}
	want := "warning: " + path + ": skipping [" + testSectionName + "]: " + testSectionReject
	if !strings.Contains(warn.String(), want) {
		t.Errorf("warning = %q, want it to contain %q", warn.String(), want)
	}
	if strings.Contains(warn.String(), "unknown config keys") {
		t.Errorf("a skipped section's keys must not also warn as unknown: %q", warn.String())
	}
	skipped := SkippedConfigSections()
	if len(skipped) != 1 || skipped[0].Section != testSectionName ||
		!strings.Contains(skipped[0].Error, testSectionReject) {
		t.Errorf("SkippedConfigSections = %+v, want the one rejected section", skipped)
	}
}

// The skipped sections describe the most recent load only: a later load of a
// file with no failing section, or of no file at all, reports none.
func TestLoadConfig_SkippedSectionsResetOnEveryLoad(t *testing.T) {
	failing := writeTempConfig(t, `
[cctest_section]
reject = true
`)
	for _, next := range []string{
		writeTempConfig(t, "[cctest_other]\nvalue = \"ok\"\n"),
		filepath.Join(t.TempDir(), "absent.toml"),
	} {
		if _, err := LoadConfig(failing, nil); err != nil {
			t.Fatalf("LoadConfig(failing): %v", err)
		}
		if len(SkippedConfigSections()) != 1 {
			t.Fatalf("after the failing load: %+v, want one skipped", SkippedConfigSections())
		}
		if _, err := LoadConfig(next, nil); err != nil {
			t.Fatalf("LoadConfig(%s): %v", next, err)
		}
		if got := SkippedConfigSections(); len(got) != 0 {
			t.Errorf("after loading %s: %+v, want none (stale from the earlier load)", next, got)
		}
	}
}

// D3, under CG_STRICT_CONFIG=1 (the CI switch, cutting-garden#325): a
// section decoder's failure (a plugin's Validate rejecting an entry) is
// EX_USAGE naming the file, the section, and the entry.
func TestLoadConfig_SectionDecoderErrorIsBadRequest(t *testing.T) {
	t.Setenv("CG_STRICT_CONFIG", "1")
	path := writeTempConfig(t, `
[cctest_section]
reject = true
`)
	_, err := LoadConfig(path, nil)
	if err == nil {
		t.Fatal("want error from the section decoder")
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("want EX_USAGE (400 bad request), got %v", err)
	}
	want := path + ": " + testSectionName + ": " + testSectionReject
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}
