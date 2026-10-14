---
status: experimental
date: 2026-10-07
promotion-criteria: |
  Promote to `experimental` once Slice 1 lands: `plugins/org/` reads a
  WebDAV-hosted directory of `.org` files and `cg list` / `cg mcp` traverse
  `account → folder → file → headline → child headline` with the facets
  below, pinned by a bats lane against an in-repo WebDAV test server.
  Read-only; the plugin never writes to the remote in Slice 1.
---

# Org (org-mode) plugin

## Problem Statement

The user keeps notes as Org outlines, synced to a phone by Orgzly through
Fastmail Files (WebDAV). They are not a cutting-garden substrate yet. Agents
need to read them mid-session as part of the user's knowledge graph, next to
`!task` (caldav), mail (fastmail) and forge issues (smith). This record makes
Org a native, in-tree **traversal** plugin, browsable over `list` / `mcp` /
`read_facets` / trellis queries. Writes beyond a single "quote" operation are
deferred.

Orgzly is transitional: the user intends to move to dodder, possibly via an
Orgzly fork that talks to dodder directly. Agents will later claim notes into
project tags. So the design favours dodder's semantics (hyphen tags, tag
inheritance) over Orgzly-specific machinery, and does not over-engineer id
stability. Orgzly has no code here; it only syncs plain `.org` files.

The UX design was worked out as user stories in a Google Doc on 2026-10-14
("org-mode plugin UX design (FDR 0026)"). This record holds its decisions.

## Decisions

Taken 2026-10-07:

- **Scheme:** `org:`, a scheme-only plugin like fastmail
  (`MustRegisterScheme`). Capture, restore and diff are out of scope.
- **Tree:** account → folder → `file.org` → headline → child headlines. A
  headline is a container.
- **Types**, horizontally versioned per FDR 0018:
  `cutting_garden-org-folder-v1`, `-file-v1`, `-headline-v1`.
- **Source:** WebDAV only (the Orgzly sync path). Local-directory and SFTP
  transports are deferred.

Taken 2026-10-14:

- **D1, URIs and ids.** A file is positional, `org://<account>/<path>.org`.
  A headline is `…/<file>.org?id=<:ID:>` when it has an `:ID:`. Otherwise it
  is a synthetic `…/<file>.org?h=<outline path joined by />`, with `&n=2` for
  a duplicate title. A synthetic id carries `id_synthetic: true`: it breaks
  on rename and fails as not-found, never as a closest match. This mirrors
  fastmail's `?thread=`; implement `NodeIDer`.
- **D12, quote (Slice 2).** An agent makes a reference durable with a
  dedicated `patch_node` body `{"quote": true}`. The plugin mints the `:ID:`
  (a UUID the agent does not choose) and adds the own tag `zz-agent_quoted`.
  Quoting is idempotent, and a headline that already has an `:ID:` only
  gains the tag. The intent is deliberately narrow, which suits scoped agent
  access (#303).
- **D11, tags.** The `tags` field declares `dodder-hyphen` as its default
  interpreter (RFC 0019), matching the user's `!task` categories
  (`project-reno-phase_1`, `area-home`, `zz-inbox`). Agents query tags with
  bare terms, which go through the interpreter and match the whole subtree.
  A facet predicate (`tags=x`) is exact membership, and `tags!=x` is not
  "lacks x": use `^x` or `^tags=x`. Hyphenated tags are not legal Emacs org
  syntax, but Orgzly accepts them. This decision exposed a host split: a
  module-managed host's caldav was `naive` while a hand-configured one was
  `dodder-hyphen`. #304 added the `tags.interpreter` module option to fix it
  (per-account: #305).
- **D4, inheritance.** `tags` is the effective set: own tags, plus
  ancestors', plus `#+FILETAGS`, as in dodder. `own_tags` is a separate
  field. `zz-agent_quoted` is never inherited.
- **D5, read_node.** Returns the parsed fields (title, state, priority,
  tags, own_tags, scheduled/deadline/closed in ISO form, properties, level,
  outline_path, file, id_synthetic), the own body (text before the first
  child), and a madder blob link to the verbatim subtree. Logbook drawers
  appear only in the blob.
- **D3, TODO keywords.** Orgzly keeps its keywords in app settings, not in
  files. A per-account `todo_keywords` (e.g. `"TODO NEXT | DONE CANCELLED"`)
  is the default, and a file's `#+TODO` overrides it. Words after `|` are
  terminal values. A headline with no keyword has `state: none`.
- **D6, dates.** `scheduled_band` and `deadline_band` (overdue / today /
  this-week / later / none, like caldav's `due_band`), plus a `closed` date
  facet. Active body timestamps are not dates (deferred).
- **D10, text search.** Trellis `title~=` / `_body~=` now; RFC 0016 search
  when it lands.
- **D9, freshness.** An in-process cache keyed by ETag. One PROPFIND per
  folder finds changed files; only those are re-fetched and re-parsed.
  Listings carry a `version` derived from the ETags.
- **D2, scope.** One base WebDAV URL per account. Every `*.org` file beneath
  it is exposed, and subdirectories become folder nodes. Other files are
  skipped, and the listing reports how many.
- **Deferred (D7, D8):** agent capture and any other writes, and the
  conflict policy against Orgzly. Orgzly uploads unconditionally, and
  Fastmail Files ignores conditional headers but enforces LOCK (#267).
  Writes into files the user edits on the phone need a real-device test of
  Orgzly's conflict behaviour first.

## Facets

On the headline type: `state` (terminal values from the keyword set, D3),
`tags` (`tag_set`, `dodder-hyphen`, effective set, D4/D11), `priority`
(A/B/C/none), `scheduled_band`, `deadline_band`, `closed`, `file`.

## Config

An `[org]` section claimed through `MustRegisterConfigSection`. Each account:

    [[org.accounts]]
    name = "personal"
    url = "https://myfiles.fastmail.com/Library/orgzly/"
    username = "…"
    password_source = "piggy"
    password_key = "cutting-garden/fastmail-files"
    todo_keywords = "TODO NEXT | DONE CANCELLED"

`url` is the WebDAV folder (it names the server); the account's root is
`org://<name>/`. `todo_keywords` is a field on the shared
`config_common.Account`, following the `session_url` precedent of a
plugin-specific field there; other plugins ignore it.

## Parsing

A hand-rolled, line-oriented parser inside the plugin. Plugins cannot import
dodder `internal/`, and the dodder PEG parser is top-level-only. Per
headline it extracts level, TODO keyword, priority cookie, title, tags (any
non-space, non-colon run between colons), the planning line, the
`:PROPERTIES:` drawer and the body. It keeps byte spans (headline line,
planning, drawer, body, subtree end) from day one, so Slice 2 can splice
without reformatting. It handles `#+TODO`, `#+FILETAGS` and CRLF. A file
with no headlines yields a single synthesized node (id = file stem, title =
first non-empty line).

## Failure behaviour

- Missing credentials fail that root with the failing source named; other
  roots still list.
- An unparseable line is reported as a warning naming file and line, in
  `read_node` of the file or any of its headlines; no headline is silently
  dropped.
- A stale headline reference (renamed, moved) fails as a bad request naming
  the file, never as a closest match.
- In a folder-wide `read_facets`, an unreadable file is skipped and the
  summary is marked incomplete (`complete: false`); listing a folder never
  fetches its files, so it is unaffected.

## Capability posture

Slice 1 implements `RootProvider`, `RootNamer` (so `!<account>` works as an
organize origin later), `RootLister`, `LeafReader`, `FacetDescriber`,
`FacetCounter`, `FacetVersioner` (a file's ETag; folders decline),
`ListingFieldsDescriber`, `UnifiedDescriber` and `NodeIDer`. ListRoots
already populates Facets and Fields (the file is parsed anyway), so there
is no separate `EnrichedLister`; the host filters. Trellis queries must
start with a step matching the anchor's children (a leading `->>` is not
supported yet, #211), and a regex containing `(` is quoted:
`[!cutting_garden-org-file-v1, !cutting_garden-org-folder-v1] ->>
!cutting_garden-org-headline-v1 title~="(?i)leak"`.
Slice 2 adds the quote mutation only. Everything organize needs for editing
(`FacetWriteDescriber`, tag writes, `CreationDescriber`) is deferred.

Slice 1 does not depend on the RFC 0020 organize grammar work. swift-plum
verified on 2026-10-14 that it touches neither trellis_eval nor the MCP
server.

## Slices

1. **Read-only traversal:** parser, WebDAV test server, plugin, bats lane.
2. **Quote** (D12): LOCK → GET → splice → PUT with the lock token → UNLOCK
   (#267). The test server gains LOCK.
3. Later: capture and edits (D7/D8), organize writes, local-dir/SFTP
   transports, archival capture (dodder FDR 0013's reference set).

## Prior art and links

- dodder `go/internal/lima/haustoria_orgmode/`, `go/internal/0/orgmode/`,
  `docs/features/0013-cutting-garden-haustoria.md`. Reuse ideas, not code.
- circus pebble tools (FDR 0027): append to `_inbox.org` over WebDAV with
  LOCK, writing level-1 headlines tagged `:zz-from_pebble:zz-inbox:` with
  `:CREATED:`. Their retry and outcome-unknown rules are the model for
  Slice 2.
- `plugins/fastmail/` (scheme-only, NodeIDer), `plugins/caldav/client.go`
  (DAV client), FDR 0021, RFC 0012, RFC 0019, cutting-garden-plugins(7).
- cutting-garden#267 (LOCK requirement), #54 (generic webdav), #303 (scoped
  agent access), #304 / #305 (tag interpreter config).
