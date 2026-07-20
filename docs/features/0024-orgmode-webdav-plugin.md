---
status: exploring
date: 2026-07-20
promotion-criteria: |
  Promote to `proposed` once the node-addressing scheme (how a heading
  inside a shared `.org` file is named and re-resolved) and the
  read-modify-write mutation model (whole-file conditional PUT with
  optimistic-concurrency retry) are settled against a concrete parser
  choice — i.e. once the two hard questions this record opens (sub-file
  addressing, RMW fidelity) have one answer each, exercised on a
  throwaway prototype against a real WebDAV server (a self-hosted
  Nextcloud/`nginx dav`/Radicale-with-files). Promote to `experimental`
  once `plugins/org/` lands with `RootLister` + `LeafReader` +
  `FacetCounter` and a single `todo→done` `patch_node` round-trip runs
  end-to-end through `cutting-garden mcp` under the #102 permission gate.
  Promote to `accepted` after a capture→restore cycle reproduces a
  collection of `.org` files byte-for-byte on a fresh server AND the
  addressing/RMW levers have gone two weeks without moving.
---

# Org-mode-over-WebDAV plugin

> **Exploring / pre-proposal.** This record explores a cutting-garden
> plugin that exposes `.org` files living on a WebDAV share as a
> **structured, mutable outline tree** — headings as addressable nodes an
> agent can list, read, facet, and edit — on top of a **byte-faithful
> capture/restore/diff** backend for the raw files. It is a design
> exploration, not a plan: the two genuinely new problems (§Sub-file
> addressing, §Mutation) are surfaced with a recommended answer each, but
> the interface is not yet committed.

## Problem Statement

cutting-garden already treats a remote tree-behind-a-URL as a first-class
capturable/traversable/mutable thing: caldav (FDR 0013, RFC 0011) is the
worked example — a CalDAV endpoint's calendars and objects are captured as
verbatim `text/calendar` blobs (the read/snapshot axis) *and* surfaced as a
structured, faceted, mutable node tree (`RootLister` + `LeafReader` +
`FacetCounter` + `NodeMutator`, FDR 0014/0020/RFC 0012). The live MCP
server already carries seven such schemes (`file`, `caldav`, `fj`, `git`,
`ytdlp`, `jira`, `newsblur`).

Org-mode is the same shape one layer down. A person's `.org` files —
`inbox.org`, `projects.org`, `notes/*.org` — are the canonical
plain-text home of their tasks, notes, and outlines, and they are
routinely synced over **WebDAV** (Nextcloud, a bare `nginx`/`Apache`
`dav` module, `webdav`-backed Syncthing alternatives, `davfs2` mounts).
Each file is a tree of **headings**, each heading a small structured
record: a TODO keyword, a priority, tags, `SCHEDULED`/`DEADLINE`
timestamps, a `:PROPERTIES:` drawer, and free-text body. That is exactly
the "structured leaf + faceted container" model cutting-garden's plugin
SDK is built around — an org agenda is, structurally, a caldav task list
that happens to live inside flat files.

A cutting-garden org plugin would let an agent (or the `list`/`mcp`
surfaces) **browse** a WebDAV org store as an outline, **facet** it
("show me every `NEXT` with a deadline this week across all projects"),
**read** a heading's structured fields, and **mutate** it (flip a TODO to
`DONE`, reschedule a deadline, add a tag, file a new capture under
`inbox.org`) — while the capture/restore/diff axis snapshots and restores
the raw `.org` bytes with the same content-addressed fidelity caldav
gives `.ics`.

### Why this is not just "caldav for files"

caldav is the closest prior art, and the plugin should mirror its
structure (scheme claim, account config, WebDAV/HTTP client, the
capture=opaque-blob / traversal=structured split). But org-over-WebDAV
introduces **one structural inversion caldav never faces**, and everything
hard about this plugin flows from it:

> **In caldav, one WebDAV resource is one node.** A `.ics` at its own
> href *is* the VEVENT; listing a calendar is a `REPORT`; mutating an
> object is a conditional `PUT` to that object's own URL.
>
> **In org, one WebDAV resource is a whole tree of nodes.** A single
> `projects.org` blob contains dozens of headings at many depths. The
> plugin's node granularity (a heading) is **finer than the transport
> resource** (the file). There is no href for "the third subtask under
> Project Foo."

This inversion is the spine of the design. It drives §Sub-file
addressing (a heading needs a name the WebDAV layer doesn't give it) and
§Mutation (editing a heading is a read-modify-write of its *whole file*,
not an independent `PUT`). The good news: the SDK already has precedent
for nested, sub-resource trees — `newsblur` exposes `folder → feed →
story → story-content` and `fj` exposes `repo → issue → comment`, both as
nested containers where a node is finer-grained than one API call. Org is
the first where the sub-tree lives **inside one opaque file**, but the
node-shape it produces is familiar to consumers.

## Interface

### Scheme and accepted argument forms

Claim the single `org` scheme, in the two shapes caldav uses (url.go
precedent) so a plain-HTTP LAN server is reachable:

- hierarchical `org://[user[:pass]@]host[:port]/path` → `https://…` (TLS
  assumed; the common form). `path` addresses a WebDAV **collection** (a
  directory of `.org` files) or a single `.org` file.
- opaque `org:<http(s)-url>` — the inner URL verbatim, the only form that
  reaches plain HTTP (`org:http://10.0.0.2:8080/dav/org/`).

Like caldav, claim **no** bare `https` host: a WebDAV org share is
indistinguishable from any other https URL, so it is opted into
explicitly.

### Node types

Four node types, hyphenated + horizontally versioned (issue #79), mirroring
the caldav two-type shape but with the file/heading nesting:

| Tag | Container | Writable | Children / body |
|---|---|---|---|
| `org-collection-v1` | yes | no | child `.org` files + sub-collections (a WebDAV directory) |
| `org-file-v1` | yes | no¹ | the file's **top-level headings** (leaf-readable → its preamble: `#+TITLE:`, `#+TODO:`, file tags) |
| `org-heading-v1` | yes | **yes** | its **sub-headings** (leaf-readable → the heading's own structured fields + body) |
| `org-heading-body-v1` | no | yes | the heading's free-text body only (a projection child, see §The heading duality) |

¹ file-level create/delete (add/remove a whole `.org` file) is a
`NodeMutator` on `org-collection-v1`'s children, deferred; the v1 write
surface is heading-level.

`org-file-v1` and `org-heading-v1` are **container and leaf at once** — a
container of what's beneath, leaf-readable for their own content. This is
legal under the SDK: `LeafReader.ReadLeaf` "is consulted only after
`ListRoots` reports a node has no children," so a childless heading falls
through to `ReadLeaf` automatically. The heading-*with*-children case is
§The heading duality below.

`leafMimeType` for the text projections is `text/plain; charset=utf-8`
(there is no registered `text/org`; `x-org` would be non-standard — state
`text/plain` and move on).

### Traversal (`RootLister` + `RootProvider`)

Descent is one level per call, exactly as `resources/list` wants:

```
org://host/dav/org/           (org-collection-v1)
  ├─ inbox.org                (org-file-v1)
  ├─ projects.org             (org-file-v1)
  │    ├─ * Cutting Garden    (org-heading-v1, TODO=PROJ, tags=[work])
  │    │    ├─ ** NEXT Write the org FDR   (org-heading-v1, leaf)
  │    │    └─ ** TODO Prototype WebDAV client (org-heading-v1, leaf)
  │    └─ * Reading list      (org-heading-v1)
  └─ notes/                   (org-collection-v1)
```

- `org-collection-v1` children: a **PROPFIND Depth:1** on the collection,
  filtered to members whose `getcontenttype`/name ends in `.org` (leaves)
  plus sub-collections (`resourcetype` = `collection`). Reuses caldav's
  `multistatusResponse`/`davResponse` XML shape — the WebDAV envelope is
  identical; only the CalDAV `REPORT`/`calendar-data` specialization drops
  away.
- `org-file-v1` children: **GET the file once**, parse its outline, return
  its top-level (`*`) headings.
- `org-heading-v1` children: its immediate deeper-level sub-headings, from
  the same parse of the enclosing file. (A file is parsed once per descent
  request and its heading tree walked in-process — no per-heading fetch.)

`RootProvider.Roots` returns each configured account's collection URL,
credential-stripped, exactly as caldav does — so a no-argument
`list`/`mcp` aggregates every org account beside the caldav/file roots.

### Reading a heading (`LeafReader`) — the `objectView` analog

`ReadLeaf` on an `org-heading-v1` returns a structured projection (the org
counterpart of caldav's `objectView`) plus the verbatim subtree bytes:

```jsonc
// read_node("org://host/dav/org/projects.org#id=a1b2…")
{
  "structured": {
    "title":     "Write the org FDR",
    "todo":      "NEXT",            // the raw keyword as configured in-file
    "todo_state":"todo",           // derived: todo | done | none (see facets)
    "priority":  "A",              // #A / #B / #C, or absent
    "tags":      ["work", "writing"],
    "scheduled": "2026-07-21",
    "deadline":  "2026-07-25",
    "closed":    null,
    "level":     2,
    "properties": { "ID": "a1b2…", "Effort": "2h" },
    "body":      "Cover the addressing inversion and the RMW model.\n"
  },
  "raw": "** NEXT [#A] Write the org FDR :work:writing:\n..."   // + madder://blobs/<digest>
}
```

`raw` is the exact bytes of the heading's subtree (its headline line
through the byte before the next same-or-higher-level `*`), so a client
can round-trip it and a blob link is content-addressable — same contract
as caldav's `.ics` bytes.

### The heading duality (a heading is a container *and* a record)

The one place the read model strains: a heading with sub-headings is a
**container** (so `read_node` on it lists its children, per the framework
contract) yet it *also* has its own TODO/body. Three ways to resolve it,
in order of preference:

1. **Enrich the listing entry (primary).** Implement `EnrichedLister`
   (caldav's `ListEnriched` precedent): every heading's scalar fields
   (`title`, `todo`, `priority`, `tags`, `scheduled`, `deadline`) ride
   **inline on its `Node` entry** when its parent is listed — via
   `Node.Fields` + `Node.Facets`. So browsing `projects.org` shows each
   project heading's TODO/tags/deadline without a second read, and the
   childless-heading case still `ReadLeaf`s the full record. This covers
   the overwhelming majority of use (an agent facets/scans; it rarely
   needs the free-text body of an *interior* heading).
2. **Body projection child (secondary).** For the residual case — reading
   an interior heading's free-text body when it also has sub-headings —
   give it an `org-heading-body-v1` **leaf child** carrying just the body,
   exactly as `newsblur-story-v1` (a container) exposes its text via
   `newsblur-story-content-v1` leaf children. Uniform, precedented,
   opt-in per-heading (emit the child only when the body is non-empty).
3. **Reject and reparent (rejected).** Flattening every heading to a leaf
   and hoisting sub-headings to siblings loses the outline — a non-starter
   for org.

Recommendation: ship (1) always; add (2) only if a consumer actually needs
interior-heading bodies (defer until asked — a tuning lever).

### Facets (`FacetDescriber` + `FacetCounter` + `FacetVersioner`)

Org is *richer* for faceting than caldav — this is the plugin's strongest
argument. Declared on `org-heading-v1`:

| Dimension | Kind | Closed? | Source |
|---|---|---|---|
| `todo_state` | categorical | **closed**: `todo` / `done` / `none` | the file's `#+TODO:` active/done partition |
| `todo` | categorical | open | the raw keyword (`NEXT`, `WAITING`, …) — file-defined, so **not** closed |
| `priority` | categorical | open (usually A/B/C) | `[#A]` cookie |
| `tag` | categorical, **multi** | open | headline `:tags:` (inherited from ancestors + file tags) |
| `deadline_band` | numeric-bucket | **closed**: `overdue`/`today`/`this-week`/`later` | `DEADLINE`, agenda-style — a direct lift of caldav's `due_band` (volatile, `RevalidateAfter`) |
| `scheduled_band` | numeric-bucket | closed, same domain | `SCHEDULED` |
| `level` | numeric-bucket | open | heading depth (star count) |
| `archived` | categorical | **closed**: `yes`/`no` | the `ARCHIVE` tag / `:ARCHIVE:` subtree |

**The one org-specific wrinkle:** TODO keywords are **configured
per-file** (`#+TODO: TODO NEXT WAITING | DONE CANCELLED` — the `|`
separates active from done states). So `todo` is an *open* dimension
(the plugin cannot enumerate a closed domain across files), while
`todo_state` — the active/done fold every file's own `#+TODO:` line
defines — *is* the stable closed dimension a consumer filters on. caldav's
`status` is a fixed vocabulary; org's is data. `FacetCounter` must read
each file's `#+TODO:` before folding its headings.

`FacetCounter` is one-shot and size-agnostic like caldav's: **GET every
`.org` file in the collection, parse, fold every heading's facet values**
into one summary, with per-file `ByContainer` attribution. `FacetVersion`
rides on the WebDAV collection change token when the server advertises one
(`getctag`, as caldav does), else the sorted join of every member file's
`getetag` from one Depth:1 PROPFIND — any file changing moves the token,
so the RFC 0012 §11 memoization stays correct.

### Mutation (`NodeMutator`) — read-modify-write with optimistic concurrency

This is the second consequence of the granularity inversion. A heading is
**not independently PUT-able**; every mutation is a whole-file cycle:

```
1. GET the enclosing .org file          → (bytes, ETag)
2. locate the target heading             (by the address, §Sub-file addressing)
3. apply the edit to the file text       (§Fidelity: surgical splice)
4. PUT the whole file  If-Match: <ETag>  → 200/204 on success
5. on 412 Precondition Failed            → re-GET, re-apply, re-PUT (bounded retry)
```

Step 4's `If-Match` is **mandatory, not optional**: two agents patching
two *different* headings in the *same* file both read-modify-write the
whole blob, so without the conditional PUT the second silently clobbers
the first. caldav sidesteps this entirely (independent resources); org
cannot. The bounded-retry loop on 412 is the price of sub-file
granularity, and it must be in the plugin, not left to the caller.

Verb mapping (the FDR 0020 surface, unchanged in shape):

- `patch_node` — **the primary org verb.** Body is a field subset:
  `{"todo":"DONE"}`, `{"tags":{"add":["urgent"]}}`,
  `{"deadline":"2026-08-01"}`, `{"priority":"A"}`, `{"scheduled":null}`
  (null clears), `{"body":"…"}`. Absent fields untouched; unknown fields
  ignored (FDR 0020 contract). This is what "mark it done / reschedule
  it / tag it" compiles to, and why the FDR 0020 `PutNode`/`PatchNode`
  split (driven by nebulous#40's single-field flips) matters here: an org
  agent flips one field constantly.
- `put_node` — full-replace a heading's subtree from raw org text or the
  structured JSON (dual-format, symmetric with `ReadLeaf`, exactly caldav).
- `create_node` — insert a new heading. Two flavors, both real:
  - caller-names-the-outline-position (`CreateNode` with a URI whose
    fragment names the parent + a title) — file it under a known parent;
  - **`ContainerCreator.CreateChild`** (server-*ish*-assigned identity)
    when the plugin mints the heading's `:ID:` — "capture this into
    `inbox.org`," returning the new heading's URI. The `fj` issue and
    `newsblur` feed already use `ContainerCreator`; a new org heading that
    gets a fresh UUID `:ID:` is the same pattern.
- `delete_node` — cut a heading's subtree (headline through the byte
  before the next same-or-higher-level star). Deleting a heading *with
  sub-headings* removes the whole subtree — state it (the FDR 0020
  container-recursion open question, answered "recurse" for org, since a
  heading and its subtree are one editing unit).

### Capture / restore / diff (the byte-faithful axis)

Unchanged from the caldav template, and deliberately **not** structured:

- **Capture** each `.org` file as a regular `capture_receipt.EntryV1`
  file entry (`TypeTag() = capture_receipt.TypeTagV1`), `EntryV1.Path` =
  the server-relative path, body = verbatim bytes. A collection capture
  walks the WebDAV tree (PROPFIND) and GETs each `.org`.
- **Restore** PUTs each captured body back to its path.
- **Diff** compares live etags/bytes against the receipt.

Because the captured blobs are plain files, an org receipt **also
materializes cleanly through the `file` plugin** — restore it to a local
directory and the `.org` files land unchanged (the same round-trip
property caldav's `.ics` blobs have). The structured heading axis
(traversal/facets/mutation) is a *separate* surface over the same files,
never the capture representation — the exact split FDR 0013 draws
("capture = opaque blob; the parser lives only on the traversal side").

### Config / roots

`[[org.accounts]]`, reusing `config_common.Account` verbatim (name + URL +
credentials), delegated into `cgconfig.ConfigV0` as the `[org]` table —
byte-identical to caldav's `AccountsConfig` (config.go), including
longest-prefix credential resolution (`matchAccount`) and the
`SetConfiguredAccounts` package-state injection. An account URL may point
at a directory (a multi-file store) or a single `.org` file.

## Sub-file addressing (the first hard question)

A heading needs a stable name the WebDAV layer does not provide. The URI
is `org://host/path/file.org` + a **fragment** naming the heading within
the file. Candidates:

| Scheme | Fragment | Robust under… | Cost |
|---|---|---|---|
| **org `:ID:`** (recommended) | `#id=a1b2c3…` | reorder, retitle, reindent, move-within-file | requires an `:ID:` property; the plugin adds one on first mutation (org-id convention — Emacs does the same) |
| `CUSTOM_ID` | `#custom-id=foo` | same, human-readable | user must set it; sparse |
| outline path | `#outline=/Projects/Foo/Bar` | nothing that renames an ancestor | ambiguous on duplicate sibling titles; needs a `[n]` positional tiebreak |
| line/byte offset | `#line=42` | nothing (any edit above it shifts it) | trivial but useless for a mutable store |

**Recommendation:** address by `:ID:`. On read, a heading without an `:ID:`
is surfaced with an outline-path fragment (still browsable) but is flagged
non-stably-addressable; the **first mutation targeting it mints and writes
an `:ID:`** (a UUID in its `:PROPERTIES:` drawer), upgrading it to stable
addressing — exactly how Emacs org-id bootstraps. This keeps read cheap
(no forced rewrite to browse) while making every *mutated* heading
durably addressable. The `outline=` form stays supported as a
convenience/fallback for read and for `create_node`'s parent reference.

This is the single most important lever to settle before promotion —
everything in §Mutation dereferences it.

## Fidelity: surgical splice, not parse-and-reprint (the second hard question)

Step 3 of the RMW cycle — "apply the edit to the file text" — must
**preserve the parts of the file it is not editing**, byte-for-byte. Org
files are human-authored, formatting-significant, and often shared with a
live Emacs; reflowing whitespace, normalizing drawers, or dropping
comments on every `patch_node` is unacceptable.

Two implementation strategies:

- **Full parse → mutate AST → reserialize.** Convenient, but Go's
  `go-org` (`niklasfasching/go-org`) is a *renderer* (org→HTML), not a
  round-trip-faithful printer — reserializing normalizes and loses
  formatting. Rejected as the mutation path.
- **Surgical text splice (recommended).** Parse only far enough to locate
  the target heading's byte span and its internal structure (headline
  line, planning line, `:PROPERTIES:` drawer, body), then splice the
  **minimal** change: rewrite just the headline's TODO keyword in place;
  insert/replace a `DEADLINE:` on the planning line; add a tag to the
  `:tags:` suffix; replace the body byte-range. Everything outside the
  edited span is untouched. This is the org analog of caldav's targeted
  `PatchNode` field edits — but load-bearing here because the file is
  shared and format-significant.

Surgical splicing implies the plugin carries a **focused org outline
parser**, not a full org implementation: it needs the headline grammar
(`^\*+\s+(KEYWORD\s+)?(\[#A\]\s+)?title(\s+:tags:)?$`), the planning line
(`SCHEDULED:`/`DEADLINE:`/`CLOSED:`), `:PROPERTIES:`…`:END:` drawers,
`#+TODO:`/`#+FILETAGS:` keywords, and byte-span tracking. It does **not**
need inline-markup (`*bold*`, links, tables) — the body is opaque text to
this plugin. A few hundred lines, testable in isolation, and far more
faithful for mutation than pulling `go-org`. (If a consumer later wants
rendered bodies for *read*, `go-org` can back that one path without
touching the mutation path.)

### Implementation vehicle: linked Go plugin vs. RFC 0013 wire plugin

- **Linked Go plugin `plugins/org/` (recommended primary).** Mirrors
  caldav directly, reuses the SDK facet/mutation interfaces natively,
  ships in the default binary via `plugins/all`. The focused outline
  parser above lives here. Fastest read/facet path (no external process).
- **RFC 0013 out-of-process wire plugin (fidelity-maximizing
  alternative).** The gold-standard org parser/serializer is Emacs's own
  `org-element`. A wire traversal plugin backed by a headless
  `emacs --batch` (via `pkgs/traversal_serve`) would give *perfect*
  round-trip fidelity for mutation — at the cost of an Emacs runtime
  dependency and process-spawn latency. Worth noting because org fidelity
  is genuinely hard and Emacs already solved it; but a heavy dep for a
  plugin whose reads should be cheap. Recommend linked-Go primary, keep
  the Emacs-wire option in the back pocket if surgical splicing proves too
  lossy in practice. (`fj`/`newsblur` already prove the wire path carries
  nested containers + mutation indistinguishably.)

## Examples

    # Browse an org store as an outline (structured, faceted).
    list_nodes(uri="org://dav.host/dav/org/")
      → [ inbox.org, projects.org, notes/ ]
    list_nodes(uri="org://dav.host/dav/org/projects.org")
      → [ {uri:…#id=a1b2, title:"Cutting Garden", todo:"PROJ",
           tags:["work"], facets:{todo_state:"todo"}}, … ]   # enriched inline

    # "Everything actionable with a deadline this week, across all projects"
    read_facets(uri="org://dav.host/dav/org/",
                filter="todo_state=todo,deadline_band=this-week")

    # Read one heading's full record + verbatim bytes
    read_node(uri="org://dav.host/dav/org/projects.org#id=a1b2")

    # Mark it done — one field, read-modify-write with If-Match retry
    patch_node(uri="org://dav.host/dav/org/projects.org#id=a1b2",
               body='{"todo":"DONE"}')                        # ⇒ clown hook asks

    # Reschedule + tag in one patch
    patch_node(uri="…#id=a1b2",
               body='{"deadline":"2026-08-01","tags":{"add":["urgent"]}}')

    # File a new capture into inbox.org (server-assigned :ID:)
    create_node(uri="org://dav.host/dav/org/inbox.org",
                type="org-heading-v1",
                body='{"title":"Call the dentist","todo":"TODO"}')
      → { uri: "org://dav.host/dav/org/inbox.org#id=<new-uuid>" }

    # Cut a subtree
    delete_node(uri="org://dav.host/dav/org/projects.org#id=a1b2")

    # The snapshot axis is independent and byte-faithful:
    cutting-garden capture org://dav.host/dav/org/     # → receipt of raw .org blobs
    cutting-garden restore <receipt> ./local-org/      # → files land unchanged (file plugin)

## Limitations

- **Whole-file RMW cost.** Every heading mutation GETs and PUTs the
  entire enclosing file. On a multi-thousand-heading `everything.org` this
  is a real cost per edit (tuning lever: cache the last GET+etag within a
  request; batch multiple patches to one file into one RMW).
- **Concurrency is WebDAV-scoped only.** `If-Match` catches a racing
  *WebDAV* write, but not an in-progress *local Emacs* edit whose buffer
  hasn't synced (Emacs's `.#file.org` lock files are not WebDAV-visible).
  A concurrent human editor can still lose an edit at the sync boundary —
  document it; do not pretend the plugin makes org multi-writer-safe.
- **Encrypted `.org.gpg` out of scope** for v1 (no decrypt in the read
  path; the capture axis would snapshot ciphertext, but traversal/facets
  can't parse it).
- **No agenda/clocking semantics** beyond `SCHEDULED`/`DEADLINE`/`CLOSED`
  facets — no clock-table aggregation, no repeater expansion (`+1w`
  cookies are surfaced verbatim, not expanded; contrast the caldav
  recurrence-expansion work, plans/2026-07-20).
- **No inline-markup structure** — the heading body is opaque text; links,
  tables, and blocks are not parsed (the focused parser stops at heading
  boundaries).
- **`:ID:`-on-mutation writes even for a "read-only-feeling" patch of an
  ID-less heading** — the first mutation of such a heading rewrites the
  file to add its `:ID:`. State it so it is not a surprise.

## Tuning Levers

| Lever | Starting point | Change signal |
|---|---|---|
| Heading address scheme | `:ID:` primary, `outline=` fallback | duplicate-title ambiguity or ID-churn shows up in practice |
| ID bootstrap timing | mint `:ID:` on first mutation | agents want stable addresses for *reads* too → mint on first read (forces a rewrite; heavier) |
| Parser | focused in-tree outline parser | surgical splices prove lossy on real files → escalate to the Emacs-wire vehicle |
| RMW batching | one file GET+PUT per verb | agents issue many patches to one file → a batch/transaction verb |
| Interior-heading body read | enriched fields only (§duality opt 1) | consumers need interior bodies → add the `org-heading-body-v1` projection |
| Body projection emission | only when body non-empty | — |
| Delete on non-empty heading | recurse (subtree is one unit) | someone wants refuse-if-children semantics |

## Open Questions

- **Addressing (blocking).** `:ID:` vs `CUSTOM_ID` vs outline-path as the
  *primary* — and whether ID-less headings are addressable at all, or must
  be given an ID before they appear. This gates every mutation.
- **Tag inheritance in facets.** Does a heading's `tag` facet include tags
  inherited from ancestors and `#+FILETAGS:` (org's own agenda semantics),
  or only its own headline tags? Inheritance matches org; it also makes a
  child's facets depend on its ancestors (a `FacetCounter` detail).
- **`todo` open vs. a config-declared closed set.** Could the plugin read
  every file's `#+TODO:` at startup and publish a *union* closed domain
  for `todo`? Cross-file union is leaky (files disagree); `todo_state`
  stays the safe closed dimension either way.
- **File-level CUD.** Is adding/removing a whole `.org` file
  (`create_node`/`delete_node` on `org-collection-v1`) in v1, or
  heading-only first? (Leaning heading-only; file CUD is a WebDAV
  `PUT`/`DELETE`/`MKCOL` away when wanted.)
- **Shared WebDAV client.** caldav and this plugin now both carry a WebDAV
  PROPFIND/conditional-PUT client. Extract a `pkgs/webdav`? (Defer — let
  the second implementation exist before factoring, as caldav did.)

## More Information

- [FDR 0013](0013-caldav-plugin.md) — the closest prior art (scheme
  claim, account config, capture=blob / traversal=structured split); the
  plugin to mirror.
- [RFC 0011](../rfcs/0011-caldav-archive-binding.md) — caldav's
  capture/restore/diff binding; the byte-faithful axis template.
- [FDR 0014](0014-plugin-root-traversal.md) — `RootLister`/`RootProvider`,
  the traversal primitive and URI address space heading nodes live in.
- [FDR 0015](0015-mcp-resource-server.md) — the `mcp` read surface headings
  are browsed/faceted through.
- [FDR 0020](0020-cud-tree-modifications.md) — `NodeMutator` /
  `ContainerCreator` and the `patch_node`/`put_node` split org mutation
  rides on; the #102 permission gate.
- [RFC 0012](../rfcs/0012-plugin-facet-contract.md) — the facet contract;
  `deadline_band` is a direct lift of caldav's volatile `due_band`.
- [RFC 0013](../rfcs/0013-traversal-plugin-jsonrpc-transport.md) — the
  out-of-process wire vehicle (the Emacs-`org-element` fidelity option);
  `fj`/`newsblur` are the nested-container precedent.

---
*Design exploration draft — not yet a commitment. The two blocking
questions (§Sub-file addressing, §Fidelity) each carry a recommendation to
be validated on a throwaway prototype before this is promoted to
`proposed`.*
