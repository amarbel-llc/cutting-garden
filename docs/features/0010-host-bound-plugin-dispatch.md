---
status: proposed
date: 2026-06-07
promotion-criteria: |
  Promote to `experimental` once RFC 0006's migration lands: the ytdlp
  httpsAllowlist converted to host bindings with the accept/refuse
  matrix passing unchanged. Promote to `testing` once a second binder
  (the google-images plugin, #61) exercises precedence in-tree.
  Tuning-lever stability (no lever moved for two weeks of real
  routing) gates `testing → accepted`.
---

# Host-bound plugin dispatch

> **Design-only.** No code exists. This FDR records the decisions from
> the #63 design pass (2026-06-07, brainstormed and approved); the
> normative contract lives in [RFC 0006]. Read this for *what and
> why*, the RFC for *exactly how*.

## Problem Statement

Plugin resolution is scheme-keyed: one plugin per URI scheme (FDR
0005). That cannot express two plugins sharing a transport scheme
split by host — the motivating case is a google-images plugin owning
`https://images.google.com/…` while another plugin handles other
`https` URLs (#61). FDR 0003 §"Future host-routing layer" predicted
this moment and sketched a router meta-plugin; with RFC 0005
consolidating resolution into a single registry, that sketch is no
longer the right shape.

## Interface

A plugin may declare **host bindings** — exact hostnames
(`images.google.com`) or leading-label suffix wildcards
(`*.google.com`) — on schemes it does not own. A hierarchical URI
routes to the most specific binding: exact beats wildcard, more
labels beat fewer, and the scheme's default claimant is the fallback.
Unmatched hosts on a bound scheme fail with an error naming the host
and hinting at the bound plugins' explicit opaque schemes:

    $ cutting-garden capture https://example.com/x
    error: no plugin bound for https host "example.com"; explicit
    forms: ytdlp:<url>, gimages:<url>

Behavior anchors (normative versions in [RFC 0006]):

- **Explicit opaque prefixes always win.** `ytdlp:<url>` /
  `gimages:<url>` are explicit plugin choices; host routing never
  second-guesses them, and every binding plugin must keep one. This
  is simultaneously the user-level misrouting escape hatch and the
  rollback path.
- **Schemeless arguments never host-route** — local paths stay
  file-plugin-only.
- **Ties are init-time errors**, not silently ordered.
- The routing table is introspectable (plugin-doctor, #62).

## Decisions and why

1. **New RFC layered on RFC 0005, not an amendment.** Keeps PR #50
   (which carries the #48 fix) shippable and each RFC
   single-concern.
2. **Registry-native bindings, not FDR 0003's router meta-plugin.**
   RFC 0005 just unified resolution into one registry; a router
   would re-fragment it into a shadow registry that doctor,
   validation, and capability probing must tunnel through.
3. **Plugins declare bindings; config overrides deferred.** #63 asked
   for config-driven precedence, but cutting-garden has no config
   subsystem and no real binding conflict exists yet. The
   equal-specificity init panic is the designed tripwire: the first
   legitimate tie justifies building the config layer.
4. **Exact + suffix wildcard only** — "most specific wins" stays
   decidable by inspection; glob/regex would forfeit that.
5. **Hierarchical authority only** — no parsing of inner URLs inside
   opaque forms, no plugin-supplied host extractors (circular: you'd
   resolve a plugin to ask it how to resolve).
6. **Capture+diff source resolution only** — restore routes by
   receipt kind and never sees a source URI.

## Examples

    # exact binding beats wildcard
    cutting-garden capture https://images.google.com/a   # → google-images
    cutting-garden capture https://photos.google.com/a   # → *.google.com binder

    # apex is not matched by a wildcard
    cutting-garden capture https://google.com/a          # → error

    # explicit prefix bypasses routing entirely
    cutting-garden capture gimages:https://example.com/a # → google-images

## Limitations

- No config-file override in v1 (deferred; see lever below and
  RFC 0006 §Effective Bindings).
- No path-component routing — host only. A plugin wanting
  `youtube.com/shorts/*` vs `youtube.com/watch` split must own the
  whole host and branch internally.
- No IDN/punycode normalization; binders of security-sensitive hosts
  enumerate textual variants, as the ytdlp allowlist already does.
- `complete` does not surface host bindings in v1 (doctor does).

## Tuning Levers

| Lever | Current | Rationale | Change signal |
|---|---|---|---|
| tie handling | init panic | no legitimate tie exists yet | a real plugin pair needs the same host |
| wildcard apex | excluded | exact-binding the apex is explicit | binders routinely duplicate apex+wildcard lines |
| config override | deferred | zero conflicts in tree | first panic-tie in the wild |

## More Information

- [RFC 0006] — the normative specification.
- `docs/plans/2026-06-07-scheme-host-dispatch-design.md` — approved
  design, approaches considered.
- amarbel-llc/cutting-garden#63 (tracking), #61 (google-images,
  motivating case), #62 (plugin doctor), #50/#48 (RFC 0005).
- FDR 0003 §"Future host-routing layer for `https`" — superseded
  sketch; FDR 0005 — the scheme registry this extends.

[RFC 0006]: ../rfcs/0006-host-bound-plugin-resolution.md
