# Scheme+host plugin dispatch — design (#63)

Date: 2026-06-07. Status: approved in brainstorm; deliverable is
**RFC 0006 (Host-Bound Plugin Resolution)**, not code. Tracking issue:
amarbel-llc/cutting-garden#63.

## Problem

Plugin resolution is scheme-keyed (FDR 0005 registries; RFC 0005 / PR
#50 consolidates them into one scheme→`Plugin` registry with
capability-precedence dispatch). One plugin per scheme cannot express
`https:` → google-images for `images.google.com` while another plugin
handles other hosts. Motivating case: the google-images plugin PR
(#61, currently unlocated); FDR 0003 §"Future host-routing layer"
sketched the need.

## Decisions (brainstormed 2026-06-07)

1. **New RFC layered on RFC 0005** (assumes PR #50 merges roughly
   as-is) — not an amendment, not standalone. Keeps #48's fix
   shippable and each RFC single-concern.
2. **Plugin-declared bindings + config override later.** Plugins
   declare host bindings at registration; a config file is an
   override/disambiguation layer explicitly deferred until a real
   conflict exists. (Cutting-garden has no config subsystem today;
   `MakeUtility("cutting-garden", nil)` — this design does not
   introduce one.)
3. **Host grammar: exact + leading-label suffix wildcard**
   (`images.google.com`, `*.google.com`). No glob/regex.
4. **Hierarchical authority only.** Host routing reads url.Parse's
   authority. Opaque prefixes (`ytdlp:…`, `git:…`) are explicit
   plugin choices and are never second-guessed.
5. **Scope: capture+diff source resolution only.** Restore routes by
   receipt kind (`protocol_registry.go`) and never sees a source URI.

## Approaches considered

- **A — registry-native host bindings (chosen).** Extend RFC 0005's
  single registry; resolution becomes `(scheme, host)`. One registry,
  one rule; doctor/complete introspect directly; ytdlp's
  `httpsAllowlist` migrates mechanically.
- **B — host-router meta-plugin (FDR 0003's sketch).** A router
  plugin claims `https`; downstreams sub-register with it. Rejected:
  re-fragments resolution into a shadow registry right after RFC 0005
  unified it; doctor/validation/capability probing must tunnel
  through the router.
- **C — config-only routing table.** Rejected: nothing works without
  user config; plugins can't ship sensible defaults.

## Design

### Binding declaration

`Schemes()` keeps its RFC 0005 meaning: *default claimant*. Host
claims are a new optional capability interface (probed like
`SourceValidator`):

    type HostBinder interface {
        HostBindings() []HostBinding
    }

    type HostBinding struct {
        Scheme string // e.g. "https"
        Host   string // "images.google.com" or "*.google.com"
    }

A plugin MAY have both (ytdlp: `Schemes() = ["ytdlp"]` + bindings on
`https`). A plugin MUST NOT both default a scheme and bind hosts on
it (no self-shadowing); the registry rejects it at registration.

### Resolution order

For an argument parsing to `(scheme, host)`:

1. exact host binding;
2. longest-suffix wildcard binding (more labels beats fewer);
3. scheme-default claimant (today's behavior);
4. error naming the unmatched host, hinting at explicit opaque
   prefixes.

Wildcards match subdomains at any depth but NOT the apex
(`*.google.com` ∌ `google.com`; apex needs an exact binding).
Equal-specificity duplicates panic at init (same spirit as duplicate
scheme claims).

### Invariants preserved from RFC 0005

- Schemeless args never host-route (file plugin only).
- Capability-precedence dispatch (protocol > EntryV1) untouched: host
  routing picks *which* plugin; capabilities pick *how*.
- The schemeless-fallback heuristic for unrecognized schemes
  (`myfile:txt`) is unchanged.

### Migration (proof case)

ytdlp's `httpsAllowlist` converts to host bindings (YouTube +
Instagram hosts on `https`); ytdlp stops claiming `https` as
scheme-default; `https` ends with no default claimant, so an
unmatched `https` host errors with the same observable outcome as
today's allowlist refusal (message text changes to name host
routing). Receipts byte-unaffected. Correctness gate: the existing
ytdlp accept/refuse matrix passes unchanged. The revived
google-images plugin (#61) then binds `images.google.com`.

### Rollback

Additive; no wire format. Permanent dual-architecture: every plugin
MUST keep its explicit opaque scheme — the user-level misrouting
escape hatch and the rollback path (revert the bindings commit;
explicit prefixes still reach every plugin).

### Config override — deferred

RFC 0006 names the override layer and specifies only the hook:
resolution consults "effective bindings"; v1 defines effective =
registered. The init-time tie panic is the deliberate signal: the
first legitimate tie triggers the config design — not before.
Documented as a limitation referencing #63's config language.

### Tuning levers

| Lever | Current | Rationale | Change signal |
|---|---|---|---|
| tie handling | init panic | no legitimate tie exists yet | a real plugin pair needs the same host |
| wildcard apex | excluded | exact-binding the apex is explicit | binders routinely duplicate apex+wildcard lines |
| config override | deferred | zero conflicts in tree | first panic-tie in the wild |

### Surfacing & testing

Registry exposes binding enumeration; plugin-doctor (#62) renders the
`(scheme, pattern) → plugin` table in specificity order; `complete`
unaffected in v1. Tests: registry precedence-ladder + panic unit
tests; ytdlp equivalence matrix; one bats lane asserting the
unmatched-`https`-host error shape and hint.

### Promotion criterion

RFC `proposed → experimental` once the ytdlp migration lands with the
equivalence matrix green and a second binder (google-images)
exercises precedence.

## References

- amarbel-llc/cutting-garden#63 (tracking), #61 (google-images),
  #62 (plugin doctor), #48 / PR #50 (RFC 0005).
- FDR 0003 §"Future host-routing layer for `https`" — the superseded
  router sketch.
- FDR 0005 — URI-scheme plugin system.
