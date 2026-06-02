---
status: proposed
date: 2026-06-02
promotion-criteria: |
  Promote to `experimental` once the requirement interface lands, the
  capture orchestrator enforces it, and the keepassxc plugin (FDR 0007)
  declares `StoreEncryptedAtRest`. Promote to `accepted` after the
  madder capability source (§The load-bearing dependency) is confirmed
  — i.e. the orchestrator can actually distinguish an encrypted store
  from a plaintext one — and the fail-closed default + override are
  validated end-to-end.
---

# capture store requirements

> **Design-only / scoping pass.** This FDR resolves the
> [FDR 0007 §At-rest enforcement open question](./0007-keepassxc-plugin.md#open-questions):
> how does a plugin refuse a destination store that can't hold its
> captures safely? No code exists yet; the interfaces below are a
> sketch for review.

## Problem Statement

A capture plugin and its destination blob store are chosen
independently. The CLI surface is `capture [STORE_ID | DIR]...`: the
user names a store group and the roots that fold into it
(`internal/capture/capture.go`), and the planner resolves a plugin per
root (`internal/capture/plan.go`) with **no knowledge of where the
bytes will land**. `ValidateSource` runs at plan time, before any store
is resolved, so it can't speak to the destination at all.

For the file and git plugins that is fine — any store will do. But the
keepassxc plugin (FDR 0007) **decrypts a vault and writes plaintext
field values as content-addressed blobs**. Writing those into a store
that isn't encrypted at rest silently turns "my passwords are in an
encrypted vault" into "my passwords are in cleartext on disk." Today
nothing stops that: the plugin gets a `BlobStoreInitialized` and writes
to it, no questions asked.

There is no general mechanism for a plugin to say *"my captures require
the destination to guarantee X."* This FDR scopes one. The motivating —
and, for now, only — requirement is **encrypted at rest**; the mechanism
is defined generically so later requirements (immutability/append-only,
large-blob support, …) extend it without re-plumbing.

## Interface

Following the repo's "everything is opt-in via narrow interfaces"
idiom (the `command.Cmd` opt-in surfaces, and the plugins package's
opt-in `ProtocolCapturePlugin`), a plugin **declares** its requirements
through a new opt-in interface; the framework **enforces** them. The
plugin never introspects store internals — it names capabilities from a
closed, framework-owned set.

### What the plugin declares

```go
// StoreCapability is a guarantee a capture destination may provide.
// The set is closed and framework-owned: plugins reference these
// constants, they do not inspect madder store config themselves.
type StoreCapability int

const (
    // StoreEncryptedAtRest: blobs written to the store are encrypted
    // at rest (e.g. an age-recipient store), so plaintext payloads are
    // not readable from the underlying medium without the store key.
    StoreEncryptedAtRest StoreCapability = iota
)

// StoreRequiringPlugin is the opt-in interface a CapturePlugin (or
// ProtocolCapturePlugin) implements to declare hard requirements on its
// destination. The orchestrator checks every returned capability
// against the resolved store before any capture I/O; an unmet (or
// unprovable) requirement fails that root. A plugin that does not
// implement this interface has no store requirements — the file, git,
// and yt-dlp plugins stay untouched.
type StoreRequiringPlugin interface {
    Plugin
    RequiredStoreCapabilities() []StoreCapability
}
```

The keepassxc plugin's entire participation is one method:

```go
func (Plugin) RequiredStoreCapabilities() []cutting_garden_plugins.StoreCapability {
    return []cutting_garden_plugins.StoreCapability{
        cutting_garden_plugins.StoreEncryptedAtRest,
    }
}
```

Declarative (return the set) rather than imperative (`CheckStore(...) error`)
on purpose: the capability set is closed, so the **framework** owns the
diagnostic and the override hint, and every plugin requiring the same
capability fails with the same actionable message instead of
hand-rolling its own.

### What the framework provides

The orchestrator derives, from the resolved store, which capabilities it
provides — as a **tristate**, because for some store types a capability
is genuinely *unknowable* (a custom/remote store cutting-garden can't
introspect):

```go
type Tristate int
const ( Unknown Tristate = iota; Yes; No )

// StoreCapabilities reports which guarantees a resolved store provides.
// Derived by the orchestrator from the madder store config
// (§The load-bearing dependency).
type StoreCapabilities interface {
    Provides(StoreCapability) Tristate
}
```

### Enforcement point

The check slots into the existing capture loop in
`internal/capture/capture.go`, at the one place where both the resolved
store and the plugin are in hand — after the per-group store is
resolved (`capture.go:106–113`) and **before** the plugin is invoked
(`CaptureProtocol` at `:129` / `CaptureRoot` at `:153`):

```
for each root in group:
    if rp, ok := root.plugin.(StoreRequiringPlugin); ok:
        if err := checkStoreRequirements(rp, blobStore, allowOverride); err != nil:
            sink.Failure(root.path, err)   // existing per-root failure path
            failCount++
            continue
    ... existing CaptureProtocol / CaptureRoot dispatch ...
```

This reuses the loop's existing per-root failure handling verbatim
(`sink.Failure` + `failCount++` → the `failCount > 0` cancel at
`capture.go:222`), so a requirement violation behaves exactly like any
other root failure: that root is skipped, the rest of the group still
captures, and the command exits nonzero. The check is per-root for
loop-granularity simplicity; hoisting it to once-per-(plugin,store) is a
later micro-optimization, not a behavior change.

`ValidateSource` is the wrong home — it runs at plan time with no store
resolved. Requirements are inherently a destination concern, so they
live in the loop, not the planner.

### Fail-closed, with an explicit override

For each required capability the rule is:

| `store.Provides(cap)` | result |
|---|---|
| `Yes` | requirement met |
| `No`  | **fail** the root |
| `Unknown` | **fail** the root (fail-closed) |

Fail-closed on `Unknown` is the only safe default for a plugin whose
reason to exist is confidentiality: if cutting-garden *cannot prove* a
store encrypts at rest, it must not write plaintext secrets there. The
diagnostic must be actionable — name the store, the unmet capability,
and the escape hatch:

```
kdbx:/home/me/secrets.kdbx: destination store "backup" is not (provably)
encrypted at rest; the keepassxc plugin captures plaintext secrets and
refuses an unencrypted destination
hint: target an age-encrypted store, or pass --allow-plaintext-secrets
```

The override exists because fail-closed will sometimes be wrong: a store
on a LUKS/FileVault volume *is* encrypted at rest, but madder can't see
that, so it reports `Unknown`. A capture-command flag
`--allow-plaintext-secrets` (and/or a `CUTTING_GARDEN_ALLOW_PLAINTEXT_SECRETS`
env) downgrades every requirement failure to a sink **warning** and
proceeds. The override is global to the invocation, loud, and never the
default — the user opts into the risk explicitly.

## The load-bearing dependency — can a store be told apart?

The whole feature hinges on one thing this repo does **not** currently
have: a way to derive `StoreEncryptedAtRest` from a resolved store. The
only store introspection cutting-garden does today is in
`capture_receipt.ComputeStoreHint`, which reads
`blobStore.BlobStore.GetBlobStoreConfig()` (a
`madder/.../blob_store_configs.Config`) and `GetDefaultHashType()`. So
the capability adapter — the single place that touches madder config
internals — would live alongside that, deriving the tristate from the
config. Three cases, in preference order:

1. **Madder already models encryption** (e.g. the config carries an
   age-recipient / cipher field). The adapter reads it: present →
   `Yes`, an explicitly-plaintext store type → `No`. Cheapest; confirm
   first.
2. **Madder exposes it only structurally** — the adapter infers from the
   concrete config type (`blob_store_configs.TypeStructForConfig(cfg)`)
   or a known field, mapping recognized encrypted store types to `Yes`
   and recognized plaintext ones to `No`, everything else `Unknown`.
   Workable, but couples cutting-garden to madder's store-type taxonomy.
3. **Madder exposes nothing usable** — needs an upstream madder change
   to surface an "encrypted at rest" predicate on the store/config
   interface. This is a hard dependency and the promotion-criteria gate;
   until it lands the keepassxc plugin's requirement always evaluates
   `Unknown` → fail-closed → unusable without `--allow-plaintext-secrets`
   (which, notably, is still a *safe* degraded state — it just isn't the
   intended one).

Confirming which case holds against the pinned madder
(`v0.3.30-0.20260526123337-…`) is the first implementation task.

## Scope

- **Capture only.** Requirements gate writing secrets *into* a store.
  Restore (FDR 0007) *reads* plaintext blobs and writes a freshly
  **encrypted** `.kdbx` out — it produces ciphertext, so it needs no
  store requirement. Diff reads the receipt and a live vault and writes
  no secrets. Neither is gated.
- **No receipt / wire-format change.** This is a pre-capture admission
  check; it does not touch RFC 0002, the bindings, or any node schema.
  No `TypeTag` impact.
- **No change to non-declaring plugins.** A plugin that doesn't
  implement `StoreRequiringPlugin` is unaffected; the type-assertion
  simply doesn't fire.
- **Closed capability set, one member.** `StoreEncryptedAtRest` is the
  only capability defined now. The enum and `Provides` shape are built
  to extend, but adding members is a future FDR's job.

## Open Questions

- **Capability source (the dependency above)** — which of the three
  cases holds in the pinned madder, and if (3), what's the smallest
  upstream surface to add (a `GetBlobStoreConfig`-level predicate, or a
  store-level `Provides`?).
- **Override granularity** — invocation-global `--allow-plaintext-secrets`
  (simple, proposed) vs. per-store opt-in (e.g. marking a specific store
  as trusted-encrypted in config). Start global; revisit if users want
  to bless one store without blanket-disabling the gate.
- **Diff/restore drift** — should restore *warn* when the source store
  it's reading plaintext out of is itself unencrypted (a capture that
  predates this gate, or one made with the override)? Read-only, but a
  one-line notice may be worth it.
- **Capability vs. policy** — `StoreEncryptedAtRest` is a store *fact*.
  A future "this store is allowed to hold secrets" is a *policy* that
  might not map 1:1 to encryption (an encrypted-but-shared store).
  Keeping the enum to verifiable facts and pushing policy to the
  override keeps the model honest; noted so it isn't conflated later.

## References

- FDR 0007 — keepassxc plugin (the plaintext-secrets motivation; this
  resolves its §At-rest enforcement open question).
- FDR 0005 — URI-scheme plugin system (the `Plugin` interface this
  extends).
- RFC 0002 — Capture Plugin Protocol (unaffected; requirements are a
  pre-capture gate).
- `internal/capture/capture.go` — the enforcement seam.
- `internal/capture_receipt/store_hint_compute.go` — the existing
  store-introspection path the capability adapter would sit beside.
