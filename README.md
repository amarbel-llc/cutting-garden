# cutting-garden

A **capture / restore / diff / traverse** framework for content-addressed
snapshots of arbitrary trees — filesystems, calendars, git repositories,
media, optical discs, issue trackers — built on
[madder](https://code.linenisgreat.com/madder)'s blob store.

Every backend is a **URI-scheme plugin**. `capture`, `restore`, and `diff`
snapshot a source into content-addressed blobs and reconstruct it; `list`
and `mcp` walk a live tree one level at a time. Plugins consume a public
SDK (`pkgs/`) and are resolved by scheme, so a backend can live out of
tree — or even out of process, over a JSON-RPC wire protocol — without
cutting-garden knowing the difference.

## Commands

The `cutting-garden` binary (aliased `cg`) has nine user-facing
subcommands:

- `capture [STORE_ID | URI]...` — snapshot directories or scheme-addressed
  sources (`git:`, `caldav:`, yt-dlp URLs, …) into content-addressed blobs
  plus one receipt per store group.
- `restore RECEIPT_ID DEST` — materialize a receipt's tree at DEST.
- `diff RECEIPT_ID [DIR]` — compare a receipt against the current state;
  exits 1 on drift, `diff(1)`-style.
- `list [URI]` — list the child nodes a traversable plugin exposes for a
  URI (a CalDAV endpoint's calendars, then a calendar's events). With no
  URI, aggregate every configured plugin root. `--facets` adds
  grouped-count summaries (by status, year, domain, …). Table or
  `-format json`.
- `mcp` — serve the same plugin trees over the
  [Model Context Protocol](https://modelcontextprotocol.io) so an agent
  can browse them as resources — with facet summaries and node-type
  self-description — and create / update / delete nodes where a plugin
  supports it ([FDR 0015](docs/features/0015-mcp-resource-server.md),
  [FDR 0020](docs/features/0020-cud-tree-modifications.md)).
- `serve` — long-lived [LocalSend](https://github.com/localsend/protocol)
  receiver bound to the host's Tailscale address; every incoming transfer
  lands as a normal capture receipt
  ([FDR 0011](docs/features/0011-localsend-serve.md)).
- `failures RECEIPT_ID` — inspect a capture failure receipt: which entries
  failed, why, and whether the run was aborted
  ([FDR 0012](docs/features/0012-capture-failure-receipts.md)).
- `health` — report every registered plugin and the capabilities it
  implements (capture / restore / diff / protocol / traversal). Table or
  `-format json`.
- `version` — print the build version.

Manpages and shell completions are generated from command metadata at
build time; `cg` is a thin alias binary.

## Plugins & configuration

Capture/restore/diff/traversal backends are keyed by URI scheme and live
in `plugins/<scheme>/`: **file, git, yt-dlp, caldav, optical, gphotos,
jira, fastmail** (the last read-only/traversal-only, JMAP; FDR 0024). Each
consumes only the public plugin SDK under `pkgs/`
([RFC 0009](docs/rfcs/0009-cutting-garden-plugin-sdk.md)) — none reach into
`internal/` — so an out-of-tree plugin in its own repo bridges the exact
same surface. Traversal plugins may also run **out of process** over a
JSON-RPC line protocol
([RFC 0013](docs/rfcs/0013-traversal-plugin-jsonrpc-transport.md)),
declared in config and indistinguishable to callers from a linked plugin.

Per-plugin roots and named accounts (e.g. CalDAV calendars) live in
`$XDG_CONFIG_HOME/cutting-garden/config.toml`
([RFC 0007](docs/rfcs/0007-config-subsystem.md)); `list` and `mcp` with no
URI enumerate them.

## Query & editing (in development)

- **trellis** ([RFC 0014](docs/rfcs/0014-trellis-query-language.md)) — a
  query language over plugin trees: predicate matching + typed-arrow
  traversal + field/body predicates, with an isometric ground data form
  (espalier). The normative grammar (`docs/rfcs/0014-trellis.peg`) and a
  recursive-descent parser have landed; wiring into `list`/`mcp` is next.
- **organize** ([RFC 0015](docs/rfcs/0015-organize-dialect.md)) —
  round-trip facet editing: query → grouped text document → edit →
  write-through, generalized from dodder's organize. In design.

## Build

```sh
nix build            # -> result/bin/cutting-garden (+ cg, manpages, completions)
```

The flake ships a devshell (`go_1_26`, `gopls`, `gomod2nix`) and a `just`
task runner. `just` (build + test + analyzers + format/lint checks) is the
full gate.

## Test

```sh
go test ./...        # unit + integration suite (no external deps)
just test            # the full gate (adds bats, dewey analyzers, format/lint)
```

## Design docs

Under `docs/{rfcs,features,plans}/`. The original extraction design lives
in [the madder repo](https://code.linenisgreat.com/madder/src/branch/master/docs/plans/2026-05-10-extract-cutting-garden-design.md).
