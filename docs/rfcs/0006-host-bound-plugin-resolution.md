---
status: proposed
date: 2026-06-07
---

# Host-Bound Plugin Resolution

## Abstract

Cutting-garden resolves a capture or diff source's backend plugin by
URI scheme, one plugin per scheme. This document extends resolution to
scheme **plus host**: a plugin MAY declare host bindings (exact
hostnames or suffix wildcards) for a scheme it does not own, and the
orchestrator routes a hierarchical URI to the most specific binding,
falling back to the scheme's default claimant. This lets, for example,
a google-images plugin handle `https://images.google.com/…` while
another plugin remains the general `https` handler.

## Introduction

The scheme-keyed registry ([RFC 0005], which this document layers on)
maps each URI scheme to exactly one plugin. That cannot express two
plugins sharing a transport scheme split by host — the motivating case
is the google-images plugin (amarbel-llc/cutting-garden#61) wanting
`images.google.com` URLs while the yt-dlp plugin (or a future general
web plugin) handles other `https` hosts. FDR 0003 §"Future
host-routing layer for `https`" sketched a router meta-plugin for
this; the design pass for amarbel-llc/cutting-garden#63 (see
`docs/plans/2026-06-07-scheme-host-dispatch-design.md`) chose instead
to make host bindings native to the [RFC 0005] registry, keeping one
registry and one resolution rule.

### Scope

This document specifies:

- The host-binding declaration surface (an OPTIONAL capability
  interface on the base `Plugin`).
- The binding grammar (exact hostnames and leading-label suffix
  wildcards) and its validation rules.
- The `(scheme, host)` resolution algorithm and its precedence
  ladder.
- Registration-time conflict rules.
- The registry's binding-enumeration surface for introspection
  tooling.

### Out of Scope

- Restore routing. Restore resolves by receipt *kind*
  (`protocol_registry.go`), never by source URI.
- Capability dispatch. Once a plugin is resolved, the
  protocol-over-`EntryV1` precedence of [RFC 0005] applies unchanged:
  host routing picks *which* plugin; capabilities pick *how* it
  dispatches.
- Host extraction from opaque URIs. An opaque prefix (`ytdlp:…`,
  `git:…`) is an explicit plugin choice; this document never
  second-guesses it (§ Resolution).
- The configuration override layer. Resolution is defined over
  *effective bindings*; this revision defines effective = registered
  (§ Effective Bindings) and defers any config-file override to a
  future revision.

## Requirements Language

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this
document are to be interpreted as described in [RFC 2119].

## Specification

### Host Bindings

A plugin declares host bindings by implementing the OPTIONAL
`HostBinder` capability interface, probed by type assertion exactly
as [RFC 0005] probes `SourceValidator`:

```go
type HostBinder interface {
    HostBindings() []HostBinding
}

type HostBinding struct {
    Scheme string // URI scheme the binding applies to, e.g. "https"
    Host   string // "images.google.com" or "*.google.com"
}
```

- `Scheme` MUST be a non-empty scheme. The empty scheme (schemeless /
  local filesystem) MUST NOT appear in a binding (§ Security
  Considerations).
- `Host` MUST be either an **exact pattern** (a hostname) or a
  **wildcard pattern**: `*.` followed by a hostname of at least one
  label. `*` is only valid as the entire leading label; embedded or
  trailing wildcards (`img*.google.com`, `images.*`) are invalid.
- Patterns MUST be lowercase; hosts are compared case-insensitively
  by lowercasing the URI host before matching. Patterns MUST NOT
  contain a port, userinfo, or path component.
- A violation of any rule above MUST cause registration to panic (a
  programming error, consistent with [RFC 0005]'s duplicate-scheme
  panic).

`Schemes()` retains its [RFC 0005] meaning unchanged: the schemes the
plugin claims as **default claimant**. Host bindings are additive
claims on *other* plugins' (or unclaimed) schemes.

### Registration Rules

`MustRegisterScheme` ([RFC 0005]) additionally indexes the plugin's
host bindings when the plugin implements `HostBinder`:

1. A plugin MUST NOT both claim scheme `S` via `Schemes()` and
   declare a binding with `Scheme == S` (no self-shadowing).
   Registration MUST panic.
2. Two bindings of **equal specificity** (identical `(Scheme, Host)`
   pattern) from any plugins MUST cause registration to panic.
3. Bindings of differing specificity on the same scheme (e.g. one
   plugin's `images.google.com`, another's `*.google.com`) are
   permitted; resolution order disambiguates.
4. A plugin that declares host bindings MUST also claim at least one
   scheme of its own via `Schemes()` (its explicit opaque form). This
   guarantees every plugin remains reachable when its bindings are
   removed or misroute (§ Compatibility — Rollback).

### Resolution

To resolve the plugin for a source argument, the orchestrator MUST:

1. Parse the argument; let `S` be the URI scheme and `H` be the URL's
   authority host, lowercased. Opaque URIs (no authority) have
   `H == ""`.
2. If `S` is empty, resolve the schemeless default plugin ([RFC
   0005]); host routing MUST NOT apply.
3. If `H` is non-empty and an exact binding `(S, H)` exists, resolve
   its plugin.
4. Else, if `H` is non-empty and one or more wildcard bindings on `S`
   match `H`, resolve the plugin of the matching pattern with the
   most labels.
5. Else, if a default claimant for `S` exists, resolve it.
6. Else, fail resolution. When step 6 is reached on a scheme that has
   at least one host binding, the error MUST name the unmatched host
   and SHOULD hint at the explicit opaque schemes of the plugins
   bound on `S`. Otherwise the [RFC 0005] unknown-scheme behavior
   (including the schemeless-fallback heuristic for arguments like
   `myfile:txt`) applies unchanged.

A wildcard pattern `*.D` matches any host of the form `<labels>.D`
with one or more leading labels. It MUST NOT match the apex `D`
itself; an apex claim requires an exact binding.

#### Examples

Given: plugin GI binds `("https", "images.google.com")`; plugin W
binds `("https", "*.google.com")`; no default claimant for `https`.

| Argument | Resolves to |
|---|---|
| `https://images.google.com/x` | GI (exact, step 3) |
| `https://photos.google.com/x` | W (wildcard, step 4) |
| `https://a.b.google.com/x` | W (wildcard matches any depth) |
| `https://google.com/x` | error (apex unmatched, step 6) |
| `https://example.com/x` | error naming `example.com` (step 6) |
| `gimages:https://example.com/x` | GI (its own opaque scheme, step 5) |
| `./tree` | filesystem plugin (step 2) |

### Effective Bindings

Resolution operates over the registry's *effective bindings*. This
revision defines effective bindings as exactly the registered
bindings. A future revision MAY introduce a configuration layer that
overrides or reorders effective bindings; the registration-time
equal-specificity panic (§ Registration Rules) is the designed signal
that such a layer has become necessary. Consumers MUST NOT assume
effective bindings and registered bindings stay identical across
future revisions.

### Introspection

The registry MUST expose enumeration of effective bindings —
`(scheme, pattern, plugin)` in precedence order — sufficient for
diagnostic tooling (the plugin-doctor command,
amarbel-llc/cutting-garden#62) to render the routing table without
private access.

## Security Considerations

- **The schemeless path is unroutable.** Host bindings MUST NOT
  apply to schemeless arguments or claim the empty scheme, preserving
  [RFC 0005]'s invariant that the local filesystem plugin is the only
  plugin reachable without an explicit scheme.
- **No DNS, no normalization beyond lowercasing.** Matching operates
  on the parsed URL host byte-wise after lowercasing. No DNS
  resolution, IDN/punycode mapping, or IP-literal canonicalization is
  performed. A plugin binding security-sensitive hosts MUST bind
  every textual variant it intends to claim (e.g. both `youtube.com`
  and `www.youtube.com`), as the ytdlp allowlist does today.
- **Routing is not authorization.** A binding determines which
  in-binary plugin handles a URL; all plugins live inside one trust
  boundary and register at init time. Per-plugin argument-injection
  and transport safeguards are unaffected and remain each plugin's
  responsibility.
- **Wildcard breadth.** A broad wildcard (`*.com`) is syntactically
  valid. Because registration is init-time and in-binary, this is a
  code-review concern, not a runtime one; introspection
  (§ Introspection) makes such claims visible.

## Conformance Testing

Conformance tests live in `zz-tests_bats/` alongside the existing
capture/diff lanes once an implementation exists (none does; this RFC
is `proposed`).

Tests use binary injection via `bats-emo`:

    require_bin CUTTING_GARDEN cutting-garden

### Covered Requirements

| Requirement | Test File | Description |
|-------------|-----------|-------------|
| § Resolution steps 3–4, exact beats wildcard | `host_routing.bats` | two bound plugins; exact-host URL routes to the exact binder |
| § Resolution step 6, error shape | `host_routing.bats` | unmatched host on a bound scheme errors naming the host and hinting opaque schemes |
| § Resolution step 2, schemeless invariant | `host_routing.bats` | schemeless args never host-route |
| § Compatibility, ytdlp equivalence | existing ytdlp lanes | accept/refuse matrix unchanged after allowlist→bindings migration |

Registration-rule panics (§ Registration Rules) are covered by Go
unit tests in `internal/cutting_garden_plugins`, not bats.

## Compatibility

This is an internal Go API extension within
`internal/cutting_garden_plugins` and its consumers
(`internal/capture`, `internal/diff`); there is no external or
on-disk format change, and existing capture receipts are unaffected.

Migration:

1. Implement binding indexing + the resolution ladder over the [RFC
   0005] registry.
2. Convert the ytdlp plugin's `httpsAllowlist` into host bindings
   (the YouTube and Instagram host sets), dropping its `https`
   default claim. `https` is left with no default claimant, so
   unmatched hosts fail at step 6 with an equivalent outcome to
   today's allowlist refusal (message text changes). The existing
   ytdlp accept/refuse test matrix MUST pass unchanged.
3. The google-images plugin (amarbel-llc/cutting-garden#61) registers
   its `images.google.com` binding as the second binder, exercising
   precedence.

Rollback: the change is additive and resolution is in-process only.
Reverting the migration commit restores scheme-only resolution; rule
4 of § Registration Rules guarantees every plugin's explicit opaque
scheme keeps working throughout, which is also the user-level escape
hatch for any misrouted URL.

This document supersedes FDR 0003 §"Future host-routing layer for
`https`" (the router-meta-plugin sketch), which is not adopted.

## References

### Normative References

- [RFC 0005]: `./0005-protocol-only-plugin-resolution.md` —
  scheme-keyed `Plugin` registry, capability-precedence dispatch,
  `SourceValidator` probing. (In review as
  amarbel-llc/cutting-garden#50 at time of writing; this document
  assumes it lands.)
- [RFC 2119]: https://www.rfc-editor.org/rfc/rfc2119 — requirement
  keywords.

### Informative References

- amarbel-llc/cutting-garden#63 — tracking issue for this design.
- amarbel-llc/cutting-garden#61 — google-images plugin (motivating
  case); #62 — plugin doctor (introspection consumer).
- FDR 0003 §"Future host-routing layer for `https`" — the superseded
  router sketch; FDR 0005 — URI-scheme plugin system.
- `docs/plans/2026-06-07-scheme-host-dispatch-design.md` — the
  approved design this RFC specifies, including tuning levers
  (tie handling, wildcard apex semantics, config-override deferral).

[RFC 0005]: ./0005-protocol-only-plugin-resolution.md
[RFC 2119]: https://www.rfc-editor.org/rfc/rfc2119
