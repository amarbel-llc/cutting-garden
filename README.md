# cutting-garden

Filesystem-tree capture/restore CLI built on top of
[madder](https://github.com/amarbel-llc/madder)'s blob store.

## Commands

- `capture [STORE_ID | DIR]...` — walk directories (or scheme-addressed
  sources: `git:`, `caldav:`, yt-dlp URLs) and write every file as a
  content-addressed blob plus one receipt per store group.
- `restore RECEIPT_ID DEST` — materialize a receipt's tree at DEST.
- `diff RECEIPT_ID [DIR]` — compare a receipt against the filesystem;
  exits 1 on drift, `diff(1)`-style.
- `serve` — long-lived [LocalSend](https://github.com/localsend/protocol)
  receiver bound to the host's Tailscale address; every incoming
  transfer lands as a normal capture receipt
  ([FDR 0011](docs/features/0011-localsend-serve.md)).
- `failures RECEIPT_ID` — inspect a capture failure receipt: which
  entries failed, why, and whether the run was aborted
  ([FDR 0012](docs/features/0012-capture-failure-receipts.md)).
- `health` — report every registered URI-scheme plugin and the
  capabilities it implements (capture / restore / diff / protocol /
  traversal), as a table or `-format json`.

`cg` is an alias binary. Manpages and shell completions are generated
from the command metadata at build time.

## Build

```sh
nix build
```

## Test

```sh
go test ./...
```

Design docs live under `docs/{rfcs,features,plans}/`; the original
extraction design is in
[the madder repo](https://github.com/amarbel-llc/madder/blob/master/docs/plans/2026-05-10-extract-cutting-garden-design.md).
