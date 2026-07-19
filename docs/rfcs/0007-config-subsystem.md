---
status: accepted
date: 2026-06-09
revised: 2026-07-19 (§ The Root-Provider Capability: root aggregation is a
  per-plugin fault-isolation boundary, not fail-fast — cutting-garden#165)
---

# Configuration Subsystem and Root Enumeration (cutting-garden config.toml)

## Abstract

Cutting-garden has no user configuration file, and a traversable endpoint
must be named explicitly on the command line. This document specifies two
linked things: a `RootProvider` capability by which a plugin enumerates its
own top-level roots with no argument — so a no-argument `mcp`/`list` surfaces
*every* plugin's roots — and an XDG TOML configuration file that is one source
of those roots (per-plugin **preferred roots** and credentialed **accounts**,
with a host-keyed credential precedence). A plugin's roots MAY instead be
**intrinsic** (the file plugin's working directory), requiring no config. The
config file realizes, along the credential/roots axis, the configuration layer
that host-bound plugin resolution (RFC 0006) deferred; routing-precedence
overrides remain deferred.

## Introduction

Two facts motivate this specification:

1. **The MCP resource server (FDR 0015) and the `list`/`health` commands must
   surface a plugin's roots without being handed a URI.** Today
   `cutting-garden mcp caldav://host/dav/me/` takes roots from argv. The
   intended behavior is that `cutting-garden mcp` (no argument) surfaces every
   plugin's roots as MCP resources: the file plugin from its working
   directory, the caldav plugin from its configured accounts, a future web
   plugin from configured preferred roots. A plugin that can enumerate no
   roots is simply absent. This needs (a) a capability for "list my top-level
   roots," and (b) for plugins whose roots are not intrinsic, a place to
   record them.

2. **RFC 0006 (host-bound plugin resolution) explicitly deferred the
   configuration layer.** Its §Effective Bindings states a future revision MAY
   introduce a configuration layer, and its tracking discussion
   (cutting-garden#63) records: *"the equal-specificity init panic is the
   designed tripwire — the first legitimate tie is the signal to build the
   config layer, not before (cutting-garden has no config subsystem today)."*
   This RFC builds that subsystem along the **credential and roots** axis; the
   **binding-precedence override** axis RFC 0006 deferred (which plugin wins a
   contested `(scheme, host)`) remains deferred (§ Relationship to RFC 0006).

The credential model follows established prior art. madder resolves SFTP
connection parameters by hostname through `ssh -G <host>` and applies a fixed
precedence — explicit URI fields override the host-resolved config, which
overrides defaults (`MakeSSHClientFromSSHConfig`). This document generalizes
that precedence.

### Scope

This document specifies:

- The `RootProvider` capability interface and how the traversal commands
  aggregate over it.
- The three sources a plugin's roots may come from (intrinsic, configured
  preferred roots, configured accounts).
- The config file's location, encoding, and top-level structure.
- Shared base types (`Root`, `Account`) and their field semantics.
- The plugin-owned, delegated per-plugin config section shape, with caldav as
  the reference implementer.
- The host-keyed credential-resolution precedence a credentialed plugin MUST
  follow.
- Loading, validation, unknown-key behavior, and the package layering that
  keeps the aggregator and plugins free of an import cycle.

### Out of Scope

- **Binding-precedence overrides.** RFC 0006's deferred "reorder/override
  effective bindings" layer is not specified here (§ Relationship to RFC 0006).
- **Capture/restore/diff consuming the config.** This revision wires only the
  read-only traversal commands (`mcp`, `list`, `health`). `capture`,
  `restore`, and `diff` continue to take explicit URIs.
- **Secret storage.** Passwords are referenced indirectly by environment
  variable name; no keyring or encrypted-at-rest format is specified
  (§ Security Considerations).
- **The set of plugins that implement `RootProvider` now.** This RFC defines
  the capability and its sources; which plugins implement it in the first
  implementation is an implementation-planning decision, not a conformance
  requirement of this spec. caldav (configured accounts) is the reference
  implementer.

## Requirements Language

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD",
"SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be
interpreted as described in RFC 2119.

## Specification

### The Root-Provider Capability

Root enumeration is exposed through an OPTIONAL capability interface in
`cutting_garden_plugins`, probed by type assertion exactly as `RootLister`
is:

```go
// RootProvider is the OPTIONAL capability of a RootLister that can
// enumerate its own top-level roots with no input node — the entry
// points a no-argument `mcp`/`list` surfaces. The source of the roots
// is plugin-defined (§ Root Sources): intrinsic state, configured
// preferred roots, or configured accounts.
type RootProvider interface {
    RootLister
    // Roots returns the plugin's top-level roots. It returns an empty
    // slice (not an error) when the plugin can enumerate none.
    Roots(ctx context.Context) ([]*url.URL, error)
}
```

- `RootProvider` extends `RootLister` (FDR 0014): a plugin that provides
  entry-point roots MUST also be able to descend them via `ListRoots`.
- A plugin that does not implement `RootProvider`, or whose `Roots` returns an
  empty slice, contributes no roots and MUST NOT appear in aggregated output.
- The traversal commands (`mcp`, `list` with no URI argument, and `health`)
  MUST aggregate roots by iterating
  `cutting_garden_plugins.RegisteredPlugins()`, type-asserting `RootProvider`,
  and concatenating each plugin's `Roots(ctx)`.
- A non-nil error from one plugin's `Roots` MUST NOT abort the whole
  aggregation: it is contained to that plugin (a warning naming it is logged
  and its contribution is simply omitted), and every OTHER plugin's roots
  MUST still be returned. This is a per-plugin fault-isolation boundary, not
  a fail-fast one (cutting-garden#165): a single misconfigured or crashed
  wire plugin (RFC 0013) previously took the whole aggregation down —
  including every healthy plugin — which on `mcp` meant it failed
  cutting-garden's own MCP `initialize` handshake with its host. This
  matches the MCP server's root resolution (FDR 0015).
- Every `*url.URL` returned by `Roots` MUST be credential-free (no userinfo);
  the same holds for every child URI a plugin's traversal emits
  (§ Security Considerations).

### Root Sources

A `RootProvider`'s roots come from one or more of three sources; the source is
the plugin's choice and is invisible to the aggregating command:

1. **Intrinsic.** The plugin derives roots from ambient state with no config.
   The file plugin's `Roots` returns its working directory, and MAY in a
   future revision return additional intrinsic roots (e.g. `/`). Intrinsic
   plugins surface roots even with no `config.toml`.
2. **Configured preferred roots.** A plugin that cannot enumerate roots from
   ambient state (a web/yt-dlp plugin cannot list "all of the web") reads a
   list of preferred root URLs from its config section (§ Shared Base Types,
   `Root`). With no config, such a plugin contributes nothing.
3. **Configured accounts.** A plugin whose roots require credentials
   (caldav, and the planned sftp/webdav/github) reads credentialed accounts
   from its config section (§ Shared Base Types, `Account`), and resolves
   credentials per § Credential Resolution.

### File Location and Encoding

1. The configuration file MUST be read from
   `$XDG_CONFIG_HOME/cutting-garden/config.toml`, resolved through the same
   cutting-garden-scoped XDG environment the binary already uses for state
   (`captures.log`). When `$XDG_CONFIG_HOME` is unset, the XDG default
   (`$HOME/.config`) applies.
2. The file MUST be TOML.
3. A missing file MUST be treated as an empty configuration and MUST NOT be an
   error. cutting-garden MUST run normally with no config file — intrinsic
   roots (source 1) still surface.
4. The on-disk format is produced and consumed by tommy codegen
   (`//go:generate tommy generate`); the Go struct definitions below are the
   normative schema and the generated `Decode*`/`Encode` methods are the
   reference (de)serializer.

### Top-Level Structure

The top-level config is a horizontally-versioned struct (`ConfigV0`)
aggregating per-plugin sections as **delegated** fields owned by the plugin
packages:

```go
// internal/cgconfig
//go:generate tommy generate
type ConfigV0 struct {
    Caldav caldav.AccountsConfig `toml:"caldav,omitempty"`
    // Future plugins add one delegated field each, e.g.:
    // Sftp   sftp.AccountsConfig   `toml:"sftp,omitempty"`   // accounts (creds)
    // Webdav webdav.AccountsConfig `toml:"webdav,omitempty"` // accounts (creds)
    // Ytdlp  ytdlp.RootsConfig     `toml:"ytdlp,omitempty"`  // preferred roots (no creds)
}
```

- A new format version MUST add a new struct (`ConfigV1`, …) beside `ConfigV0`
  rather than mutating it, consistent with the repository's hyphenated,
  horizontal versioning (cutting-garden#79).
- Each plugin section is keyed by the plugin's primary scheme and is OPTIONAL.
- The file plugin (intrinsic roots) has **no** section in this revision.

### Shared Base Types

Plugins share two base types, defined in a neutral `config_common` package
that both `cgconfig` and plugin packages MAY import (the import-cycle break of
§ Package Layering):

```go
// internal/config_common
//go:generate tommy generate

// Root is a plugin entry point with no credentials — a "preferred
// root" for plugins that cannot self-enumerate (source 2).
type Root struct {
    Name string `toml:"name"`
    URL  string `toml:"url"`
}

// Account is a credentialed root (source 3): a Root plus the
// credential indirection.
type Account struct {
    Root
    Username    string `toml:"username,omitempty"`
    PasswordEnv string `toml:"password_env,omitempty"`
}
```

Field semantics:

- `Name` — REQUIRED. A non-empty label, unique within its plugin section. It
  MAY be surfaced as an MCP resource display name.
- `URL` — REQUIRED. The endpoint URI in the **same form the plugin accepts on
  the command line** (e.g. `caldav://dav.host/dav/me/`). It MUST parse as a URI
  whose scheme the plugin claims. It is the entry-point root.
- `Username` — OPTIONAL. The account username (§ Credential Resolution).
- `PasswordEnv` — OPTIONAL. The **name of an environment variable** whose value
  is the account password. The password value MUST NOT appear in the config
  file directly (§ Security Considerations). An unset named variable resolves
  to an empty password.

A plugin whose credential model needs more than `Account` provides MUST embed
`Account` (or `Root`) and add fields, rather than widening the shared base.
(Example: a future `github` plugin embedding `Root` and adding `TokenEnv`.)

### Plugin-Owned Sections

Each account- or root-bearing plugin owns its section struct (delegated into
`ConfigV0`). caldav is the reference implementer; its roots are credentialed
accounts:

```go
// internal/cutting_garden_plugin_caldav
//go:generate tommy generate
type AccountsConfig struct {
    Accounts []config_common.Account `toml:"accounts"`
}
func (AccountsConfig) Validate() error
```

producing the array-of-tables form:

```toml
[[caldav.accounts]]
name = "personal"
url  = "caldav://dav.host/dav/me/"
username = "me"
password_env = "CALDAV_PERSONAL_PASSWORD"

[[caldav.accounts]]
name = "team"
url  = "caldav://dav.host/dav/team/"
username = "me"
password_env = "CALDAV_TEAM_PASSWORD"
```

A preferred-roots plugin (illustrative, future) instead uses the
credential-free `Root`:

```go
// a future web/ytdlp plugin
type RootsConfig struct {
    Roots []config_common.Root `toml:"roots"`
}
```

```toml
[[ytdlp.roots]]
name = "my-subscriptions"
url  = "ytdlp:https://www.youtube.com/feed/subscriptions"
```

A section's `Validate` MUST reject: a duplicate `name`; an empty `name` or
`url`; a `url` that does not parse or whose scheme the plugin does not claim.
A validation error MUST abort config load (§ Loading and Validation).

### Credential Resolution

A plugin resolving credentials for a node URI `N` MUST apply the following
precedence, highest first (mirroring madder's URI-explicit-over-resolved SSH
precedence):

1. **Explicit URI userinfo.** If `N` carries userinfo
   (`scheme://user:pass@…`), that username and password MUST be used; steps
   2–3 MUST NOT be consulted.
2. **Matched account.** Else, the plugin MUST select the configured account
   whose `URL` has the same host as `N` (compared case-insensitively, byte-wise,
   no DNS) and whose `URL` path is a prefix of `N`'s path. When several
   accounts match, the account with the **longest matching path prefix** MUST
   win. Its `Username` is used and its password is `os.Getenv(PasswordEnv)`.
3. **Global environment fallback.** Else, the plugin MUST fall back to its
   existing global environment credentials (for caldav,
   `CALDAV_USERNAME` / `CALDAV_PASSWORD`), preserving today's behavior when no
   account matches.

A section's `Validate` MUST guarantee step 2 is unambiguous: two accounts MUST
NOT share both host and an identical path prefix.

Resolution MUST run on **every** traversal call, not only at the top-level
root: descent re-resolves the plugin from a child URI that carries no userinfo
(child URIs MUST be credential-free — § Security Considerations).

This section applies to credentialed plugins (source 3). Intrinsic and
preferred-root plugins (sources 1, 2) that need no credentials are unaffected.

### Loading and Validation

1. The loader MUST decode the file with the generated `DecodeConfigV0`,
   invoking each delegated section's `Validate` (the generated decoder calls
   `Validate` automatically when present).
2. The loader SHOULD report keys present in the file but consumed by no field
   (via the generated `Undecoded()`) as a stderr warning, so a misspelled key
   is visible. Unknown keys MUST NOT abort loading.
3. A decode or `Validate` error MUST abort the command with a usage-class
   error (EX_USAGE), naming the file and the offending entry.

### Package Layering

To keep the aggregator and plugins free of an import cycle:

- `config_common` (`Root`, `Account`) imports neither `cgconfig` nor any
  plugin; it is a leaf.
- Each plugin package owns its section struct, MAY import `config_common`, and
  MUST NOT import `cgconfig`.
- `cgconfig` imports the plugin packages to embed their sections as delegated
  fields; this is the only direction.
- The composition root (the binary's command builder) imports `cgconfig` and
  the plugin packages, loads the config once, and injects each plugin's
  section into that plugin **before** any command resolves roots. The
  injection mechanism is implementation-defined but MUST be a process-global
  set-once, consistent with the init-time plugin registry; `Plugin` values
  remain zero-size (a plugin reads injected configuration; it does not carry it
  as instance state).

## Security Considerations

- **No plaintext secrets in the file.** Passwords MUST be referenced by
  environment-variable name (`PasswordEnv`), never written into `config.toml`.
  Inline plaintext passwords MUST NOT be a supported field.
- **Credentials MUST NOT leak into surfaced URIs.** The `*url.URL` values a
  plugin returns from `Roots`, and every child node URI its traversal emits,
  MUST be credential-free. MCP resource URIs are visible to the connected
  client; a credentialed URI would disclose the password. Explicit userinfo in
  an input URI (resolution step 1) is the user's own choice and is stripped
  before any URI is surfaced.
- **Host matching is textual.** Account matching (resolution step 2) operates
  on the parsed URL host, lowercased, byte-wise — no DNS, IDN/punycode, or IP
  canonicalization, identical to RFC 0006 and for the same reason: matching is
  routing, not authorization.
- **Credential resolution is not authorization.** Selecting an account chooses
  which credentials a plugin presents; it grants no access the server would
  not already grant them.
- **Intrinsic roots and ambient scope.** The file plugin's intrinsic root is
  the process working directory; a future `/` root would expose the whole
  local filesystem to a connected MCP client. Broadening intrinsic scope is a
  deliberate, reviewable change, and any non-PWD intrinsic root SHOULD be
  opt-in (config or flag), not a silent default.
- **File trust.** `config.toml` is a local, user-owned file read at the
  invoking user's trust level; because it holds only env-var *names*, not
  secrets, no special file mode is REQUIRED.

## Conformance Testing

Conformance tests for this specification live in `zz-tests_bats/`.

Tests use binary injection via `bats-emo`:

    require_bin CUTTING_GARDEN cutting-garden

### Covered Requirements

| Requirement | Test File | Description |
|-------------|-----------|-------------|
| § File Location, missing file is empty | `config.bats` | with no `config.toml`, intrinsic roots still surface and no error occurs |
| § Root-Provider, aggregation | `config.bats` | a seeded caldav account surfaces as a root in `list` (no URI arg) and an MCP `resources/list` roundtrip |
| § Root Sources, intrinsic | `config.bats` | the file plugin surfaces its working directory as a root with no config |
| § Credential Resolution, precedence | `config.bats` | a node matching an account uses that account's `password_env`; a non-matching host falls back to `CALDAV_USERNAME`/`CALDAV_PASSWORD` |
| § Loading and Validation | `config.bats` | an unknown key warns but does not abort; a duplicate `name`/empty `url` aborts with EX_USAGE naming the entry |

Struct-level rules (`Validate` rejections, `Roots` projection,
credential-precedence unit behavior, import-cycle-free layering) are covered by
Go unit tests in `internal/cgconfig`, `internal/config_common`, and
`internal/cutting_garden_plugin_caldav`.

## Compatibility

This subsystem is additive and changes no existing on-disk capture receipt.

- **Backwards behavior is preserved.** With no `config.toml`, or when no
  account matches a URI, credential resolution falls through to today's
  global-environment behavior (step 3) and the commands behave as before.
- **Explicit URIs still work.** `list`/`capture`/`diff`/`mcp` MAY continue to
  accept explicit endpoint URIs; for `mcp`/`list` an explicit URI overrides
  config-driven root enumeration, and an argv URI is resolution step 1 for
  credentials.
- **tommy version skew** is caught at build time: the generated decoder stamps
  the producing tommy build, and `tommy generate --check` in the merge gate
  fails on drift.

## Relationship to RFC 0006

RFC 0006 (host-bound plugin resolution) and this RFC are **distinct axes**:

- RFC 0006 resolves *which plugin* handles a `(scheme, host)` — routing.
- This RFC resolves *which roots and credentials* a resolved plugin uses —
  configuration.

RFC 0006 §Effective Bindings deferred a configuration layer that could override
or reorder binding precedence, naming the equal-specificity registration panic
as the tripwire for building it. This RFC builds a config *subsystem* but
deliberately does **not** add the binding-override layer: binding precedence
remains "effective = registered" as RFC 0006 specifies. A future revision MAY
add a `[bindings]` section to this file to realize RFC 0006's deferred
override; that is out of scope here. Consumers MUST NOT assume this file
influences RFC 0006 resolution in this revision.

## References

### Normative References

- [RFC 0006]: `./0006-host-bound-plugin-resolution.md` — host-bound plugin
  resolution; defines the deferred configuration layer this RFC partially
  realizes and the host-matching rules this RFC reuses.
- [RFC 2119]: https://www.rfc-editor.org/rfc/rfc2119 — requirement keywords.

### Informative References

- [RFC 0005]: `./0005-protocol-only-plugin-resolution.md` — the scheme-keyed
  registry and capability-probing pattern (`RegisteredPlugins`, type-asserted
  capabilities) `RootProvider` follows. (In review as cutting-garden#50;
  number 0005 is reserved for it.)
- FDR 0014 — plugin root traversal (`RootLister`, `Node`, `Types()`), the
  primitive `RootProvider` extends.
- FDR 0015 — the MCP resource-traversal server, the first consumer of
  aggregated roots.
- cutting-garden#63 — host-keyed dispatch design; records the config-layer
  deferral this RFC acts on. #55 (sftp), #54 (webdav), #53 (github) — planned
  account-bearing plugins this schema extends to. #73 — expose plugin resources
  through an MCP server.
- madder `MakeSSHClientFromSSHConfig`
  (`internal/foxtrot/blob_stores/util_ssh.go`) — the URI-explicit-over-host-
  resolved credential precedence this RFC generalizes; madder's `// TODO move
  to a config_common package` over its shared `TomlUriV0` is the shared-base
  prior art.
- `github.com/amarbel-llc/tommy` — the TOML codegen (`tommy generate`,
  `Validate`, `Undecoded`); madder's `internal/charlie/blob_store_configs` is
  the usage pattern.

[RFC 0006]: ./0006-host-bound-plugin-resolution.md
[RFC 0005]: ./0005-protocol-only-plugin-resolution.md
[RFC 2119]: https://www.rfc-editor.org/rfc/rfc2119
