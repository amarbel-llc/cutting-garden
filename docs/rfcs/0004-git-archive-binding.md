---
status: proposed
date: 2026-06-02
---

# Git-Archive Binding

## Abstract

This document is a **binding** of the [Capture Plugin Protocol
(RFC 0002)](./0002-capture-plugin-protocol.md) for the `git` capture
kind. It pins the plugin-defined node-type schemas the git plugin emits
under RFC 0002's plugin-defined slots: the payload subtree, the
plugin-defined environment node, and the per-object leaf types. The
protocol-defined nodes (receipt, identity, invocation, environment,
host, binary, outcome) are unchanged from RFC 0002 — only their
`<kind>` tag (`git`) and the git-specific subtrees are specified here.

The capture target is a git remote branch. The payload is git's own
object graph: every commit, tree, and blob reachable from the branch
tip is stored individually as a content-addressed leaf blob, and a
single payload node references them all. Because git objects are
content-addressed by construction, an unchanged object keeps its oid and
its bytes, so it stores once across re-captures — RFC 0002's automatic
merkle dedup applies at git-object granularity.

## Capture kind

```
! cutting_garden-capture-receipt-git-v1
```

The receipt's `<kind>` is `git`. The producing binary
(`environment.binary.name`) is `cutting-garden`; a second implementation
of git capture would emit the same kind-tagged receipt.

## Invocation

The git plugin populates the protocol-defined invocation body as:

| Field       | Value                                                        |
|-------------|-------------------------------------------------------------|
| `target`    | the canonical `<remote>#<branch>` identity (or bare `<remote>` for the default branch). |
| `format`    | `object-graph`.                                              |
| `normalize` | `false` — git objects are captured verbatim; no normalization pass. |
| `options`   | `{}`.                                                        |

## Plugin-defined environment

Type: `!jcs-git-capture-environment-v1`. Body (JCS):

```json
{"git_version":"<string>"}
```

| Field         | Required | Description                                              |
|---------------|----------|----------------------------------------------------------|
| `git_version` | yes      | `git version` output of the binary the plugin shelled out to. `"unknown"` if git could not be run. |

This is identity-affecting: captures made with different git versions
produce different environment markl-ids (git's object serialization is
stable across versions, but recording the tool keeps identity honest).

## Payload

The receipt's single `payload` reference points at one payload node that
owns the whole object list. (The plugin uses a single payload node
rather than thousands of direct `payload` refs on the receipt, keeping
the receipt small; the payload node declares the object list one level
down.)

Type: `!jcs-git-capture-payload-v1`. The node is both bodied and
reference-bearing.

Body (JCS):

```json
{"branch":"<string>","object_count":<int>,"remote":"<string>","tip":"<oid>"}
```

| Field          | Required | Description                                                              |
|----------------|----------|--------------------------------------------------------------------------|
| `remote`       | yes      | The git remote URL captured.                                             |
| `branch`       | yes      | The requested branch (`""` when the remote's default branch was used).   |
| `tip`          | yes      | The branch tip commit oid the object graph was reachable from.           |
| `object_count` | yes      | Number of object references in this node.                                |

References: one per stored git object, in `git cat-file
--batch-all-objects` enumeration order. The reference alias is the git
object's oid; the reference type is the object's git-kind leaf type:

```
- <oid> < @<digest> !git-capture-object-<git-type>-v1@<sig>
```

## Object leaves

Type: `!git-capture-object-<git-type>-v1` where `<git-type>` is one of
`commit`, `tree`, `blob`, `tag`.

An object leaf is **not** a hyphence node — it is the raw `git cat-file`
payload of the object (the bytes after git's `<type> <size>\0`
loose-object header), stored verbatim. Its markl-id is computed over
those bytes; its git oid is recoverable by re-prepending the git header
and hashing per git's rule. A consumer reconstitutes a repository by
writing each leaf back with `git hash-object -t <git-type> -w` (or
assembling a pack) and recreating the branch ref from the payload body's
`tip`.

Each git binding type is registered in the build-time type-signature
registry (RFC 0002 §Type Signatures mechanism (1), implemented in
`internal/capture_plugin/typeregistry.go`), so every reference to a git
node — the receipt's `payload` ref and the payload node's per-object
refs — carries an `@<sig>` type lock, and consumers verify it. The
registered interface keys are:

| Type | `iana_media_type` | `payload_cardinality` |
|---|---|---|
| `cutting_garden-capture-receipt-git-v1` | `application/vnd.cutting-garden.capture-receipt-git+hyphence` | — |
| `jcs-git-capture-payload-v1` | `application/vnd.cutting-garden.git-capture-payload+jcs` | `single` |
| `jcs-git-capture-environment-v1` | `application/vnd.cutting-garden.git-capture-environment+jcs` | — |
| `git-capture-object-<git-type>-v1` | `application/vnd.cutting-garden.git-object-<git-type>` | — |

These interim media-type/cardinality keys are documented here pending
the FDR-0010 graduation noted in RFC 0002 §IANA Media Type Interface.

## Stability

Per RFC 0002 §Stability Table, with git-specific notes:

| Node                                   | Stable across…                                                         |
|----------------------------------------|------------------------------------------------------------------------|
| object leaf (`git-capture-object-*`)   | every capture in which that git object is reachable — git's oid is the cross-capture handle; identical object ⇒ identical bytes ⇒ identical markl-id ⇒ stored once. |
| `jcs-git-capture-payload-v1`           | re-captures whose branch tip (and therefore reachable object set) is unchanged. |
| `jcs-git-capture-environment-v1`       | every capture with the same git version.                               |

The diff freshness probe (FDR 0006) leans on this: an unchanged tip oid
means an unchanged reachable object set, so a tip match needs no
re-clone.

## Restore

Restore reconstitutes a working clone checked out to the preserved
branch. The procedure:

1. `git init -b <branch>` a fresh repository at the destination (the
   branch name comes from the payload body).
2. For each object leaf, write its bytes back with
   `git hash-object -t <git-type> -w --stdin`, and verify the resulting
   oid equals the reference alias (an integrity check on the recreated
   object).
3. Point `refs/heads/<branch>` at the payload body's `tip` and
   `git reset --hard` to materialize the working tree.

Routing is by receipt kind: the `restore` command peeks the receipt
type-tag and dispatches a `cutting_garden-capture-receipt-git-v1`
receipt to the git binding's protocol-restore handler, independent of
the local destination path's scheme.

## Diff

Diff compares a git receipt against a live `git:` source by branch tip:
`git ls-remote` resolves the source's current tip and compares it to the
payload body's `tip`. Equal ⇒ no drift; moved ⇒ one difference line.
Object-level enumeration is a follow-up.

## References

- [RFC 0002: Capture Plugin Protocol](./0002-capture-plugin-protocol.md)
  — the protocol this binds.
- [RFC 0003: Web-Archive Binding](./0003-web-archive-binding.md) — the
  sibling binding for the `web` kind.
- [FDR 0006: git plugin](../features/0006-git-plugin.md) — the
  implementation and its capture/diff behavior.
- `internal/capture_plugin/` — the protocol emitter.
- `internal/cutting_garden_plugin_git/` — the git binding implementation.
