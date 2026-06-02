# capture_plugin

The orchestrator-side emitter for the Capture Plugin Protocol
([RFC 0002](../../docs/rfcs/0002-capture-plugin-protocol.md)). It
serializes one capture as a merkle tree of typed hyphence node blobs and
writes every node through a generic `Writer`, returning the root
receipt's markl id.

This is the **in-process form** of RFC 0002 (§In-Process Plugin
Interface). There is no subprocess/JSON-batch path yet; an in-process
plugin (today: the git binding) calls `WriteReceipt` directly.

## The tree

```
receipt(<kind>) ──┬── identity ──┬── invocation        (jcs body)
                  │              └── environment ──┬── host    (jcs body)
                  │                                ├── binary  (jcs body)
                  │                                └── plugin  (plugin-defined)
                  ├── outcome                       (jcs body; per-run datetime)
                  └── payload ─▶ plugin-defined subtree
```

`WriteReceipt` (`receipt.go`) writes the protocol-defined nodes in
post-order — every child before its parent, so each reference line
carries a real digest. The caller writes its **payload** subtree first
(through the same `Writer`) and passes the resulting `Ref`(s) in
`ReceiptParams.PayloadRefs`.

## What lives here

- `Writer` / `NewBlobStoreWriter` (`writer.go`) — the node sink. The
  blob-store adapter reuses `plugin_blob_io.WriteReaderBlob`.
- `BuildNode` / `Ref` / `LockedRef` (`node.go`) — hyphence node
  serialization with FDR-0001 typed blob references
  (`- <alias> < @<digest> !<type>@<sig>`). Exported so bindings build
  their own payload nodes in the same framing.
- `RegisterType` / `SignatureFor` / `VerifyRef` (`typeregistry.go`) —
  the build-time type-signature registry (see above).
- `JCS` (`jcs.go`) — JCS-canonical JSON for node bodies (see the
  constraint note below).
- `GatherHost` / `HostInfo` / `BinaryInfo` (`environment.go`) — the
  identity environment leaves.
- `Invocation` / `PluginEnv` / `ReceiptParams` / `WriteReceipt`
  (`receipt.go`) — the request shape and the post-order driver.
- `types.go` — protocol type-strings + `ReceiptType(kind)`.

## Type signatures (the build-time registry)

Reference lines carry FDR-0001 type locks (`< @<digest> !<type>@<sig>`).
The `<sig>` is resolved through a **build-time embedded registry** — RFC
0002 §Type Signatures mechanism (1) — in `typeregistry.go`:

- Each type registers a `TypeDef` (its `iana_media_type`, and
  `payload_cardinality` for payload types) at `init()`. The protocol
  types register here; bindings register their own
  receipt/payload/leaf types (the git binding in `types_register.go`).
- The signature is the markl id of the type's **canonical type-blob** —
  its interface keys serialized as deterministic TOML
  (`canonicalTypeBlob`), hashed via a discard-store digester. Changing a
  type's interface keys changes its signature: that *is* the
  version-pinning the lock provides.
- `LockedRef(alias, digest, type)` fills `Ref.Sig` from the registry;
  `BuildNode` emits `@<sig>` whenever `Ref.Sig` is set (raw `Ref`s
  without a sig stay sig-less, so the framing tests can assert exact
  bytes). `SignatureFor` / `MediaTypeFor` expose the registry;
  `VerifyRef` enforces a lock on the consume side (sig-less = unlocked
  and always valid; a signed ref to a known type must match — a
  mismatch is a type-version-drift error).

## Deliberate simplifications (vs. the full RFC)

- **JCS via encoding/json.** `JCS` uses `json.Marshal` with HTML
  escaping disabled. That is JCS-equivalent only for the value shapes
  the protocol uses — ASCII object keys, strings, booleans, small
  non-negative integers. Callers MUST NOT pass floats or non-ASCII
  keys. A real RFC 8785 implementation is a follow-up if a binding ever
  needs those.
- **Binary digest omitted.** `BinaryInfo.Digest` is left empty by the
  git binding (non-deterministic `go test`/`go run` builds would churn
  identity). RFC 0002 RECOMMENDS populating it only under deterministic
  builds.
- **libc / kernel best-effort.** `GatherHost` reports `libc:"unknown"`
  and reads the Linux kernel release where available; refining detection
  is a documented follow-up.

## Bindings

Plugin-defined node schemas (payload, plugin-env, leaves) live in the
plugin's binding RFC, not here. The git binding is
[RFC 0004](../../docs/rfcs/0004-git-archive-binding.md), implemented in
`internal/cutting_garden_plugin_git/protocol.go`.
