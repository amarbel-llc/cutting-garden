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

// D3: a section decoder's failure (a plugin's Validate rejecting an entry)
// is EX_USAGE naming the file, the section, and the entry.
func TestLoadConfig_SectionDecoderErrorIsBadRequest(t *testing.T) {
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
