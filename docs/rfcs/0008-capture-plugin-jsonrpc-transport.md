# RFC 0008 — Capture Plugin Transport: JSON-RPC + FD-passed blob sockets

> Renumbered from RFC 0007 (and before that 0005): 0005 went to
> protocol-only plugin resolution, and 0007 was independently claimed by
> the config subsystem, which has the wider reference footprint.

- Status: **proposed** — cutting-garden's orchestrator side and the
  shared `capture_serve` library (peer, handshake, launch, plugin-side
  `Serve`) are implemented and ratify this text: byte identity between
  the in-process form and a spawned v2 session is pinned by
  `internal/capture_serve`'s conformance tests, and the packaged test
  peer runs under the nix-sandbox bats lane. Flips to **accepted** when
  a second implementation (chrest's `capture-serve`) passes the same
  §Conformance bar cross-process.
- Date: 2026-06-03 (transport handshake revised 2026-07-12: inherited
  socketpair replaced by the go-plugin-style announce/dial launch, per
  the madder RFC 0001 pattern; see §Launch)
- Supersedes: RFC 0002 §Subprocess vs In-Process Plugins (subprocess
  half), §Writer Protocol, §Capture Plugin Protocol (the stdin/stdout
  batch invocation). The merkle-tree shape, node framing, type system,
  JCS, identity/stability model, and the In-Process Plugin Interface of
  RFC 0002 are **unchanged**.
- Schema bump: `capture-plugin/v1` → `capture-plugin/v2`.

## Abstract

RFC 0002 §Subprocess Form invokes a capture plugin as a one-shot CLI
(one JSON document in on stdin, one out on stdout) and writes every node
blob by **spawning a fresh writer process per blob** (`writer.cmd`),
piping the bytes through its stdin. A single capture emits ~11 nodes, so
a modest batch is dominated by `fork`/`exec` and by re-resolving the
blob store on every blob.

This RFC replaces that transport with a **persistent peer-to-peer
JSON-RPC 2.0 session over an `AF_UNIX` socket**, and moves blob bytes
**out of band**: for each blob the orchestrator creates a fresh pipe and
hands the plugin the write end via `SCM_RIGHTS` ancillary data
(`sendmsg`), then content-addresses the read end into its store. No
per-blob process spawn; no store re-resolution; blob bytes never traverse
the JSON-RPC channel.

The bytes written to the store, and therefore every markl id and the
whole identity/stability model, are **identical** to RFC 0002. This RFC
changes only *how* the orchestrator and plugin talk and *how* blob bytes
move — not *what* gets stored.

## Motivation

- **Cost.** `fork`/`exec` per blob is ~hundreds of µs–ms each; worse, the
  RFC 0002 reference writer (`cutting-garden __write-blob`) re-opens the
  blob store (config discovery, store init) on every invocation. For an
  N-capture batch that is `~11·N` process spawns and store opens.
- **Out-of-band bytes.** Streaming blob bytes over a JSON-RPC channel
  would force base64 (33% bloat) or an interleaved binary framing on the
  control channel. A passed FD keeps the control channel small and
  message-oriented and lets blob transfer be a plain `io.Copy` between two
  processes' kernel buffers.
- **Lifetime clarity.** A pipe whose write end the plugin closes gives an
  unambiguous end-of-blob (EOF) and a natural backpressure point, with no
  sentinel framing.
- **A real session.** A persistent connection lets the orchestrator
  negotiate protocol version + capabilities once (`initialize`), drive
  multiple captures, and cancel cleanly — none of which the one-shot CLI
  affords.

## Non-goals

- The **in-process form** (RFC 0002 §In-Process Plugin Interface) is
  untouched: an in-process binding still calls `WriteReceipt` with an
  in-process `Writer`. FD passing applies only to the subprocess form.
- The **receipt tree, node framing, JCS, type registry, and identity
  model** are untouched (RFC 0002 / 0003 / 0004 unchanged).
- This RFC does not add streaming/partial results: `capture.batch`
  returns once, after all its captures complete.

## Transport

### Launch and the control socket

Launch follows the magic-cookie announce/dial pattern this workspace
already standardizes on (HashiCorp go-plugin; normatively established
for madder plugins by madder RFC 0001):

1. The orchestrator generates a fresh random cookie per launch and
   `exec`s the plugin's transport subcommand (SHOULD be named
   `capture-serve`) with the environment variable
   **`CAPTURE_PLUGIN_COOKIE`** set to it. A plugin invoked without the
   cookie MUST exit non-zero with a diagnostic on stderr and MUST NOT
   write to stdout — the guard against accidental direct invocation.
2. The plugin binds an `AF_UNIX` **`SOCK_SEQPACKET`** ("unixpacket")
   rendezvous listener at a **short** path inside a fresh mode-0700
   directory (`$XDG_RUNTIME_DIR` or `/tmp`; `sun_path` is ~108 bytes, so
   a deeply nested temp dir MUST NOT be used), then prints exactly ONE
   line on stdout and nothing else:

   ```
   <cookie>|capture-plugin/v2|unixpacket|<socket-path>|<metadata>|capture-plugin
   ```

   Six `|`-separated fields: the echoed cookie, the protocol version
   token, the network, the socket path, free-form metadata (MAY be
   empty; MUST NOT contain `|` or newlines), and the fixed subprotocol
   token `capture-plugin`.
3. The orchestrator reads the plugin's FIRST stdout line under a
   bring-up deadline, validates the cookie echo and field shape, and
   dials the announced socket. ANY other first line — a log line, a
   wrong field count, a cookie or subprotocol mismatch — rejects the
   handshake: the orchestrator kills the child and treats the launch as
   a bring-up failure (see §Migration: that is the fall-back-to-v1
   signal). The plugin unlinks the socket (removes its directory) on
   exit.

`SOCK_SEQPACKET` is chosen because it is **message-oriented** (one
`sendmsg` = one `recvmsg`), so an `SCM_RIGHTS` FD is unambiguously
associated with exactly the JSON-RPC message it accompanies. `SOCK_STREAM`
would require a length-prefix framing *and* careful association of
ancillary data with a byte offset; SEQPACKET removes both problems. The
tradeoff is a per-message size bound (the socket send buffer); control
messages are small (see §Message size).

After the announce line, stdin/stdout/stderr are NOT part of the
protocol. stderr MAY carry human-readable diagnostics at any time;
stdout MUST carry nothing but the single announce line. stdin is a
lifecycle signal: the plugin MUST treat stdin EOF as a shutdown request
(equivalent to the `shutdown` notification).

> Design note: an earlier draft passed an inherited `socketpair` end via
> `CAPTURE_PLUGIN_CONTROL_FD`. The announce/dial revision replaces it —
> same SEQPACKET data path, but the launch matches the established
> go-plugin/madder-RFC-0001 shape (cookie guard, stdout-pollution
> rejection, version visible in the announce), costs one filesystem
> rendezvous (see §Security), and keeps the child's fd table free of
> magic inherited descriptors.

### JSON-RPC 2.0, peer-to-peer

Both peers send and receive JSON-RPC 2.0 requests, responses, and
notifications. Exactly **one JSON-RPC message per SEQPACKET datagram**,
encoded as UTF-8 JSON with no trailing framing. Batching (JSON-RPC arrays)
is NOT used. `id`s are per-sender; a peer MUST NOT assume the other peer's
id space.

Direction of each method:

| Method                | Caller       | Callee       | Kind         |
|-----------------------|--------------|--------------|--------------|
| `initialize`          | orchestrator | plugin       | request      |
| `capture.batch`       | orchestrator | plugin       | request      |
| `shutdown`            | orchestrator | plugin       | notification |
| `blob.begin`          | plugin       | orchestrator | request      |
| `blob.finish`         | plugin       | orchestrator | request      |

The plugin is therefore a JSON-RPC **server** for `initialize` /
`capture.batch` / `shutdown` and a **client** for `blob.begin` /
`blob.finish`, concurrently, over the one socket.

### Handshake — `initialize`

The orchestrator MUST send `initialize` first and await its response
before any `capture.batch`.

`initialize` params:

```json
{
  "protocol_versions": ["capture-plugin/v2"],
  "features": { "blob_concurrency": 1 }
}
```

`initialize` result:

```json
{
  "schema":   "capture-plugin/v2",
  "plugin":   { "name": "chrest", "version": "1.2.3" },
  "features": { "blob_concurrency": 1 },
  "formats":  ["pdf", "text", "screenshot", "..."]
}
```

- `schema` MUST be the single version the plugin selected from
  `protocol_versions`; if none is acceptable the plugin MUST fail the
  request with error code `-32000` (`unsupported-version`).
- `features.blob_concurrency` is the max number of simultaneously-open
  blobs the peer supports; the effective limit is the min of both peers'
  advertised values. `1` (sequential) is the floor and the default.
- `formats` is advisory (the authoritative capability surface remains the
  `capabilities` blob in the receipt tree, RFC 0003).

### Batch — `capture.batch`

Params are the RFC 0002 Batch Input **minus `writer.cmd`** (the writer is
now the FD-passing channel, §Blob protocol). `schema` MUST be
`capture-plugin/v2`.

```json
{
  "schema": "capture-plugin/v2",
  "target": "https://example.com/article",
  "defaults": { "normalize": true, "plugin": { "browser": "firefox" } },
  "captures": [
    { "name": "pdf", "format": "pdf", "options": { "background": true } },
    { "name": "text", "format": "text" }
  ]
}
```

While handling `capture.batch`, the plugin assembles each capture's
merkle tree exactly as in RFC 0002/0003, obtaining a markl id for every
node via the Blob protocol below. When all captures are done, the plugin
responds with the RFC 0002 Batch Output (likewise `schema:
"capture-plugin/v2"`):

```json
{
  "schema": "capture-plugin/v2",
  "plugin": { "name": "chrest", "version": "1.2.3" },
  "errors": [],
  "captures": [
    { "name": "pdf",  "receipt": { "id": "blake2b256-…", "size": 482 } },
    { "name": "text", "error": { "kind": "fetch-failed", "message": "…" } }
  ]
}
```

Per-capture success/error semantics are unchanged from RFC 0002:
per-capture failures are `captures[].error`; only transport- or
batch-fatal conditions surface as a JSON-RPC error response to
`capture.batch`.

### Blob protocol — `blob.begin` / `blob.finish`

To write one node blob, the plugin performs a two-message exchange with
the orchestrator:

1. **`blob.begin`** (plugin → orchestrator), params `{}`. The orchestrator:
   - creates a `pipe()` → `(r, w)`;
   - sends the JSON-RPC **response** to `blob.begin` with
     `result = { "blob": <handle> }` **and** `w` attached as `SCM_RIGHTS`
     ancillary data on that same datagram;
   - closes **its** copy of `w` (so the reader sees EOF when the plugin
     closes its copy);
   - begins reading `r`, streaming bytes through a markl digester into the
     blob store.

   `handle` is an integer correlating this blob to its `blob.finish`.

2. The plugin receives the response, extracts the passed write-fd from the
   ancillary data, **writes the node's raw bytes to it, and closes it.**

3. **`blob.finish`** (plugin → orchestrator), params `{ "blob": <handle> }`.
   The orchestrator, having read `r` to EOF and committed the blob, responds
   with:

   ```json
   { "id": "blake2b256-…", "size": 12034 }
   ```

   `id` is the markl id of the bytes; `size` is the byte count.

The plugin MUST NOT have more than `min(advertised blob_concurrency)`
blobs open (begun but not finished) at once; with the default `1` it MUST
fully write+close+finish a blob before the next `blob.begin`. The
orchestrator MUST tolerate the plugin closing the write-fd before or after
sending `blob.finish` (EOF is the byte-stream terminator; `blob.finish`
is the synchronization + result point).

This pair replaces the RFC 0002 Writer Protocol entirely. The
`{id, size}` contract is preserved verbatim; only the byte path (a passed
pipe instead of a spawned process's stdin) and the result path (a JSON-RPC
response instead of the process's stdout) change.

### Cancellation and shutdown

- The orchestrator MAY abort an in-flight `capture.batch` by closing the
  control socket; the plugin MUST treat a control-socket EOF/`EPIPE` as
  cancellation, abandon work, and exit.
- `shutdown` (notification, orchestrator → plugin) requests graceful exit
  after any in-flight `capture.batch` resolves; the plugin SHOULD exit 0.
- If a blob's read end (`r`) errors or the orchestrator abandons it, the
  passed write-fd breaks; the plugin MUST surface the resulting write
  error as the capture's error.

### Message size

Control messages MUST fit one SEQPACKET datagram. `blob.*` and
`initialize` messages are small. `capture.batch` params scale with the
capture list; orchestrators SHOULD keep a batch within the socket send
buffer (Linux default `SO_SNDBUF` comfortably exceeds realistic capture
lists). A future revision MAY define a `SOCK_STREAM` + length-prefix
transport for very large control messages; it is out of scope here.

## Go mechanics (informative)

The reference implementation is cutting-garden's `capture_serve` package
(exported for out-of-tree plugins as `pkgs/capture_serve`). Orchestrator
side:

```go
// Launch: cookie → spawn → ReadAnnounce(stdout) → DialAnnounced.
sess, err := capture_serve.Launch(ctx, pluginBin, "capture-serve")
result, err := capture_serve.RunBatch(ctx, sess.Conn, dest, batch)
// dest is any capture_plugin.Writer (NewBlobStoreWriter in production);
// RunBatch serves blob.begin/blob.finish while calling
// initialize/capture.batch, then notifies shutdown.
// capture_serve.Run composes the two. On blob.begin the orchestrator:
r, w, _ := os.Pipe()
oob := syscall.UnixRights(int(w.Fd()))
uc.WriteMsgUnix(responseJSON, oob, nil)              // FD travels with this datagram
w.Close()                                            // keep only the read end
id, size := digestAndStore(r)                        // io.Copy(digester, r) → EOF
```

Plugin side (`capture-serve` subcommand bring-up):

```go
cookie, _ := capture_serve.CookieFromEnv()           // exit 1, no stdout, if unset
ln, sock, cleanup, _ := capture_serve.ListenRendezvous()
line, _ := capture_serve.AnnounceLine(cookie, capture_serve.Handshake{
    Version: capture_serve.SchemaV2,
    Network: capture_serve.HandshakeNetwork,
    Address: sock,
})
os.Stdout.WriteString(line)
conn, _ := ln.AcceptUnix()
err := capture_serve.Serve(ctx, conn, capture_serve.ServeConfig{
    Plugin: capture_serve.PluginInfo{Name: "chrest", Version: v},
    Batch:  batchFunc, // gets a capture_plugin.Writer speaking the blob protocol
})
cleanup()
```

A small JSON-RPC peer (one message per datagram, request/response
correlation by id, concurrent in/out, `SCM_RIGHTS` attach/extract) wraps
`WriteMsgUnix`/`ReadMsgUnix`. `capture_plugin.Writer` (the in-process
interface) is implemented on the plugin side by a type whose
`WriteBlob(ctx, io.Reader)` does `blob.begin` → copy into the passed fd →
close → `blob.finish` → return the id, so `WriteReceipt` is reused
unchanged. One implementation gotcha the reference hit: a clean remote
close reads as bare `io.EOF`, which dewey's errors package refuses to
wrap — build fresh errors on EOF paths.

## Migration from `capture-plugin/v1`

- The batch-input `writer.cmd` field is **removed**; the FD channel
  replaces it. Orchestrators MUST NOT send it under v2; plugins MUST
  ignore it if present.
- The plugin grows a `capture-serve` subcommand (the JSON-RPC server).
  The v1 `capture-batch` (stdin/stdout) MAY be retained for
  compatibility but is deprecated. Version selection is
  **always-try-v2-then-fall-back**: the orchestrator launches
  `capture-serve` (§Launch) first on every capture; a *bring-up
  failure* (missing subcommand, handshake rejection, announce deadline,
  failed dial) or an `initialize` refusal with `-32000`
  unsupported-version falls back to `capture-batch`. Bring-up MUST fail
  fast and unambiguously — a plugin without the subcommand exits
  nonzero in milliseconds, never hangs. Any failure AFTER a successful
  `initialize` is a real capture failure and MUST NOT silently retry on
  v1. (Reference: `capture_serve.IsFallbackSignal`.)
- `cutting-garden __write-blob` (the v1 writer subprocess) is retired
  once the fallback path is no longer exercised (chrest ships
  `capture-serve`); the orchestrator writes blobs in-process from the
  passed pipe.
- The in-process binding (git) is unaffected.
- All blob bytes, markl ids, and receipt shapes are identical across v1
  and v2 for the same input — v2 is a pure transport change, so existing
  archives and cross-form byte-equivalence conformance still hold.

## Conformance

A v2 plugin is conformant iff, for any well-formed `initialize` +
`capture.batch`:

1. it negotiates `capture-plugin/v2` and answers `capture.batch` with a
   batch output validating against RFC 0002 §Batch Output;
2. every node blob it stores is obtained via `blob.begin`/`blob.finish`,
   and the resulting receipt trees are **byte-identical** to those the
   same plugin's v1 subprocess form (or its in-process form) would produce
   for the same input;
3. it never has more than the negotiated number of blobs open at once;
4. it treats control-socket EOF as cancellation.

The byte-identity requirement ties v2 back to RFC 0002's conformance: the
transport is correct iff it is invisible in the stored bytes.

## Security

- The passed FD is a single pipe write end with no ambient authority; the
  plugin can only append bytes to one blob the orchestrator is reading.
- The control socket is a filesystem rendezvous, mitigated in layers:
  the socket lives in a fresh mode-0700 directory (only the same user
  can connect), exists only for the session's lifetime (unlinked on
  exit), and a connecting party learns its path only via the announce
  line. The per-launch magic cookie authenticates the *plugin* to the
  orchestrator (a polluted or forged announce is rejected before any
  dial); it is not a secret capability for the socket itself — same-user
  processes are inside the trust boundary, as with any `AF_UNIX`
  service.
- The rendezvous path MUST be short (`sun_path` ~108 bytes): binding
  under a deeply nested temp dir fails with `EINVAL`. Implementations
  bind under `$XDG_RUNTIME_DIR` or `/tmp`.
- `target`, `options`, and node bytes remain untrusted external data
  (RFC 0002 §Security); FD passing does not widen that surface.

## Open questions

- **Concurrency > 1.** The handle + `blob_concurrency` negotiation admits
  parallel blob writes (e.g. independent payloads), but the post-order
  receipt assembly is inherently sequential per capture; concurrency would
  only help across captures. Deferred until a workload needs it.
- **Windows / non-`SCM_RIGHTS` platforms.** This RFC is POSIX-`AF_UNIX`
  only. A Windows transport (named pipes + `DuplicateHandle`, or a
  loopback-TCP + length-prefixed bytes fallback) is a separate RFC.
- **`SOCK_STREAM` fallback** for control messages exceeding the datagram
  bound (very large capture lists) — deferred.
