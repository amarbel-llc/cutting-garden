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
  dedicated patch body `{"quote": true}` (a `mutate_nodes` patch op since
  #315; `patch_node` before). The plugin mints the `:ID:`
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
  folder finds changed files; only those are re-fetched and re-parsed, and
  a single file is checked with a Depth-0 PROPFIND before any GET. A file
  or headline's facet version is the file's ETag; a folder declines one.
  A confirmed ETag is trusted for 5 s without asking again (2026-10-15):
  a trellis walk with `_body~=` lists and reads every headline, and with a
  PROPFIND each it took over two minutes on about 300 headlines. Orgzly
  syncs every few minutes at best, so the staleness is invisible; a quote
  drops the entry it wrote.
- **D2, scope.** One base WebDAV URL per account. Every `*.org` file beneath
  it is exposed, and subdirectories become folder nodes. Other files are
  skipped by design.
- **D7, agent writes:** metadata edits (D7-B), body edits (D7-C), moves
  (D7-D) and capture (D7-E) were taken 2026-10-15 and 2026-10-18 (below).

Taken 2026-10-15, for metadata edits (slice 3):

- **D7-B, edits.** A patch on a headline (a `mutate_nodes` patch op, e.g.
  id `_inbox.org?id=…` under the account anchor) sets `title`, `state`,
  `priority`, `own_tags` (the whole own set), `scheduled` and `deadline`
  (`YYYY-MM-DD` with an optional ` HH:MM`; `null` clears), alongside
  `quote`. The body was left alone until D7-C. Slice 3a is the patch; slice 3b
  declares the same fields to organize (facet writes, tag atoms).
- **D8-C, conflict policy.** Edits land in files the user also edits in
  Orgzly, which uploads without locking (#267). Each edit takes the quote's
  LOCK path, then reads the file back after unlocking and again after 2 s.
  If the edit is gone, the call fails with `lost_to_concurrent_sync` rather
  than reporting success. This detects the race; it cannot prevent it. A
  client that honours the lock is explored in #314 ("shears").
- **D13, ids.** Any edit mints an `:ID:` when the headline has none (or a
  shadowed copy's), without the `zz-agent_quoted` tag, so a retitled
  headline keeps a durable `?id=` URI. A child's `?h=` path does change
  when its parent is retitled.
- **D14, CLOSED.** Entering a done keyword adds `CLOSED: [now]` to the
  planning line (first, as org does); leaving one removes it.

Taken 2026-10-18, for body edits and moves (slice 4; examples in the
Google Doc "org plugin: body edits, by example (FDR 0026 D7)"):

- **D7-C, body.** The `body` patch key replaces the headline's own text,
  exactly what `read_node` returns as `body`; `null` clears it. Drawers in
  the body (`:LOGBOOK:` and the like) are kept byte for byte, gathered above
  the text (where Orgzly writes them), so a drawer between two paragraphs
  moves above both. The planning line, the property drawer and the children
  are untouched. The text keeps the old text's indentation and must read
  back exactly as sent: a line that would start a headline, a drawer, or a
  `#+TODO` / `#+FILETAGS` line is refused, not escaped.
- **D7-D, moves.** One of the patch keys `parent`, `before` or `after`
  moves a headline with its subtree. `parent` is a headline (last child) or
  a file (level 1, at its end); `before` / `after` a sibling headline, at
  its level. A reference is a full `org://` URI, a path from the account
  root (`notes.org?id=…`), or a bare `id=…` / `h=…` in the same file. The
  subtree's levels shift to fit; own tags travel, inherited ones are the new
  place's. Moves may cross files: both files are locked (in path order), the
  destination is written first and the source second, so a failure between
  them leaves the headline in both files (reported as such) rather than in
  neither. The read-back (D8-C) checks the destination, and the source
  across files; a lost cross-file move quotes the moved text back in the
  error. Metadata and body keys in the same patch apply first. A keyword the
  destination file does not define is refused.
- **D7-E, capture.** A headline is created through `ContainerCreator`: a
  `mutate_nodes` create with a temp id and `in` set to a file (a level-1
  headline at its end) or a headline (its last child). The body is JSON: a
  required `title` plus any of the D7-B/D7-C keys. The plugin mints the
  `:ID:` (the create returns the `?id=` URI), stamps `:CREATED:  [now]`
  (the host's local time), and adds the own tag `zz-agent_captured`, which,
  like `zz-agent_quoted`, never inherits. Creating at a caller-chosen URI is
  refused (the headline type is `ServerAssignedIdentity`). Same write path
  and read-back as edits.

Round-trip tested against Orgzly on 2026-10-18 in the dedicated notebook
`zz-tests-cutting_garden.org` (and `_mirror.org` for the cross-file move):
a metadata edit, a body edit, a same-file move, a move to `_mirror.org`
and back (carrying a child added in Orgzly meanwhile) each showed in Orgzly
and survived Orgzly's next whole-file upload. Orgzly writes a blank line
between a property drawer and the body text; a later agent body edit drops
it and Orgzly restores it, a cosmetic flip-flop only.

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
with no headlines yields a single synthesized node, titled by its first
non-empty line (else the file stem) and addressed like any headline by that
title. A copy-pasted headline that repeats another's `:ID:` is addressed by
its outline path, so both stay reachable.

Known gap: the `state` dimension's terminal values come from the accounts'
`todo_keywords`, because a facet declaration is static. A file's own
`#+TODO` done words (e.g. `DROPPED`) are treated as done for the date bands
but are not terminal for organize's default exclusion.

## Failure behaviour

- Missing credentials fail that root with the failing source named; other
  roots still list.
- An unparseable line is reported as a warning naming file and line, in
  `read_node` of the file or any of its headlines; no headline is silently
  dropped.
- A stale headline reference (renamed, moved) fails as a bad request naming
  the file, never as a closest match.
- In a folder-wide `read_facets`, an unreadable file or subfolder is
  skipped and the summary is marked incomplete (`complete: false`); a 401 or
  403 still fails the call, so bad credentials never pass as an empty
  summary. Listing a folder never fetches its files, so it is unaffected.
- A URI with a `.` or `..` segment is refused, so no path escapes the
  account's folder.

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
Slice 2 adds `NodeMutator` (only `PatchNode`, first with `{"quote": true}`
and from slice 3a with the metadata fields, from slice 4 with `body` and the
move keys; create/put/delete are bad requests; slice 5 adds
`ContainerCreator` for capture) and `BodyDescriber` (so describe_node_types marks the headline
writable and lists the patch keys).
`PatchNode` can only report applied field names, not a new URI, so the
agent reads the node again: the old `?h=` URI still resolves, its fields
now carry `id`, and later listings mint the `?id=` URI. A re-quote changes
nothing and reports nothing applied, which the mcp `mutate_nodes` tool
lists under `patched_nothing` (an error result when nothing else in the
batch applied). Slice 3b adds `FacetWriteDescriber`,
`FacetWriteApplier`, `MembershipWriteApplier`, `FieldWriteApplier` and
`FieldPresenter`, all derived from the unified declaration; creating
headlines (`CreationDescriber`) is deferred. The plugin is also the
reference `Searcher` (RFC 0016): it searches exactly what `_body` reads (a
file's bytes, a headline's whole subtree) from the parse cache, so a
`_body*=` query over the account costs one PROPFIND per folder instead of
one per headline.

Slice 1 does not depend on the RFC 0020 organize grammar work. swift-plum
verified on 2026-10-14 that it touches neither trellis_eval nor the MCP
server.

## Slices

1. **Read-only traversal:** parser, WebDAV test server, plugin, bats lane.
2. **Quote** (D12, landed): LOCK (60 s timeout; on 423, 5 retries sleeping
   1–5 s) → fresh GET → splice the `:ID:` drawer line and the tag, keeping
   the file's line endings → PUT with the lock token → UNLOCK on every
   path (#267). Each request has a 25 s timeout, so GET + PUT fit in the
   lock. A 5xx or a lost response is "outcome unknown": still holding the
   lock, the plugin re-reads the file and reports success only if its bytes
   landed; it never retries blindly. A copy-pasted duplicate `:ID:`, or an
   empty one, is replaced rather than duplicated, and the splice keeps the
   headline's own line ending. The test server enforces LOCK and, like Fastmail Files,
   ignores conditional headers. Verified against a real device on
   2026-10-18: a quoted headline kept its `:ID:` and `zz-agent_quoted` tag
   through later Orgzly syncs of the same file, and Orgzly shows the tag.
3. **Metadata edits.** 3a (landed): patch edits (D7-B, D8-C, D13,
   D14), sharing the quote's write path. The headline line is rebuilt only
   when its keyword, priority or title changes (a tags-only edit replaces
   just the tag block, keeping the line's spacing) and must parse back to
   exactly what was asked for, so a title cannot begin with a keyword or
   end in `:tags:` where that would be ambiguous. Planning entries are
   edited in place; a rescheduled date keeps its repeater and warning
   cookies, and a new planning line is written unindented. 3b (landed):
   organize writes for the same fields, declared once in the unified
   codecs. State, priority and the dates are headings (a move writes them;
   `none`, or the no-value section for a date, clears); priority and the
   dates are also box atoms, the title is the writable trailer, and the
   tag atoms write the own tags: organize hands over the new effective
   set, and dropping an inherited tag is refused, naming it. A month or
   year date bucket keeps the day of month (clamped) and the time of day.
   Organize anchors the document at the file, because headlines are
   addressed by query (`<file>.org?id=…`) rather than by path.
4. **Body edits and moves** (D7-C, D7-D, landed), as patch keys on the
   same write path; round-trip tested.
5. **Capture** (D7-E): `CreateChild` on a file or headline.
6. Later: creation through organize (`CreationDescriber`), local-dir/SFTP
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
