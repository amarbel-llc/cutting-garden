# gitwire

A minimal client for git's smart transfer protocol (v0) — just the
`want`/`have`/`done` fetch-pack negotiation cutting-garden needs to
transfer **only the objects that differ** between a remote branch and a
set of objects already held.

It is the shared primitive behind incremental capture and object-level
diff in `internal/cutting_garden_plugin_git`: given a want oid (the live
tip) and have oids (a previously captured tip — known from a receipt
without any local objects), the server computes and sends a pack of just
the delta.

## Why hand-rolled, and the key trick

`git fetch` performs the same negotiation, but only against a local odb
that already holds the haves — which would mean seeding every captured
object before every fetch. Speaking the protocol ourselves lets us
assert `have <captured-tip>` by **oid alone**, no seeding.

The one subtlety that makes the returned pack usable standalone: do
**not** select the `thin-pack` capability. A thin pack deltas against
the haves' objects (which we don't have locally) and can't be exploded
without them; by omitting `thin-pack` the server sends a self-contained
pack, and `git unpack-objects` explodes it into a scratch odb directly.
We also omit `side-band*`, so the raw pack follows the ack pkt-lines as
a plain stream.

## Surface

- `FetchDelta(ctx, remote, want, haves, scratchGitDir)` — negotiate and
  unpack the delta into `scratchGitDir` (an already `git init`-ed repo).
- `ErrUnsupportedTransport` — returned for ssh/git\:// and scp-like
  remotes; callers fall back to a full clone.

## Files

- `pktline.go` — pkt-line read/write (4-hex length prefix; `0000` flush).
- `transport.go` — `dial` + two transports: **local** (spawn
  `git upload-pack <dir>`, duplex over stdio) and **smart-HTTP**
  (`GET info/refs` then `POST git-upload-pack`).
- `fetch.go` — capability selection, request framing
  (`want`+flush+`have`s+`done`), ack draining, and `git unpack-objects`.

## Scope / caveats

- Transports: local and http(s). SSH, `git://`, and the dumb protocol
  are not implemented (→ `ErrUnsupportedTransport`).
- v0 protocol, single negotiation round (always sends `done`
  immediately); no multi-round have/ack haggling. Sufficient because the
  caller always knows both the want and the single have up front.
- Pack parsing (delta resolution) is delegated to `git unpack-objects` —
  this package never decodes packfile bytes itself.
- Only the **local** transport is exercised by the test suite (no
  network in the sandbox); the HTTP transport is implemented to the spec
  but unverified here. Callers fall back to a full clone on any
  negotiation failure, so an HTTP edge case degrades to correctness, not
  breakage.
