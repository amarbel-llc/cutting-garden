# Design: piggy pass entries as `config.toml` passwords

Date: 2026-09-28

## Problem

`config_common.Account` (RFC 0007) resolves an account's password only via
`PasswordEnv` → `os.Getenv`. Credentials should be resolvable from a piggy
pass store entry too, so a `caldav`/`fastmail`/`jira` account in
`config.toml` can point at `piggy pass show <name>` instead of requiring the
operator to pre-export an env var.

## Scope (v1)

All account-bearing plugins get this for free, since the field lives on the
shared `config_common.Account` / `AccountsSection` schema every such plugin
(`caldav`, `fastmail`, `jira`) decodes through (RFC 0007 § Plugin-Owned
Sections) — not a caldav-only feature.

## Schema

```go
// internal/config_common (mirrored in pkgs/config_common via dagnabit copy mode)
type Account struct {
    Root
    Username       string `toml:"username,omitempty"`
    PasswordEnv    string `toml:"password_env,omitempty"`    // legacy sugar for password_source="env"
    PasswordSource string `toml:"password_source,omitempty"` // "env" | "piggy"
    PasswordKey    string `toml:"password_key,omitempty"`
    SessionURL     string `toml:"session_url,omitempty"`
}
```

- `password_source="env"` + `password_key="CALDAV_PW"` is the new spelling of
  `password_env="CALDAV_PW"`. `password_env` remains supported indefinitely
  as sugar (not deprecated-and-removed — no evidence of real deployments to
  migrate, and it's a two-line resolution branch to keep).
- `password_source="piggy"` + `password_key="cutting-garden/caldav/personal"`
  resolves via `piggy pass show <password_key>`.

### Validation

New shared `Account.ValidatePassword() error`, called from each plugin's
existing per-account `Validate()` loop (caldav, fastmail, jira):

- `password_env` set together with either `password_source` or
  `password_key` → error (mutually exclusive).
- `password_source` XOR `password_key` set (one without the other) → error.
- `password_source` outside `{"env", "piggy"}` → error.

Same EX_USAGE-naming-the-entry posture RFC 0007 already specifies for
decode/Validate errors (file, section, offending entry).

## Resolution

`Account.Password()` signature changes from `func (a Account) Password() string`
to `func (a Account) Password() (string, error)` — a hard-error return
requires an error value. Blast radius is exactly 3 call sites, all already
inside error-returning functions: `plugins/caldav/url.go:149`
(`connectionFromArg`), `plugins/jira/url.go:118` (`connectionFromArg`),
`plugins/fastmail/config.go:171` (`resolveClient`).

```go
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
```

`piggyPassShow` follows this repo's established external-tool convention
exactly (`plugins/optical/exec.go`, `plugins/ytdlp/exec.go`,
`plugins/googlephotos/exec.go`): resolve via `exec.LookPath("piggy")`, run
`piggy pass show <key>`, trim the trailing newline from stdout, and on a
missing binary or non-zero exit return an error including the last stderr
tail.

A missing `piggy` binary or a failed decrypt is a **hard error** — no
silent empty-password fallback (unlike an unset `PasswordEnv`, which
silently resolves to `""` today). A wrong/empty piggy-sourced password
would otherwise fail auth downstream with a much less specific error.

### No `context.Context` threading in v1

None of the 3 call sites' call chains (`connectionFromArg` × 2,
`resolveClient`) carry a context today, and threading one through would
touch roughly 15 call sites across 3 plugins for a PIN-prompt-cancellation
benefit that's speculative until real usage shows it matters.

**Tuning lever:** if a hung PIN prompt on `piggy pass show` turns out to be
a real annoyance (un-interruptible short of killing the whole process),
that's the signal to thread cancellation through — in v2, via
`interfaces.ActiveContext` (see below), not a bare `context.Context`.

## Testing

No dependency on piggy's own `fibby` virtual-card test harness — that is
piggy's own integration-test infrastructure and not worth wiring into
cutting-garden's build for this feature.

- **Config-side** (`Validate`, mutual exclusivity, enum checks) — pure Go
  unit tests, no exec.
- **`piggyPassShow` plumbing** — a stub `piggy` shell script placed on
  `PATH` (asserts invocation args, returns canned stdout/stderr/exit code),
  the same idiom `plugins/optical/plugin_test.go` already uses for the
  "tool missing" case (`t.Setenv("PATH", t.TempDir())`).
- **Real end-to-end decrypt** against a live piggy pass store is explicitly
  **out of scope** for v1 — a documented limitation, not a silent gap.

## Docs

RFC 0007 (accepted) gets a **revision note** — its frontmatter already
carries two precedent revision entries (cutting-garden#120, #165) — adding
`password_source`/`password_key` to § Shared Base Types and extending
§ Security Considerations' "passwords MUST be referenced by
environment-variable name" language to cover the piggy indirection. This is
a revision to the existing RFC, not a new one.

## v2 (explicitly deferred, not this change)

1. **Batched resolution via `piggy pass show-batch`.** Collect every
   piggy-pass-backed account across all registered plugins after config
   load, resolve them all in one PIV/PIN session (NDJSON progress per
   RFC 0005 in the piggy repo, exit codes 0/1/2), cache the results, and
   have `Password()` read the cache. Removes the "one PIN prompt per
   account" cost of v1's lazy per-call exec when `piggy-agent` isn't
   running. Needs a new cross-plugin synchronization point after all
   config sections decode (today each plugin's section decoder injects
   independently, RFC 0007 § Package Layering) and NDJSON
   parsing/diagnostic-taxonomy handling.
2. **`interfaces.ActiveContext` threading.** Extend `Password()` to
   `Password(ctx interfaces.ActiveContext) (string, error)` and thread the
   request-scoped context already built by the CLI dispatch loop
   (`Request.Context`, cancelled via `errors.ContextCancelWithError` on
   SIGINT/SIGTERM/SIGHUP — `internal/command/utility.go`) down through
   `connectionFromArg`/`resolveClient` into the piggy exec, for both a
   single `pass show` and the batched `pass show-batch` session. This is
   the codebase's existing cancellation idiom — not a parallel bare
   `context.Context` path — so a hung PIN prompt becomes interruptible the
   same way every other long-running operation in this CLI already is.
