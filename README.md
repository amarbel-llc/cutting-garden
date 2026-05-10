# cutting-garden

Filesystem-tree capture/restore CLI built on top of
[madder](https://github.com/amarbel-llc/madder)'s blob store.

## Status

Phase 1 — framework bootstrap. No commands implemented yet. See
[the extraction design](https://github.com/amarbel-llc/madder/blob/master/docs/plans/2026-05-10-extract-cutting-garden-design.md)
in the madder repo for context.

## Build

```sh
nix build
```

## Test

```sh
go test ./...
```
