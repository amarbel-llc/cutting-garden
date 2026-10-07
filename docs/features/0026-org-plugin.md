---
status: proposed
date: 2026-10-07
promotion-criteria: |
  DRAFT OUTLINE — loose on purpose; a heavier pass should flesh out every
  section marked (TODO). Promote to `experimental` once Slice 1 lands:
  `plugins/org/` reads a WebDAV-hosted directory of `.org` files and
  `cg list` / `cg mcp` traverse `root → folder → file → headline →
  child headline` with the facets below, pinned by a bats lane against an
  in-repo WebDAV test server. Read-only; the plugin never writes to the
  remote in Slice 1.
---

# Org (org-mode) plugin

## Problem Statement

A user's Org outlines (synced to a phone through Orgzly over WebDAV) are not
a cutting-garden substrate. dodder has a `haustoria_orgmode` that ingests
top-level headings as zettels (dodder FDR 0013 frames cutting-garden as the
canonical haustoria protocol and names an org plugin as a reference-set
member). This record makes Org a native, in-tree, **traversal** plugin first,
so it is browsable over `list` / `mcp` / `read_facets`; `organize` / trellis /
espalier writes follow in a later slice.

Orgzly has no code of its own here: it only syncs plain `.org` files over
WebDAV.

## Decisions taken (2026-10-07, with the user)

- **Scheme:** `org:`.
- **Tree shape:** nested outline — a headline is a container; child
  headlines are its children.
- **Source, Slice 1:** WebDAV only (the Orgzly sync path). Local-directory
  and SFTP transports are deferred. (Note: a local-fs transport is
  ~50 LOC in the dodder prior art and a natural later addition.)
- **Capture/restore/diff:** out of scope; scheme-only plugin like
  fastmail (`MustRegisterScheme`).

## Node model (TODO: flesh out)

    org://<account>/                      root (config account)
      <folder>/                           WebDAV collection
        <file>.org                        container: file preamble + headlines
          <headline>                      container: body via ReadLeaf
            <child headline> ...

- Types, horizontally versioned per FDR 0018: `cutting_garden-org-folder-v1`,
  `-file-v1`, `-headline-v1`.
- **Ids (TODO — the hard part):** use the `:ID:` property when present.
  Otherwise synthesize deterministically from file path + outline path (and
  a disambiguating ordinal for duplicate titles). Slice 1 MUST NOT write
  `:ID:` back; the dodder haustoria does (byte-splice on read), which a
  read-only slice cannot copy. Consider declaring a URI template (RFC 0018)
  and `NodeIDer` for organize later.
- A file with no headlines synthesizes a single headline (dodder prior art:
  id = file stem, title = first non-empty line).

## Parsing (TODO)

Hand-rolled, line-oriented parser inside the plugin (cutting-garden plugins
cannot import dodder `internal/`, and the dodder PEG parser is
top-level-only and skips TODO/priority/planning). Extract: level, TODO
keyword, priority cookie, title, trailing `:tag:` suffix, planning line
(SCHEDULED / DEADLINE / CLOSED), `:PROPERTIES:` drawer, body. Keep **byte
spans** per headline (body start/end, drawer insert point) from day one so
later lossless organize edits are possible. Open: `#+TODO` per-file keyword
sets, `#+FILETAGS` and tag inheritance, CRLF, large files (dodder re-reads
whole files per call; consider ETag-keyed caching).

## Facets (TODO)

Candidates: `state` (TODO keyword; a `terminal_values` set for DONE /
CANCELLED), `tags` (a `tag_set`), `priority`, `scheduled` / `deadline`
(date facets with bands like caldav's `due_band`), `file`, `folder`.
Implement `FacetDescriber` + `FacetCounter` (+ `FacetVersioner` via file
ETags).

## Config (TODO)

An `[org]` section claimed through `MustRegisterConfigSection` over the
shared `config_common.AccountsSection`: per account WebDAV url, username,
credential source (env fallback, mirroring dodder's `ORGMODE_WEBDAV_*`).
Open: how folder scoping is declared (dodder's `FolderMapping`).

## Capability posture (Slice 1)

Implements: `RootProvider`, `RootLister`, `LeafReader`, `FacetDescriber`,
`FacetCounter`, `EnrichedLister`, `ListingFieldsDescriber`. Defers:
`NodeMutator`, `FacetWriteDescriber`, tag writes, `CreationDescriber`,
`NodeIDer` — i.e. everything organize needs.

## Later slices (TODO)

1. Organize/trellis/espalier: TODO state and tags as writable dimensions,
   creation of headlines, byte-splice edits with ETag-conditional writes.
2. Local-dir and SFTP transports.
3. Archival capture (`cutting_garden-capture_receipt-org-v1`) to satisfy
   dodder FDR 0013's reference set; dodder-side consumption lives in
   dodder's lane.

## Prior art and links

- dodder `go/internal/lima/haustoria_orgmode/` (main.go, headings.go,
  transport_webdav.go), `go/internal/0/orgmode/` (hand-rolled parser),
  `go/internal/0/orgmode_peg/`. Reuse ideas, not code; ask the dodder
  session for answers about it.
- dodder `docs/features/0013-cutting-garden-haustoria.md`.
- `plugins/fastmail/` (scheme-only plugin pattern), FDR 0024, FDR 0021
  (facets), RFC 0012 (facet contract), cutting-garden-plugins(7).

## Known gaps in prior art (TODO: verify)

- No code extracts SCHEDULED/DEADLINE/priority/TODO state today; those
  facets are net-new work.
- The dodder bats lane for orgmode is skipped (SFTP server blocked by
  sandbox, dodder #118/#191), so the sync path is unverified there; we need
  our own WebDAV test server fixture.
