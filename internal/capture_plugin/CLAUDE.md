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
- `BuildNode` / `Ref` (`node.go`) — hyphence node serialization with
  FDR-0001 typed blob references (`- <alias> < @<digest> !<type>`).
  Exported so bindings build their own payload nodes in the same framing.
- `JCS` (`jcs.go`) — JCS-canonical JSON for node bodies (see the
  constraint note below).
- `GatherHost` / `HostInfo` / `BinaryInfo` (`environment.go`) — the
  identity environment leaves.
- `Invocation` / `PluginEnv` / `ReceiptParams` / `WriteReceipt`
  (`receipt.go`) — the request shape and the post-order driver.
- `types.go` — protocol type-strings + `ReceiptType(kind)`.

## Deliberate simplifications (vs. the full RFC)

- **Sig-less references.** Reference lines omit the optional `@<sig>`
  type-lock (`< @<digest> !<type-string>`). RFC 0002 §Type Signatures
  permits this; the type-string alone identifies the type. Adding a
  signed type-blob registry is future work.
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
