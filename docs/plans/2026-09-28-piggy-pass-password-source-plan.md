# piggy pass entries as config.toml passwords — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use eng:subagent-driven-development to implement this plan task-by-task.

**Goal:** Let a `config.toml` account (`config_common.Account`, shared by the
caldav/fastmail/jira plugins) resolve its password from a piggy pass store
entry, via new `password_source`/`password_key` fields, alongside the
existing `password_env`.

**Architecture:** `config_common.Account` gains `PasswordSource` /
`PasswordKey` fields (tommy-codegen'd, mirrored into the `pkgs/config_common`
dagnabit facade). `Account.Password()` changes from `() string` to
`() (string, error)`; a `"piggy"` source shells out to `piggy pass show
<key>` via the same `exec.LookPath` convention already used by
`plugins/optical`, `plugins/ytdlp`, `plugins/googlephotos`. A new
`Account.ValidatePassword()` enforces mutual exclusivity between the legacy
`password_env` and the new pair, called from each of the three plugins'
existing `AccountsConfig.Validate()`.

**Tech Stack:** Go, tommy (TOML codegen), dagnabit (facade codegen),
`os/exec`, dewey's `pkgs/errors`.

**Rollback:** N/A — purely additive (new optional fields; `password_env`
keeps working exactly as before; `Account.Password()`'s signature change is
internal to this repo, not a wire-format change).

Design doc: `docs/plans/2026-09-28-piggy-pass-password-source-design.md`
(read it first — this plan implements its v1 scope only. v2 — batched
`piggy pass show-batch` resolution and `interfaces.ActiveContext`
threading — is explicitly deferred and MUST NOT be implemented here.)

---

### Task 1: Extend the `Account` schema and regenerate codegen

**Promotion criteria:** N/A (additive schema change, no old approach to retire).

**Files:**
- Modify: `internal/config_common/config.go`
- Generated (do not hand-edit): `internal/config_common/config_tommy.go`,
  `pkgs/config_common/config.go`, `pkgs/config_common/config_tommy.go`
- Test: `internal/config_common/config_test.go` (new file)

**Step 1: Write the failing test**

Create `internal/config_common/config_test.go`:

```go
package config_common

import "testing"

func TestAccount_NewPasswordFieldsRoundTripThroughTOML(t *testing.T) {
	section := AccountsSection{
		Accounts: []Account{{
			Root:           Root{Name: "x", URL: "caldav://host/dav/"},
			PasswordSource: "piggy",
			PasswordKey:    "cutting-garden/caldav/x",
		}},
	}
	encoded, err := EncodeAccountsSection(&section)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var decoded AccountsSection
	if err := DecodeAccountsSection(&decoded, encoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := decoded.Accounts[0]
	if got.PasswordSource != "piggy" || got.PasswordKey != "cutting-garden/caldav/x" {
		t.Errorf("round-trip mismatch: got %+v", got)
	}
}
```

(If tommy's generated API names differ from `EncodeAccountsSection`/
`DecodeAccountsSection` — check the existing generated
`internal/config_common/config_tommy.go` for the actual exported names
before writing this test; adjust the test to match. The round-trip shape is
what matters, not the exact function name.)

**Step 2: Run test to verify it fails**

Run: `just debug-test-pkg internal/config_common TestAccount_NewPasswordFieldsRoundTripThroughTOML`

Expected: FAIL — `PasswordSource`/`PasswordKey` are not fields on `Account` yet (compile error).

**Step 3: Add the fields**

Edit `internal/config_common/config.go`, `Account`:

```go
// Account is a credentialed root (a Root plus credential indirection) for
// plugins whose roots require authentication (caldav, and the planned
// sftp/webdav/github). The password comes from exactly one of:
//   - PasswordEnv (legacy): the name of an environment variable.
//   - PasswordSource + PasswordKey: PasswordSource selects the backend
//     ("env" resolves PasswordKey as an environment variable name — the
//     new spelling of PasswordEnv; "piggy" resolves PasswordKey as a
//     `piggy pass show` entry name). ValidatePassword enforces mutual
//     exclusivity and the PasswordSource enum; see that method.
// The secret itself is never stored in the config file (RFC 0007 §
// Security Considerations).
//
// SessionURL overrides the endpoint a plugin with a FIXED API host would
// otherwise hard-code — today only the fastmail plugin's JMAP Session URL
// (default https://api.fastmail.com/jmap/session), which is how the bats
// lane points a `fastmail://<name>/` account at its in-memory test server.
// Plugins whose URL already names the server (caldav, jira) ignore it.
//
//go:generate tommy generate
type Account struct {
	Root
	Username       string `toml:"username,omitempty"`
	PasswordEnv    string `toml:"password_env,omitempty"`
	PasswordSource string `toml:"password_source,omitempty"`
	PasswordKey    string `toml:"password_key,omitempty"`
	SessionURL     string `toml:"session_url,omitempty"`
}
```

**Step 4: Regenerate tommy + dagnabit codegen**

Run: `just codemod-generate` (regenerates `internal/config_common/config_tommy.go`)
Run: `just codemod-generate-dagnabit` (regenerates the `pkgs/config_common` facade — the copy-mode mirror `plugins/caldav` etc. actually import, per RFC 0009 §5)

**Step 5: Run test to verify it passes**

Run: `just debug-test-pkg internal/config_common TestAccount_NewPasswordFieldsRoundTripThroughTOML`

Expected: PASS

**Step 6: Verify no codegen drift**

Run: `just validate-generate` and `just validate-generate-dagnabit`

Expected: both clean (no diff between committed generated files and a fresh regen).

**Step 7: Commit**

```bash
git add internal/config_common/ pkgs/config_common/
git commit -m "feat(config): add password_source/password_key fields to Account"
```

---

### Task 2: `Account.ValidatePassword()`

**Promotion criteria:** N/A.

**Files:**
- Modify: `internal/config_common/config.go`
- Test: `internal/config_common/config_test.go`

**Step 1: Write the failing tests**

Append to `internal/config_common/config_test.go`:

```go
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
```

**Step 2: Run tests to verify they fail**

Run: `just debug-test-pkg internal/config_common TestAccount_ValidatePassword`

Expected: FAIL — `ValidatePassword` method does not exist (compile error).

**Step 3: Implement**

Add to `internal/config_common/config.go` (needs `fmt` import — check whether
this repo's convention is stdlib `fmt.Errorf` or dewey's `pkgs/errors` for a
plain validation error with no request-level BadRequestf context; the
existing plugin `Validate()` methods wrap this package's errors with
`errors.BadRequestf` themselves, so a plain `fmt.Errorf` here — wrapped by
the caller — keeps the message composable):

```go
// ValidatePassword enforces mutual exclusivity between the legacy
// PasswordEnv and the PasswordSource/PasswordKey pair, and validates the
// PasswordSource enum. A plugin's own AccountsConfig.Validate calls this
// from its per-account loop and wraps the error with its own
// "<scheme>.accounts[%q]: %s" context (acct.Name is already known to be
// non-empty by the time that loop reaches this check).
func (a Account) ValidatePassword() error {
	if a.PasswordEnv != "" && (a.PasswordSource != "" || a.PasswordKey != "") {
		return fmt.Errorf(
			"password_env is mutually exclusive with password_source/password_key",
		)
	}
	if (a.PasswordSource == "") != (a.PasswordKey == "") {
		return fmt.Errorf(
			"password_source and password_key must be set together",
		)
	}
	switch a.PasswordSource {
	case "", "env", "piggy":
		// ok
	default:
		return fmt.Errorf(
			"unknown password_source %q (want \"env\" or \"piggy\")",
			a.PasswordSource,
		)
	}
	return nil
}
```

Add `"fmt"` to the import block in `internal/config_common/config.go`.

**Step 4: Run tests to verify they pass**

Run: `just debug-test-pkg internal/config_common TestAccount_ValidatePassword`

Expected: PASS (all 8 subtests)

**Step 5: Commit**

```bash
git add internal/config_common/config.go internal/config_common/config_test.go
git commit -m "feat(config): add Account.ValidatePassword"
```

---

### Task 3: `piggyPassShow` + `Account.Password()` signature change

**Promotion criteria:** N/A.

**Files:**
- Modify: `internal/config_common/config.go`
- Test: `internal/config_common/config_test.go`

**Step 1: Write the failing tests**

Append to `internal/config_common/config_test.go`:

```go
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
	script += "exit " + itoa(exitCode) + "\n"
	path := dir + "/piggy"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}
```

(`shellQuote`/`itoa` are small test-only helpers — use `strconv.Itoa` for
`itoa` and a minimal single-quote-wrapping escaper for `shellQuote`, e.g.
`"'" + strings.ReplaceAll(s, "'", `'\''`) + "'"`, imported via `strconv` and
`strings` in the test file. Keep them unexported and file-local.)

**Step 2: Run tests to verify they fail**

Run: `just debug-test-pkg internal/config_common TestAccount_Password`

Expected: FAIL — `Password()` still returns a single `string`, and
`PasswordSource`/piggy branch don't exist yet (compile error: too many
return values expected by the new tests).

**Step 3: Implement**

Replace the existing `Password()` method in `internal/config_common/config.go`:

```go
// Password resolves the account's password: PasswordEnv (legacy) or
// PasswordSource ("env": PasswordKey as an env var name; "piggy":
// PasswordKey as a `piggy pass show` entry name). Returns "", nil when no
// password field is set. A "piggy" source's missing binary or failed
// decrypt is a hard error — unlike an unset env var, which silently
// resolves to "" (ValidatePassword already guarantees PasswordSource is
// one of "", "env", "piggy" and that PasswordSource/PasswordKey are set
// together, so the switch below has no further default case to reach).
func (a Account) Password() (string, error) {
	switch {
	case a.PasswordEnv != "":
		return os.Getenv(a.PasswordEnv), nil
	case a.PasswordSource == "env":
		return os.Getenv(a.PasswordKey), nil
	case a.PasswordSource == "piggy":
		return piggyPassShow(a.PasswordKey)
	default:
		return "", nil
	}
}

// piggyStderrTailBytes caps how much of `piggy pass show`'s stderr is
// buffered for the failure diagnostic — same bound and rationale as
// plugins/optical and plugins/googlephotos' stderr tails.
const piggyStderrTailBytes = 4096

// piggyPassShow decrypts and returns the named piggy pass entry via
// `piggy pass show <key>`, trimming the trailing newline `pass show`
// writes. The binary is resolved through exec.LookPath, honoring the
// caller's PATH (the same convention as plugins/optical, plugins/ytdlp,
// plugins/googlephotos). No context.Context threading in v1 (see the
// design doc's "No context.Context threading in v1" section) — a v2
// revision threads interfaces.ActiveContext through for both a single
// `pass show` and a batched `pass show-batch` session.
func piggyPassShow(key string) (string, error) {
	binPath, err := exec.LookPath("piggy")
	if err != nil {
		return "", fmt.Errorf(
			"config_common: piggy not found on PATH (%w)\n"+
				"hint: enter the devshell or run a nix-built binary",
			err,
		)
	}

	cmd := exec.Command(binPath, "pass", "show", key)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if runErr := cmd.Run(); runErr != nil {
		tail := stderr.Bytes()
		if len(tail) > piggyStderrTailBytes {
			tail = tail[len(tail)-piggyStderrTailBytes:]
		}
		return "", fmt.Errorf(
			"config_common: piggy pass show %q failed (%w)\nstderr-tail: %s",
			key, runErr, tail,
		)
	}

	return strings.TrimRight(stdout.String(), "\n"), nil
}
```

Add `"bytes"`, `"os/exec"`, `"strings"` to the import block in
`internal/config_common/config.go` (`"fmt"` was already added in Task 2).

**Step 4: Run tests to verify they pass**

Run: `just debug-test-pkg internal/config_common TestAccount_Password`

Expected: PASS (all subtests, including the piggy stub cases)

**Step 5: Regenerate dagnabit facade**

`Password()`'s signature change is a source change dagnabit's copy-mode
mirrors — regenerate and verify no drift:

Run: `just codemod-generate-dagnabit`
Run: `just validate-generate-dagnabit`

**Step 6: Commit**

```bash
git add internal/config_common/ pkgs/config_common/
git commit -m "feat(config): Account.Password resolves piggy pass entries"
```

---

### Task 4: Update the 3 production call sites

**Promotion criteria:** N/A — this is a mechanical signature-propagation fix; there is no old code path left behind to retire once done.

**Files:**
- Modify: `plugins/caldav/url.go:149`
- Modify: `plugins/jira/url.go:118`
- Modify: `plugins/fastmail/config.go:171`

This task has no separate test-first step: the existing test suites for
each plugin already exercise `connectionFromArg`/`resolveClient`
end-to-end (e.g. `plugins/caldav/url_test.go`,
`plugins/caldav/config_test.go`, `plugins/jira/url_test.go`,
`plugins/jira/config_test.go`) — those are the regression tests for this
change, and Step 1 below is "make the build compile again," not "write a
new test."

**Step 1: Fix the call sites**

`plugins/caldav/url.go`, inside `connectionFromArg` (around line 144-149):

```go
	} else if acct, ok := matchAccount(parsed.Host, parsed.Path); ok {
		// Step 2: a configured account matching this node's host + longest
		// path prefix supplies the credentials.
		username = acct.Username
		password, err = acct.Password()
		if err != nil {
			return "", "", "", errors.Wrapf(err, "caldav plugin: account %q", acct.Name)
		}
	} else {
```

`plugins/jira/url.go`, inside `connectionFromArg` (around line 113-118):

```go
	} else if acct, ok := matchAccount(parsed.Host, strings.TrimLeft(parsed.Path, "/")); ok {
		// Step 2: a configured account matching this node's host + longest
		// project-path prefix supplies the credentials.
		username = acct.Username
		token, err = acct.Password()
		if err != nil {
			return "", "", "", errors.Wrapf(err, "jira plugin: account %q", acct.Name)
		}
	} else {
```

`plugins/fastmail/config.go`, `resolveClient` (around line 164-172):

```go
func resolveClient(ref nodeRef) (*client, error) {
	acct, ok := accountByName(ref.account)
	if !ok {
		return nil, errors.BadRequestf(
			"fastmail plugin: unknown account %q", ref.account,
		)
	}
	password, err := acct.Password()
	if err != nil {
		return nil, errors.Wrapf(err, "fastmail plugin: account %q", acct.Name)
	}
	return newClient(sessionURLFor(acct), password), nil
}
```

**Step 2: Confirm each package still builds**

Run: `just debug-test-pkg plugins/caldav TestConnectionFromArg_Precedence`
Run: `just debug-test-pkg plugins/jira TestConnectionFromArg_UserinfoWins`
Run: `just debug-test-pkg plugins/fastmail TestConfigSection_DecodesAccountsAndInjects`

Expected: all PASS (a compile error in any of the 3 modified files fails
every test in that package, so these existing tests are sufficient
regression coverage for the signature change).

**Step 3: Commit**

```bash
git add plugins/caldav/url.go plugins/jira/url.go plugins/fastmail/config.go
git commit -m "fix: propagate Account.Password()'s new error return at its 3 call sites"
```

---

### Task 5: Wire `ValidatePassword` into each plugin's `Validate()`

**Promotion criteria:** N/A.

**Files:**
- Modify: `plugins/caldav/config.go` (`AccountsConfig.Validate`)
- Modify: `plugins/fastmail/config.go` (`AccountsConfig.Validate`)
- Modify: `plugins/jira/config.go` (`AccountsConfig.Validate`)
- Test: `plugins/caldav/config_test.go`, `plugins/fastmail/config_section_test.go`, `plugins/jira/config_test.go`

**Step 1: Write the failing tests**

Append to `plugins/caldav/config_test.go`:

```go
func TestAccountsConfig_Validate_RejectsConflictingPasswordFields(t *testing.T) {
	c := AccountsConfig{Accounts: []config_common.Account{{
		Root:           config_common.Root{Name: "x", URL: "caldav://host/dav/"},
		PasswordEnv:    "FOO",
		PasswordSource: "env",
		PasswordKey:    "FOO",
	}}}
	if err := c.Validate(); err == nil {
		t.Error("want error: password_env conflicts with password_source/password_key")
	}
}
```

Append to `plugins/fastmail/config_section_test.go`:

```go
func TestConfigSection_RejectsConflictingPasswordFields(t *testing.T) {
	setAccounts(t)
	err := decodeConfigSection(sectionModel(t, `
[[accounts]]
name = "x"
url = "fastmail://x/"
password_env = "FOO"
password_source = "env"
password_key = "FOO"
`))
	if err == nil {
		t.Fatal("want error: password_env conflicts with password_source/password_key")
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("want EX_USAGE (400 bad request), got %v", err)
	}
}
```

Append to `plugins/jira/config_test.go` (check the file's existing helper
names — e.g. its own `acct(...)` constructor — before matching this shape;
follow whatever pattern `plugins/jira/config_test.go` already uses for
building a `[]config_common.Account` and calling `AccountsConfig.Validate`
directly, analogous to the caldav test above):

```go
func TestAccountsConfig_Validate_RejectsConflictingPasswordFields(t *testing.T) {
	c := AccountsConfig{Accounts: []config_common.Account{{
		Root:           config_common.Root{Name: "x", URL: "jira://host/PROJ"},
		PasswordEnv:    "FOO",
		PasswordSource: "env",
		PasswordKey:    "FOO",
	}}}
	if err := c.Validate(); err == nil {
		t.Error("want error: password_env conflicts with password_source/password_key")
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `just debug-test-pkg plugins/caldav TestAccountsConfig_Validate_RejectsConflictingPasswordFields`
Run: `just debug-test-pkg plugins/fastmail TestConfigSection_RejectsConflictingPasswordFields`
Run: `just debug-test-pkg plugins/jira TestAccountsConfig_Validate_RejectsConflictingPasswordFields`

Expected: all FAIL (no error returned yet — `Validate()` doesn't call `ValidatePassword` yet).

**Step 3: Wire the call**

In each plugin's `Validate()` loop, immediately after the existing
`seenHostPath[key] = struct{}{}` (caldav, jira) or the `SessionURL` check
(fastmail) — i.e. as the last check for that account before the loop
continues — add:

```go
		if err := acct.ValidatePassword(); err != nil {
			return errors.BadRequestf(
				"<scheme>.accounts[%q]: %s", acct.Name, err,
			)
		}
```

substituting the literal scheme string already used by that plugin's other
`errors.BadRequestf` calls in the same function (`"caldav"`, `"fastmail"`,
`"jira"` — match the existing prefix exactly, e.g. caldav's is
`"caldav.accounts[%q]: %s"`).

**Step 4: Run tests to verify they pass**

Run the same 3 commands as Step 2.

Expected: all PASS. Also re-run each plugin's full existing config test
file to confirm nothing else regressed:

Run: `just debug-test-pkg plugins/caldav TestAccountsConfig`
Run: `just debug-test-pkg plugins/fastmail TestConfigSection`
Run: `just debug-test-pkg plugins/jira TestAccountsConfig`

**Step 5: Commit**

```bash
git add plugins/caldav/config.go plugins/caldav/config_test.go \
        plugins/fastmail/config.go plugins/fastmail/config_section_test.go \
        plugins/jira/config.go plugins/jira/config_test.go
git commit -m "feat(config): wire Account.ValidatePassword into caldav/fastmail/jira Validate"
```

---

### Task 6: Docs

**Promotion criteria:** N/A.

**Files:**
- Modify: `docs/rfcs/0007-config-subsystem.md`
- Modify: `plugins/caldav/AGENTS.md`
- Modify: `plugins/jira/AGENTS.md`

**Step 1: RFC 0007 revision note**

In `docs/rfcs/0007-config-subsystem.md`'s frontmatter, add a third
`revised:` line (matching the existing two-entry pattern) dated today,
e.g.:

```
  2026-09-28 (§ Shared Base Types: add PasswordSource/PasswordKey as a
  second password indirection alongside PasswordEnv, resolving to a piggy
  pass store entry — cutting-garden#<issue-number-if-filed>)
```

**Step 2: Update § Shared Base Types**

In the `Account` struct shown in that section (around line 282-287), add
the two new fields to match `internal/config_common/config.go`, and add a
field-semantics bullet after the existing `PasswordEnv` bullet (around line
298-301):

```markdown
- `PasswordSource` — OPTIONAL. Selects the backend `PasswordKey` is
  resolved against: `"env"` (an environment variable name — the new
  spelling of `PasswordEnv`) or `"piggy"` (a `piggy pass show` entry name).
  MUST be set together with `PasswordKey`, and MUST NOT be set together
  with `PasswordEnv`.
- `PasswordKey` — OPTIONAL. The key to resolve within `PasswordSource`'s
  backend. MUST be set together with `PasswordSource`.
```

**Step 3: Update § Security Considerations**

Extend the "No plaintext secrets in the file" bullet (around line 491-493)
to cover the piggy indirection:

```markdown
- **No plaintext secrets in the file.** Passwords MUST be referenced
  indirectly — by environment-variable name (`PasswordEnv`, or
  `PasswordSource="env"` + `PasswordKey`) or by a piggy pass store entry
  name (`PasswordSource="piggy"` + `PasswordKey`) — never written into
  `config.toml` directly. Inline plaintext passwords MUST NOT be a
  supported field.
```

**Step 4: One-line AGENTS.md touches**

`plugins/caldav/AGENTS.md` (around line 26-27), change:

```
(`[[caldav.accounts]]`, injected via `SetConfiguredAccounts`; password
from the account's `password_env`), else the global `CALDAV_USERNAME` /
```

to:

```
(`[[caldav.accounts]]`, injected via `SetConfiguredAccounts`; password
from the account's `password_env` or `password_source`/`password_key` —
RFC 0007), else the global `CALDAV_USERNAME` /
```

`plugins/jira/AGENTS.md` (around line 82-83), change:

```
(`[[jira.accounts]]`, injected via `SetConfiguredAccounts`; the API token
comes from the account's `password_env`), else the global `JIRA_USERNAME` /
```

to:

```
(`[[jira.accounts]]`, injected via `SetConfiguredAccounts`; the API token
comes from the account's `password_env` or `password_source`/`password_key`
— RFC 0007), else the global `JIRA_USERNAME` /
```

**Step 5: Commit**

```bash
git add docs/rfcs/0007-config-subsystem.md plugins/caldav/AGENTS.md plugins/jira/AGENTS.md
git commit -m "docs: revise RFC 0007 for password_source/password_key; touch caldav/jira AGENTS.md"
```

---

### Task 7: Full verification pass

**Promotion criteria:** N/A.

**Files:** none (verification only).

**Step 1: Run the affected packages' full test files once more**

Run: `just debug-test-pkg internal/config_common TestAccount`
Run: `just debug-test-pkg plugins/caldav TestConnectionFromArg`
Run: `just debug-test-pkg plugins/caldav TestAccountsConfig`
Run: `just debug-test-pkg plugins/jira TestConnectionFromArg`
Run: `just debug-test-pkg plugins/jira TestAccountsConfig`
Run: `just debug-test-pkg plugins/fastmail TestConfigSection`

Expected: all PASS.

**Step 2: Confirm no codegen drift**

Run: `just validate-generate`
Run: `just validate-generate-dagnabit`

Expected: both clean.

**Step 3: Do NOT run the full `just`/`just test` suite here**

Per this repo's CLAUDE.md: `merge-this-session`'s pre-merge hook already
runs `just` as the CI lane. Running it redundantly here wastes cycles —
the per-package runs above are the appropriate pre-merge sanity check.

**Step 4: Hand off**

At this point all 6 implementation tasks are committed on the current
spinclass branch. The next step is `spinclass merge-this-session` (or its
async twin), which runs the full `just` gate as the real CI signal.
