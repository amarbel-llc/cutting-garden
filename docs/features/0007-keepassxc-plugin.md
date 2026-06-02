---
status: proposed
date: 2026-06-02
promotion-criteria: |
  Promote to `experimental` once the plugin lands and at least one
  manual end-to-end capture/diff/restore round-trip against a real
  kdbx4 database (password and key-file credentials) is documented.
  Promote to `accepted` after the plaintext-at-rest posture is
  reviewed against a concrete blob-store target (§Security posture)
  and the plugin ships in the default binary.
---

# keepassxc plugin

> **Design-only.** This FDR is the "let's examine" pass requested
> against the git plugin (FDR 0006). No code exists yet; the package
> paths, types, and the binding schemas below are a sketch for review,
> not an implementation status. The git plugin is the template
> throughout — read this as "the git plugin, but for a `.kdbx` vault,"
> and the §Where the analogy snaps sections are where it stops being a
> mechanical port.

## Problem Statement

Cutting-garden captures filesystem trees (FDR 0001), yt-dlp media
(FDR 0003), and git remotes (FDR 0006). A KeePassXC database — a
`.kdbx` vault — is a file, so the file plugin already captures it. But
it captures the **ciphertext**, and that is worse than useless:

- A kdbx re-derives a fresh master seed and encryption IV on **every
  save**. The encrypted payload changes in full even when no entry
  changed, so a byte-level capture diffs as a total rewrite every time
  and dedups **nothing** across snapshots. The file plugin's content
  addressing — its whole value — is defeated by the format.

This is the exact situation the git plugin was built for, inverted.
Git is *already* a content-addressed merkle DAG, so the git plugin
**mirrors** that graph into madder. A kdbx is the opposite: it has a
rich internal tree (groups → entries → fields, with stable UUIDs and
per-entry history) that the encryption **hides**. The natural capture
is to **decrypt the vault and mirror its logical entry tree** into
madder's blob store as a content-addressed merkle tree — recovering
dedup, cheap re-capture, and a *semantic* diff ("entry X's password
changed") that the ciphertext can never give.

The URI-scheme plugin registry (FDR 0005) and the Capture Plugin
Protocol (RFC 0002) already support this: the kdbx plugin registers
under its own scheme and emits an RFC 0002 receipt merkle tree, exactly
as the git plugin does via `ProtocolCapturePlugin`.

## Security posture — plaintext entry tree (read this first)

The chosen posture (over "opaque blob" and "structural/redacted") is to
**store the decrypted entry tree, plaintext field values included**, as
content-addressed blobs. This buys the full git-like feature set —
per-field dedup, free incremental re-capture, semantic diff, and
**restore** — at one cost that must be stated loudly:

> **The capture store holds your passwords in the clear.** Confidentiality
> is offloaded entirely to the destination blob store. The kdbx plugin
> MUST refuse to capture into a blob store that does not provide at-rest
> encryption / equivalent access control (enforcement mechanism is an
> open question — see below).

Two subtleties that survive *any* posture and are worth recording so a
later "let's just hash the secrets instead" proposal doesn't relitigate
them as if they were free:

1. **Content addressing is a confirmation oracle.** A field value is
   keyed by the hash of its bytes. Anyone who can read blob digests can
   confirm a guessed password (`hash(guess) == digest?`) and detect
   password **reuse** across entries (same digest) — even in the
   "redacted to HMACs" posture. Storing plaintext is *honest* about a
   leak that hashing only appears to prevent. (An HMAC under a
   capture-time secret key closes the oracle but breaks cross-capture
   dedup, which is the point of capturing — so the redacted posture is
   self-defeating for this format. That is why this FDR picks plaintext
   outright rather than hashing.)
2. **The credential is process-scoped, never persisted.** The master
   password / key file is read at capture/restore time only (§Credentials)
   and never written into a receipt or blob.

## Interface

### Accepted argument forms

The plugin claims the single `kdbx` scheme. A bare `.kdbx` path is left
to the **file plugin** (opaque ciphertext capture); the `kdbx:` prefix
is the explicit opt-in to **decrypt-and-capture-the-tree**. This mirrors
the git plugin's "always opt-in via `git:`, no schemeless claim, no host
allowlist" stance.

1. `kdbx:<path>` — opaque, preferred. The inner path is any local
   `.kdbx` file (`/abs/path.kdbx`, `./rel.kdbx`). Preserved verbatim,
   `-`-leading paths refused (belt-and-suspenders; there is no child
   process, mirroring the git plugin's argument guard).

A key file, when used, is **not** part of the URI (it is a credential,
resolved from the environment — §Credentials). There is no hierarchical
`kdbx://host/...` form in v1: a vault is a local file, not a host/path
service. Fetching a remote `.kdbx` (WebDAV/https sync targets) is an
open question.

The capture identity stamped onto the receipt is the network-free
absolute vault path (the git plugin's `<remote>#<branch>` analog —
here just the canonical path). Diff re-derives the same identity from
the same argument.

### Credentials — the master secret (the `auth.go` analog)

The git plugin's `authMethod` selects ssh-agent / token-env by transport.
The kdbx analog resolves the vault's **master credential**:

- **Master password** — `CUTTING_GARDEN_KDBX_PASSWORD` env var (mirrors
  `CUTTING_GARDEN_GIT_TOKEN`). Never a CLI flag — that would leak the
  vault password into `ps`, shell history, and the process table.
- **Key file** — `CUTTING_GARDEN_KDBX_KEYFILE` env var holding a path
  (the path is not itself secret; the file's bytes are mixed into the
  composite key). KeePass composite keys allow password, key file, or
  both — the resolver supplies whichever is set, and a missing *all*
  surfaces as a clear "no kdbx credential" error up front (mirroring the
  git plugin's "is ssh-agent running?" early, specific failure) rather
  than a vague decrypt failure deep in the library.

Restore re-encrypts the rebuilt vault under the **same** env credential
(§Restore).

### Capture — the entry tree as a merkle tree

```
cutting-garden capture kdbx:/home/me/secrets.kdbx
```

1. Open and decrypt the vault in-process via the pure-Go kdbx library
   (§Runtime dependency) using the resolved credential. No
   `keepassxc-cli`, no subprocess — the git plugin's "no `git` binary"
   philosophy, ported.
2. Walk the decrypted document and mirror its tree into madder, each
   piece stored **individually** as its own content-addressed blob:
   - **field/binary leaves** — each entry field value (Title, UserName,
     Password, URL, Notes, custom fields) and each attachment is a
     content-addressed leaf. The git **blob** analog. An unchanged
     password keeps its digest and stores once — within a vault (reuse
     dedups) and across captures.
   - **entry nodes** — an entry is a node referencing its field/binary
     leaves plus a metadata body (UUID, Times, AutoType, and the
     entry's **History** array — see open questions). The git **tree**
     analog at entry granularity.
   - **group nodes** — a group references its child groups and entries
     plus a metadata body (UUID, Name, Notes, Icon). The git **subtree**
     analog.
   - **payload node** — references the root group and a database-metadata
     body (db name, KDF/cipher params *as recorded metadata only*,
     recycle-bin config). The merkle root, the git **tip/payload** analog.
3. Emit the RFC 0002 receipt tree over that payload (§RFC 0002 receipt
   tree). Object refs sorted deterministically so re-captures are
   byte-stable, exactly as the git plugin sorts by oid.

Dedup is automatic and at field granularity: an unchanged entry's node
digest is stable, so the entry and all its fields store once across
captures even though the surrounding ciphertext changed completely.
**That recovery of dedup is the entire point** — it is what the file
plugin cannot do for this format.

#### Incremental re-capture — *where the analogy snaps (1)*

The git plugin needs a wire-protocol `have`/`want` negotiation
(`internal/gitwire` → seeded go-git fetch) to avoid re-downloading a
remote's full closure. **A kdbx has no wire and no remote** — it is a
local file you always hold in full. So "incremental" is *free and
trivial here*: decrypt the whole (cheap, local) file, build the tree,
and content addressing makes the write store only the nodes/leaves that
are actually new against the prior receipt. There is **no negotiation
primitive, no seeded storer, no delta-over-the-wire** — that entire
subsystem of the git plugin (`gitobj.go` seeding, `remote.go`
`fetchBranchInto`, the `TestSeededStorer_FetchTransfersOnlyDelta`
machinery) collapses to "the blob store already dedups." The
`PriorReceiptDigest` the orchestrator passes is therefore not needed for
*storage* economy; it is useful only as the diff baseline.

### Diff — semantic, UUID-keyed — *where the analogy snaps (2)*

```
cutting-garden diff <kdbx-receipt> kdbx:/home/me/secrets.kdbx
```

The git plugin's diff is a two-stage *content-address symmetric
difference*: probe the tip cheaply, and on a move report `A <oid>` /
`D <oid>` lines. Content addressing alone can't tell a rename from an
add+delete — git oids are anonymous.

KeePass entries carry **stable UUIDs**, so the kdbx plugin can do
strictly better — a *semantic* diff. There is no cheap remote tip probe
(the file is local; just decrypt it), so diff:

1. Decrypts the live vault and builds its tree.
2. Matches entries/groups across the receipt and the live tree **by
   UUID**, not by content digest.
3. Reports field-level, human-meaningful drift:

```
M kdbx:/home/me/secrets.kdbx
~ entry <uuid> "GitHub"  Password changed
~ entry <uuid> "GitHub"  Times.LastModified changed
+ entry <uuid> "AWS root"            (added, in group "Cloud")
- entry <uuid> "old-vpn"             (removed)
> entry <uuid> "Mail"    moved  Personal/ -> Archive/
~ group <uuid> "Cloud"   renamed "AWS" -> "Cloud"
```

A mismatch exit (1) follows any drift. This is the reason to decrypt at
all: the ciphertext byte-diff is always "everything changed"; the UUID
join turns that into a precise changelog. Whether to print *which* field
values changed vs. only *that* they changed is a posture knob (the
plaintext posture permits showing them; a careful default may redact the
diff output even though the store holds plaintext — open question).

### Restore

```
cutting-garden restore <kdbx-receipt> ./restored.kdbx
```

Rebuild the gokeepasslib document from the merkle tree (groups, entries,
fields, binaries, history), **re-encrypt** it under the credential
resolved from the environment (§Credentials), and write a fresh `.kdbx`
at the destination. Each rebuilt entry/field is verified against its
recorded content digest as it is loaded (the integrity check the git
plugin's oid re-verification provides). The destination must not already
exist (`assertDestAbsent`, ported).

Routing is by **receipt kind** (`keepassxc`), not destination path —
identical to the git plugin: `restore` peeks the receipt's `! type`
line and dispatches a `keepassxc`-kind receipt to this binding's
`RestoreProtocol` via the kind-keyed protocol registry.

Note the restored vault is **not byte-identical** to the original (fresh
KDF salt/IV, and field ordering normalized) — but it is
*logically* identical and unlocks under the same credential. This is the
correct notion of restore for an always-re-encrypting format; a
byte-identical restore is impossible without storing the ciphertext,
which defeats the whole plugin.

## Runtime dependency — pure Go, no `keepassxc-cli`

Following the git plugin's no-binary stance, capture/diff/restore run
in-process on a **pure-Go kdbx library** — candidate:
`github.com/tobischo/gokeepasslib/v3` (reads/writes kdbx 3.1 and 4.x;
AES / ChaCha20; Argon2 / AES-KDF; password + key-file credentials). The
go-git analog: it removes any `keepassxc-cli` runtime dependency, so the
Nix flake need not wrap a `keepassxc` binary onto the installed PATH.

**Pre-implementation checklist** (verify before committing to the lib):
kdbx4 + Argon2id round-trip; key-file (both XML `<KeyFile>` and raw
formats); per-entry History preservation on write; attachment/binary
pool round-trip. If any gap exists, the fallback is the `keepassxc-cli`
exec template (FDR 0003 shape, `exec.go`) at the cost of the no-binary
property.

## RFC 0002 receipt tree — binding sketch (→ future RFC 0005)

`cutting-garden capture kdbx:…` emits a full RFC 0002 capture merkle
tree. The protocol nodes (receipt → identity → {invocation, environment
→ host/binary/plugin}, outcome) come from `internal/capture_plugin`
unchanged. The kdbx-specific subtree — the binding a future
**RFC 0005 (KeePassXC-Archive Binding)** would pin, the git plugin's
RFC 0004 analog — sketches as:

```
captureKind   = "keepassxc"                       // receipt: …-receipt-keepassxc-v1
payloadType   = "jcs-keepassxc-capture-payload-v1"
pluginEnvType = "jcs-keepassxc-capture-environment-v1"  // body: { kdbx_lib_version, kdbx_format_version }
captureFormat = "entry-tree"

leaf / node type-strings:
  keepassxc-capture-group-v1     // node: child refs + JCS body {uuid,name,notes,icon,times}
  keepassxc-capture-entry-v1     // node: field/binary refs + JCS body {uuid,times,autotype,history…}
  keepassxc-capture-field-v1     // leaf: a single field value's bytes
  keepassxc-capture-binary-v1    // leaf: an attachment's bytes
```

Body serialization uses `capture_plugin.JCS` (the same JCS-via-`encoding/json`
the git binding uses — ASCII keys, strings, small ints only; field
*values* are leaf blobs, not body fields, so arbitrary value bytes are
fine). Field/child refs are sorted deterministically (by UUID for nodes,
by field key then digest for leaves) so a re-capture of unchanged state
yields a byte-identical payload node — the git plugin's oid-sort
invariant, ported.

### TypeTag reuse

As with the git plugin, the protocol receipt carries its own
`…-receipt-keepassxc-v1` kind, but the EntryV1 `TypeTag()` would reuse
`capture_receipt.TypeTagV1` — because the plugin must still register as
an EntryV1 `CapturePlugin`/`DiffPlugin` for the `kdbx` scheme to resolve
at all (the orchestrator resolves EntryV1 then type-asserts the protocol
interface — the same #48 wart the git plugin documents). The vestigial
`CaptureRoot`/`ScanForDiff` stubs would return an error, identically.

## Plugin skeleton — package outline

A peer leaf of `cutting_garden_plugins/`, mirroring
`cutting_garden_plugin_git/` file-for-file where the analogy holds:

```
internal/cutting_garden_plugin_keepassxc/
  plugin.go            // Plugin{}; Schemes() = {"kdbx"}; TypeTag(); ValidateSource/DiffDir (structural, no decrypt)
  init.go              // init(): MustRegister{Capture,Diff,Restore} for "kdbx"
  url.go               // pathFromArg(*url.URL) (path, error); refuse leading '-'; the git url.go analog
  creds.go             // credential() → kdbx composite key from CUTTING_GARDEN_KDBX_{PASSWORD,KEYFILE}; the auth.go analog
  kdbxobj.go           // gokeepasslib <-> madder bridge; write/loadGroup|Entry|Field|Binary; the gitobj.go analog
  protocol.go          // CaptureProtocol: decrypt, walk, store nodes, writeKdbxReceipt; the git protocol.go analog
  diff_protocol.go     // DiffProtocol: decrypt live, UUID-keyed semantic diff vs receipt payload
  restore.go           // RestoreProtocol: rebuild document, re-encrypt under creds, write dest (assertDestAbsent)
  protocol_consume.go  // loadReceiptPayload / readNode; the git protocol_consume.go analog
  types_register.go    // RegisterType for the four leaf/node + payload + plugin-env types
  *_test.go            // fixtures built in-process with gokeepasslib (no binary); capture→diff→restore round-trips
```

Files in the git package with **no kdbx analog** (the snap points):
`remote.go`, `localtransport.go`, `protocol.go`'s negotiation half,
`incremental.go`, `negotiation_test.go`, `memstore_test.go` — there is
no wire, so no transport, no seeding, no delta negotiation. The kdbx
plugin is materially *smaller* than the git plugin on the capture side
and *richer* on the diff side.

## Open Questions

- **At-rest enforcement.** How does the plugin *refuse* a non-encrypted
  blob store (§Security posture)? The `blob_stores` interface may not
  expose an "encrypted at rest" predicate today; this may need a capability
  query or an explicit `--allow-plaintext-secrets` opt-in gate.
- **Entry History.** Capture the full per-entry History array (faithful,
  and content-addressing dedups unchanged historical versions for free)
  or only current state (smaller, loses the vault's own audit trail)?
  Leaning faithful.
- **Diff output redaction.** The store holds plaintext, but should
  `diff` *print* changed field values by default, or only "Password
  changed"? Leaning redact-by-default with a `--show-values` opt-in.
- **Remote vaults.** A `kdbx:https://…` / WebDAV form to capture a synced
  vault. Deferred; v1 is local files only.
- **Recycle bin / deleted-objects tombstones.** KeePass keeps tombstones
  for sync. Capture them (enables faithful merge-aware restore) or prune?
- **kdbx library gap → exec fallback.** If gokeepasslib lacks a needed
  kdbx4 feature, fall back to the `keepassxc-cli` exec template and
  reintroduce a runtime binary dependency (FDR 0003 shape).

## References

- FDR 0006 — git plugin (the merkle-tree capture template this mirrors).
- FDR 0005 — URI-scheme plugin system.
- FDR 0003 — yt-dlp plugin (the exec-a-tool fallback template).
- RFC 0002 — Capture Plugin Protocol (the merkle-tree capture model).
- RFC 0004 — Git-Archive Binding (the node-schema template a future
  RFC 0005 KeePassXC-Archive Binding would mirror).
- `internal/cutting_garden_plugin_git/` — the implementation this design
  ports.
