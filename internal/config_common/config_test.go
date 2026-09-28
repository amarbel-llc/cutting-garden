package config_common

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestAccount_NewPasswordFieldsRoundTripThroughTOML(t *testing.T) {
	section := AccountsSection{
		Accounts: []Account{{
			Root:           Root{Name: "x", URL: "caldav://host/dav/"},
			PasswordSource: "piggy",
			PasswordKey:    "cutting-garden/caldav/x",
		}},
	}

	doc, err := DecodeAccountsSection([]byte(""))
	if err != nil {
		t.Fatalf("DecodeAccountsSection (empty): %v", err)
	}
	*doc.Data() = section
	encoded, err := doc.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, err := DecodeAccountsSection(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := decoded.Data().Accounts[0]
	if got.PasswordSource != "piggy" || got.PasswordKey != "cutting-garden/caldav/x" {
		t.Errorf("round-trip mismatch: got %+v", got)
	}
}

func TestAccount_ValidatePassword_PasswordEnvAlone_OK(t *testing.T) {
	a := Account{PasswordEnv: "FOO"}
	if err := a.ValidatePassword(); err != nil {
		t.Errorf("password_env alone should validate, got %v", err)
	}
}

func TestAccount_ValidatePassword_SourceAndKey_OK(t *testing.T) {
	a := Account{PasswordSource: "piggy", PasswordKey: "path/to/entry"}
	if err := a.ValidatePassword(); err != nil {
		t.Errorf("password_source+password_key should validate, got %v", err)
	}
}

func TestAccount_ValidatePassword_Empty_OK(t *testing.T) {
	if err := (Account{}).ValidatePassword(); err != nil {
		t.Errorf("no password fields should validate (unauthenticated), got %v", err)
	}
}

func TestAccount_ValidatePassword_EnvAndSourceConflict(t *testing.T) {
	a := Account{PasswordEnv: "FOO", PasswordSource: "env", PasswordKey: "FOO"}
	if err := a.ValidatePassword(); err == nil {
		t.Error("want error: password_env set together with password_source/password_key")
	}
}

func TestAccount_ValidatePassword_EnvAndKeyOnlyConflict(t *testing.T) {
	a := Account{PasswordEnv: "FOO", PasswordKey: "FOO"}
	if err := a.ValidatePassword(); err == nil {
		t.Error("want error: password_env set together with password_key")
	}
}

func TestAccount_ValidatePassword_SourceWithoutKey(t *testing.T) {
	a := Account{PasswordSource: "piggy"}
	if err := a.ValidatePassword(); err == nil {
		t.Error("want error: password_source set without password_key")
	}
}

func TestAccount_ValidatePassword_KeyWithoutSource(t *testing.T) {
	a := Account{PasswordKey: "x"}
	if err := a.ValidatePassword(); err == nil {
		t.Error("want error: password_key set without password_source")
	}
}

func TestAccount_ValidatePassword_UnknownSource(t *testing.T) {
	a := Account{PasswordSource: "keychain", PasswordKey: "x"}
	if err := a.ValidatePassword(); err == nil {
		t.Error("want error: unknown password_source")
	}
}

func TestAccount_Password_Empty(t *testing.T) {
	got, err := (Account{}).Password()
	if err != nil || got != "" {
		t.Errorf("Password() = %q, %v; want \"\", nil", got, err)
	}
}

func TestAccount_Password_LegacyEnv(t *testing.T) {
	t.Setenv("CG_TEST_PW", "secret1")
	got, err := (Account{PasswordEnv: "CG_TEST_PW"}).Password()
	if err != nil || got != "secret1" {
		t.Errorf("Password() = %q, %v; want \"secret1\", nil", got, err)
	}
}

func TestAccount_Password_SourceEnv(t *testing.T) {
	t.Setenv("CG_TEST_PW2", "secret2")
	a := Account{PasswordSource: "env", PasswordKey: "CG_TEST_PW2"}
	got, err := a.Password()
	if err != nil || got != "secret2" {
		t.Errorf("Password() = %q, %v; want \"secret2\", nil", got, err)
	}
}

func TestAccount_Password_Piggy_Success(t *testing.T) {
	writeStubPiggy(t, 0, "piggy-secret\n", "")
	a := Account{PasswordSource: "piggy", PasswordKey: "cutting-garden/x"}
	got, err := a.Password()
	if err != nil {
		t.Fatalf("Password: %v", err)
	}
	if got != "piggy-secret" {
		t.Errorf("Password() = %q, want %q (trailing newline trimmed)", got, "piggy-secret")
	}
}

func TestAccount_Password_Piggy_MissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	a := Account{PasswordSource: "piggy", PasswordKey: "x"}
	if _, err := a.Password(); err == nil {
		t.Error("want error when piggy is not on PATH")
	}
}

func TestAccount_Password_Piggy_DecryptFailure(t *testing.T) {
	writeStubPiggy(t, 1, "", "decrypt failed: no such entry\n")
	a := Account{PasswordSource: "piggy", PasswordKey: "missing/entry"}
	if _, err := a.Password(); err == nil {
		t.Error("want error on non-zero piggy exit")
	}
}

// writeStubPiggy puts an executable `piggy` on PATH (a temp dir prepended
// to $PATH for the duration of the test) that ignores its arguments and
// prints stdout/writes stderr/exits with the given code — enough to test
// piggyPassShow's plumbing without a real PIV card or piggy pass store.
func writeStubPiggy(t *testing.T, exitCode int, stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n"
	if stdout != "" {
		script += "printf '%s' " + shellQuote(stdout) + "\n"
	}
	if stderr != "" {
		script += "printf '%s' " + shellQuote(stderr) + " >&2\n"
	}
	script += "exit " + strconv.Itoa(exitCode) + "\n"
	path := dir + "/piggy"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
