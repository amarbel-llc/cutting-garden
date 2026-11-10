package config_common

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
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

func accountWithSessionURL(raw string) Account {
	return Account{SessionURL: raw}
}

func TestAccount_ValidateSessionURL_Accepts(t *testing.T) {
	for _, raw := range []string{
		"", // unset: the plugin's fixed default applies
		"https://api.example/v1",
		"https://jmap.proxy.example:8443/session",
		"https://api.example", // https to a non-loopback host is the normal case
		"http://127.0.0.1:43113/jmap/session",
		"http://localhost:43113/v1",
		"http://LOCALHOST:43113/v1",
		"http://localhost.:43113/v1", // trailing root dot
		"http://[::1]:43113/v1",
	} {
		if err := accountWithSessionURL(raw).ValidateSessionURL(); err != nil {
			t.Errorf("ValidateSessionURL(%q) = %v, want ok", raw, err)
		}
	}
}

func TestAccount_ValidateSessionURL_Rejects(t *testing.T) {
	for _, raw := range []string{
		"/relative/path",
		"ftp://host/x",
		"http://",
		"http://:8080/x",  // a port but no hostname
		"https://:8443/x", // ditto, under https
		// Plaintext http to anything that is not loopback: the token would
		// travel unencrypted.
		"http://example.com/x",
		"http://192.0.2.7/x",
		"http://10.0.0.5/x",
		"http://0.0.0.0/x", // unspecified, not loopback
		// Bypass shapes that LOOK loopback.
		"http://127.0.0.1@evil.example/x", // 127.0.0.1 is userinfo; the host is evil.example
		"http://localhost.evil.example/x", // a subdomain of someone else's domain
		"http://127.0.0.1.evil.example/x", // ditto
		"http://localhost@evil.example/x", // userinfo again
		"http://127.1.2.3.example.test/x", // not an IP literal
	} {
		if err := accountWithSessionURL(raw).ValidateSessionURL(); err == nil {
			t.Errorf("ValidateSessionURL(%q) = nil, want an error", raw)
		}
	}
}

// A credential pasted into the value must never be echoed back in the error.
func TestAccount_ValidateSessionURL_ErrorOmitsUserinfo(t *testing.T) {
	for _, raw := range []string{
		"http://alice:s3cr3t@example.com/x", // plaintext to a non-loopback host
		"ftp://alice:s3cr3t@example.com/x",  // wrong scheme
	} {
		err := accountWithSessionURL(raw).ValidateSessionURL()
		if err == nil {
			t.Fatalf("ValidateSessionURL(%q) = nil, want an error", raw)
		}
		for _, leaked := range []string{"s3cr3t", "alice"} {
			if strings.Contains(err.Error(), leaked) {
				t.Errorf("error for %q leaks %q: %v", raw, leaked, err)
			}
		}
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

// A piggy password is decrypted once per process (cutting-garden#398 step
// 2): a long-lived `cg mcp` making hundreds of calls costs one card decrypt
// per entry, not one per call. The stub counts its own invocations.
func TestAccount_Password_Piggy_DecryptsOncePerProcess(t *testing.T) {
	t.Cleanup(forgetPiggyPasswords)
	dir := t.TempDir()
	count := dir + "/calls"
	script := "#!/bin/sh\necho call >> " + shellQuote(count) + "\nprintf 'memo-secret\\n'\n"
	if err := os.WriteFile(dir+"/piggy", []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	a := Account{PasswordSource: "piggy", PasswordKey: "cutting-garden/memo"}
	for i := 0; i < 3; i++ {
		got, err := a.Password()
		if err != nil || got != "memo-secret" {
			t.Fatalf("Password() #%d = %q, %v; want memo-secret", i+1, got, err)
		}
	}
	calls, err := os.ReadFile(count)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(calls), "call"); n != 1 {
		t.Errorf("piggy pass show ran %d times for three Password() calls, want 1", n)
	}
}

// A burst of concurrent first calls for one entry decrypts once: the key's
// lock is held across its decrypt.
func TestAccount_Password_Piggy_ConcurrentFirstCallsDecryptOnce(t *testing.T) {
	t.Cleanup(forgetPiggyPasswords)
	dir := t.TempDir()
	count := dir + "/calls"
	script := "#!/bin/sh\necho call >> " + shellQuote(count) + "\nsleep 0.2\nprintf 'burst-secret\\n'\n"
	if err := os.WriteFile(dir+"/piggy", []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	a := Account{PasswordSource: "piggy", PasswordKey: "cutting-garden/burst"}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, err := a.Password(); err != nil || got != "burst-secret" {
				t.Errorf("Password() = %q, %v", got, err)
			}
		}()
	}
	wg.Wait()
	calls, _ := os.ReadFile(count)
	if n := strings.Count(string(calls), "call"); n != 1 {
		t.Errorf("piggy pass show ran %d times for 8 concurrent first calls, want 1", n)
	}
}

// The remembered secret lives only in process memory: formatting the account
// does not show it, and another entry's failure does not mention it.
func TestAccount_Password_Piggy_MemoNeverPrinted(t *testing.T) {
	t.Cleanup(forgetPiggyPasswords)
	writeStubPiggy(t, 0, "memo-secret\n", "")
	a := Account{PasswordSource: "piggy", PasswordKey: "cutting-garden/memo"}
	if _, err := a.Password(); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		if out := fmt.Sprintf(verb, a); strings.Contains(out, "memo-secret") {
			t.Errorf("Sprintf(%s, account) = %q, contains the secret", verb, out)
		}
	}

	writeStubPiggy(t, 1, "", "decrypt failed\n")
	_, err := Account{PasswordSource: "piggy", PasswordKey: "other/entry"}.Password()
	if err == nil {
		t.Fatal("want an error for the failing entry")
	}
	if strings.Contains(err.Error(), "memo-secret") {
		t.Errorf("error %q contains another entry's remembered secret", err)
	}
}

// A failed decrypt is not remembered: the next call tries again.
func TestAccount_Password_Piggy_FailureIsNotRemembered(t *testing.T) {
	t.Cleanup(forgetPiggyPasswords)
	writeStubPiggy(t, 1, "", "card not present\n")
	a := Account{PasswordSource: "piggy", PasswordKey: "cutting-garden/retry"}
	if _, err := a.Password(); err == nil {
		t.Fatal("want an error from the failing stub")
	}
	writeStubPiggy(t, 0, "now-it-works\n", "")
	if got, err := a.Password(); err != nil || got != "now-it-works" {
		t.Errorf("Password() after a failure = %q, %v; want a fresh decrypt", got, err)
	}
}

// forgetPiggyPasswords drops every remembered piggy password, so each test
// starts with none.
func forgetPiggyPasswords() {
	piggyPasswordsMu.Lock()
	defer piggyPasswordsMu.Unlock()
	clear(piggyPasswords)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
