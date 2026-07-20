package command_components

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
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

func TestLoadConfig_MissingFileIsEmpty(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "nope.toml"), nil)
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(cfg.Caldav.Accounts) != 0 {
		t.Errorf("want empty config, got %+v", cfg.Caldav.Accounts)
	}
}

func TestLoadConfig_DecodesAccounts(t *testing.T) {
	path := writeTempConfig(t, `
[[caldav.accounts]]
name = "personal"
url = "caldav://dav.host/dav/me/"
username = "me"
password_env = "CALDAV_PERSONAL_PASSWORD"
`)
	cfg, err := LoadConfig(path, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Caldav.Accounts) != 1 {
		t.Fatalf("want 1 account, got %d", len(cfg.Caldav.Accounts))
	}
	a := cfg.Caldav.Accounts[0]
	if a.Name != "personal" || a.URL != "caldav://dav.host/dav/me/" ||
		a.Username != "me" || a.PasswordEnv != "CALDAV_PERSONAL_PASSWORD" {
		t.Errorf("decoded account wrong: %+v", a)
	}
}

func TestLoadConfig_UnknownKeyWarns(t *testing.T) {
	path := writeTempConfig(t, `
bogus_top_key = 1

[[caldav.accounts]]
name = "x"
url = "caldav://h/p/"
`)
	var warn bytes.Buffer
	if _, err := LoadConfig(path, &warn); err != nil {
		t.Fatalf("load: %v", err)
	}
	if !strings.Contains(warn.String(), "bogus_top_key") {
		t.Errorf("want warning naming the unknown key, got %q", warn.String())
	}
}

func TestLoadConfig_DuplicateNameIsBadRequest(t *testing.T) {
	path := writeTempConfig(t, `
[[caldav.accounts]]
name = "dup"
url = "caldav://h/a/"

[[caldav.accounts]]
name = "dup"
url = "caldav://h/b/"
`)
	_, err := LoadConfig(path, nil)
	if err == nil {
		t.Fatal("want error for duplicate account name")
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("want EX_USAGE (400 bad request), got %v", err)
	}
}
