---
status: proposed
date: 2026-06-15
supersedes-design: |
  This revision replaces the flat capture shape this FDR originally
  proposed (one opaque `*all` JSON blob per issue, keyed `PROJECT/KEY.json`,
  reusing `capture_receipt.TypeTagV1`). That shape was lossy — it collapsed
  every native Jira object into a single per-issue blob — and the worst case
  for invalidation: any field edit rewrote the whole issue blob and no
  sub-issue object could be shared or diffed. The landed code
  (`plugins/jira/`, capture+diff over `CapturePlugin`/`EntryV1`) is the
  flat increment and is to be reworked onto the protocol-capture shape
  specified here.
promotion-criteria: |
  Promote to `experimental` once the plugin emits an RFC 0002 merkle
  receipt for one project, the node types below are declared, and one
  manual end-to-end capture + a second incremental capture (with at least
  one changed and one unchanged issue) demonstrate subtree reuse by
  markl-id. Promote to `testing` once `diff` descends only changed
  subtrees against a real project. Promote to `accepted` after a milestone
  ships with the plugin in the default binary and the `updated` reuse
  contract has gone two weeks without a correctness lever moving.
---

# Jira plugin

## Problem Statement

Cutting-garden captures filesystem trees and, via the yt-dlp, git, gphotos,
and caldav plugins, several classes of URL-addressable network source. A
Jira project — where a team accumulates the durable record of its work —
sits outside that surface. The only workaround is to script the REST API
into a directory of JSON and `cutting-garden capture` that, which mixes
archival intent into ad-hoc layout and loses the project/issue addressing
as an organizing key.

But Jira is not one object. An issue is a small graph of native objects —
fields, an ADF description, comments, attachments (with binary content),
worklogs, typed links, an append-only changelog — over a shared backdrop of
slow-changing schema (fields, statuses, issue types) and actors (users). A
faithful capture must **represent those native types losslessly** and in a
shape where **unchanged subtrees can be severed and reused** rather than
re-fetched, and where a diff touches only what changed. A single opaque blob
per issue satisfies neither goal.

This FDR specifies the plugin as a **protocol-capture plugin** (RFC 0002):
`cutting-garden capture` and `diff` route `jira:` arguments through the Jira
Cloud REST v3 API and emit a **merkle tree of typed, content-addressed
nodes** — one node per native Jira object — into the blob store. Dedup and
severing fall out of content addressing the way they do in git; the issue
node is the unit of reuse, and the issue `updated` timestamp is the
freshness oracle that governs it.

The Jira REST surface is already exercised in the org by the **`sisyphus`**
moxin in `amarbel-llc/moxy` (search-by-JQL, issue GET, project enumeration,
`JIRA_URL`/`JIRA_USERNAME`/`JIRA_API_TOKEN` basic auth) as interactive MCP
tools. This plugin is the capture-shaped sibling — same API, same auth, but
it snapshots state rather than mutating a live tracker, the same
relationship the caldav plugin (FDR 0013) has to bob's CalDAV MCP tools.

## Approach: protocol capture, not flat entries

Two capture surfaces exist (RFC 0009 SDK, RFC 0002 protocol):

- The **flat path** (`CapturePlugin.CaptureRoot` → `[]EntryV1`) treats a
  capture as a set of independent file entries folded into a shared
  store-group receipt. caldav, gphotos, yt-dlp use it. It has no notion of
  inter-object structure or per-object identity.
- The **protocol path** (`ProtocolCapturePlugin.CaptureProtocol` → an
  RFC 0002 receipt) emits a self-contained merkle tree of typed hyphence
  nodes joined by typed blob references (`< @<digest> !<type>@<sig>`). A
  consumer traverses the receipt and pulls every referenced blob
  recursively; unchanged subtrees are shared blobs across captures. git and
  the web plugin use it.

Jira takes the **protocol path**. The node types below are the plugin's
binding (RFC 0002 leaves plugin node schemas to the plugin); the wire
framing, traversal contract, and identity/outcome subtrees are RFC 0002's.

## Jira object model

Grouped by change-rate and dedup value — this grouping is the tree's spine:

**Instance / global schema** — slow-changing, shared across every capture of
the site, referenced by issues *by id*:

- `serverInfo` (version, deployment type)
- **fields** — system + custom field definitions (`customfield_10001` → name,
  schema, custom type)
- **issue types**, **statuses** + status categories, **priorities**,
  **resolutions**, **issue link types**

**Actors** — referenced by id from many places (assignee, reporter, creator,
comment/worklog author, voter, watcher):

- **users** (accountId, displayName, email visibility, active), **groups**

**Project config:**

- project entity (key, name, lead, project type, company- vs team-managed
  style), **components**, **versions** (releases), project roles

**Issue-level** (the bulk; the severable unit):

- core **fields** (system) + **custom field** values
- **description** (ADF) — large, edited independently of fields
- **comments** (author, ADF body, created/updated, visibility)
- **attachments** — metadata **+ raw binary content** (immutable once
  uploaded)
- **worklogs**, **issue links** (typed; target another issue by key),
  **changelog** (append-only field-change history, `expand=changelog`)
- **watchers**, **votes**, **remote links**, **entity properties** (app
  key-value)
- **sub-tasks** (their own issues)

**Agile** (`/rest/agile/1.0`): **boards** (per project / filter-backed),
**sprints** (belong to a board; issues reference via the sprint field),
**epics** (an issue type plus an epic-link relationship).

Out of scope for an issue archive: filters, dashboards, workflow-as-config,
webhooks/automation.

## Node types

Declared via `Types()`, hyphenated and horizontally versioned (FDR 0018
unified type namespace, issue #79): a future shape change adds a `-v2`
beside the `-v1`. Container nodes carry only typed references to children;
leaf nodes carry canonical bytes.

| Tag (`cutting_garden-jira-…`) | Kind | Holds |
|---|---|---|
| `site-v1` | container | refs to `server-info`, `catalog`, `actors`, `projects` |
| `server-info-v1` | leaf | version / deployment metadata |
| `catalog-v1` | container | refs to every schema-object node below |
| `field-v1` | leaf | one field definition (system or custom) |
| `issue-type-v1` / `status-v1` / `priority-v1` / `resolution-v1` / `link-type-v1` | leaf | one schema object |
| `actors-v1` | container | refs to `user`/`group` nodes |
| `user-v1` / `group-v1` | leaf | one account / group snapshot |
| `projects-v1` | container | refs to `project` nodes |
| `project-v1` | container | project entity + refs to components/versions/boards/issues collections |
| `component-v1` / `version-v1` | leaf | one project component / release version |
| `board-v1` | container | board entity + refs to `sprint` nodes |
| `sprint-v1` | leaf | one sprint |
| `issues-v1` | container | refs to `issue` nodes (keyed by issue key) — **the reuse boundary** |
| `issue-v1` | container | refs to `issue-fields`, `description`, comments/attachments/worklogs/links collections, `changelog`, watchers, votes, remote-links, properties |
| `issue-fields-v1` | leaf | system + custom field **values** (reference catalog/actors by id) |
| `description-v1` | leaf | the issue's ADF description body |
| `comment-v1` | leaf | one comment (author ref, ADF body, created/updated, visibility) |
| `attachment-v1` | container | attachment metadata + ref to its content blob |
| (attachment content) | leaf | raw binary, stamped with the attachment's IANA media type (dodder null-type otherwise) |
| `worklog-v1` | leaf | one worklog entry |
| `issue-link-v1` | leaf | one typed link (link-type ref + target issue key) |
| `changelog-v1` | leaf | append-only ordered field-change history |
| `remote-link-v1` | leaf | one remote/Confluence link |
| `property-v1` | leaf | one entity property (app key-value) |
| `watchers-v1` / `votes-v1` | leaf | watcher accountId list / vote count + voters |

## Merkle tree structure

```
receipt                          RFC 0002 root; per-run, NOT a dedup key
├── identity/                    host + plugin config + site origin + auth principal  (dedups across runs)
├── outcome/                     timing, counts, the `updated` window used, failures
└── payload → site
    ├── server-info
    ├── catalog/                 fields/<id>  issue-types/<id>  statuses/<id>
    │                            priorities/<id>  resolutions/<id>  link-types/<id>
    ├── actors/                  users/<accountId>   groups/<name>
    └── projects/<PROJECT>/
        ├── (project entity)     components/<id>  versions/<id>
        ├── boards/<boardId>/sprints/<sprintId>
        └── issues/
            └── <KEY>            ◀── THE severable unit; its markl-id is the reuse key
                ├── fields            (values reference catalog/ + actors/ by id)
                ├── description       (ADF, own blob)
                ├── comments/<id>     (body ADF own blob)
                ├── attachments/<id>/{ meta, content }   ◀── content = raw binary blob
                ├── worklogs/<id>  links/<id>  changelog
                └── watchers  votes  remote-links/<id>  properties/<key>
```

**Node identity (the merkle rule).** A leaf's markl-id is the hash of its
canonical bytes. A container's markl-id is the hash of its hyphence body,
which lists its children's typed references in a deterministic order (keyed
by id, sorted). A container's id therefore changes **iff some descendant
changed**; an unchanged subtree keeps its id and is a physically shared blob
across captures. This is RFC 0002's automatic dedup — not bolted on, but a
consequence of the type-driven recursive references the hyphence walker
already follows.

## Severing & invalidation

The pivot is that the issue **`updated` timestamp is a cheap freshness
oracle**, so the `issue-v1` subtree is the unit you graft or rebuild.

**Incremental capture:**

1. The prior receipt records, per issue key, its `issue-v1` **markl-id** and
   its `updated` value (in the `outcome` subtree's per-issue index).
2. New capture: `project = X ORDER BY updated DESC`, `fields=[updated]` — one
   paginated search, **no bodies**.
3. Per key:
   - **`updated` unchanged** vs prior → the new `issues-v1` container
     references the **prior `issue-v1` markl-id verbatim**; comments,
     attachments, changelog are **never fetched**. This is the *sever*: the
     whole issue subtree is grafted from the old tree by id.
   - **`updated` advanced** → re-fetch and rebuild *only that* issue subtree.
   - **new** key → build fresh. **absent** (in prior tree, not in new query)
     → drop (deletion).
4. `projects`/`site`/`catalog` containers recompute from their children;
   unchanged issues contribute their prior ids, so only the hash-chain along
   the changed path rehashes.

**Diff** is then a tree walk: compare two receipt roots and **descend only
where child markl-ids differ** → O(changed), not O(total). An unchanged
issue is one id comparison and zero network calls.

This is the `ProtocolDiffPlugin` contract; restore (deferred, below) would be
`ProtocolRestorePlugin` keyed on the `jira` receipt kind.

## Dedup properties

- **Attachment binaries** are stored once (immutable → perfect content
  addressing); the same file on two issues is one blob.
- **Catalog/actor** changes invalidate **one** node. Issues reference schema
  and users by id, so a display-name edit rehashes the `user-v1` node alone —
  issues do not rehash. (Trade-off: the issue's *rendered* view changed but
  its node did not. Correct for an archive; see Open Questions.)
- **ADF description / comment bodies** are their own blobs, so a summary edit
  does not rewrite the (large) description.
- **Changelog is append-only**, so new history extends the node and old
  entries' contribution is stable.

## Interface

### Accepted argument forms

The single `jira` URI scheme, two forms (mirroring caldav's
opaque/hierarchical split):

1. `jira://[user[:token]@]host[/PROJECT[/ISSUE-KEY]]` → `https://host/…`
   (TLS assumed, the Jira Cloud common case).
2. `jira:<http(s)-url>` → verbatim (the only way to reach a plain-HTTP
   self-hosted instance, e.g. `jira:http://10.0.0.2:8080/PROJ`).

The URL path is the in-Jira address relative to the REST origin
(`scheme://host`): bare host = the site root (all browsable projects);
`/PROJECT` = one project; `/PROJECT/KEY-1` = one issue. A context-path
install (`https://host/jira`) is out of scope — the path is project/issue.

### Credentials (RFC 0007)

Basic auth: Atlassian account email as username, API token as password.
Precedence: URL userinfo → `[[jira.accounts]]` matched by host + longest
project-path prefix (token from the account's `password_env`) →
`JIRA_USERNAME` / `JIRA_API_TOKEN` (the env vars sisyphus uses). Configured
accounts are also surfaced as credential-free `RootProvider` roots for
no-argument `list` / `mcp`.

### Traversal

`RootLister` walks the same node types lazily, one level per call: site →
projects → issues → (issue object collections). The `list`/`mcp` consumers
descend the live tree without capturing; capture and traversal share the
same enumeration so they cannot disagree about the tree.

## Restore Deferral

The plugin registers **capture + diff only**; restore is intentionally not
implemented (it would be a `ProtocolRestorePlugin` on the `jira` kind). A
capture is a snapshot for archival/backup. Writing issues back to a live
tracker is lossy and destructive:

- A captured issue carries read-only/rendered fields (watchers, rendered
  HTML, history, rollups) that cannot be written back.
- Bodies are ADF; faithful round-trip of description/comment content is a
  parser problem out of scope here.
- "Restore" of a deleted issue is a *create* with new identity, not an
  in-place rewrite — no non-surprising semantics.

The captured nodes still materialize to a local directory through a
read-only export (the merkle tree projects to a JSON/asset tree), which is
the useful restore target. Same posture as gphotos (FDR 0017) and yt-dlp. A
write-back, if ever wanted, would be a separate, narrowly-scoped feature
(e.g. summary/description update of existing issues only).

## Design decisions / tensions

- **Reference-by-id vs embed** (catalog, actors): by-id maximizes dedup and
  localizes invalidation but makes an issue node non-self-contained (needs
  the catalog to interpret values). **Decision:** reference by id, but
  capture the catalog/actors in the **same receipt** so the tree is closed
  and self-describing.
- **Cross-issue references must be by-id edges, not child edges.** A merkle
  tree is a DAG; embedding a linked issue or a sub-task as a *child* of an
  issue creates cycles and double-captures. **Decision:** `issue-link-v1`
  and parent/sub-task relationships store the target **issue key** as data;
  the target is captured once under its project's `issues/`, not nested.
- **Team- vs company-managed** projects model parent/epic differently
  (parent field vs epic-link custom field). **Decision:** store the raw
  field and record the project style on `project-v1`; do not normalize.

## Open Questions

- **`updated` is coarse.** It bumps on field/comment/worklog/transition/
  attachment edits, but a watcher-add, vote, or app entity-property write may
  not. Treat `updated` as the documented reuse contract (rare non-bumping
  mutations caught on the next *full*, non-incremental capture), or add a
  cheap volatile-probe hash over watchers/votes/properties? Leaning:
  document the contract; offer `--full` to force a complete re-hash.
- **Attachment content fetch policy.** Always pull binaries (faithful, but
  heavy), or make it opt-in (`--with-attachments`) with metadata-only the
  default? The binary is the immutable, perfectly-deduped part, so always
  pulling is cheap on re-capture; leaning always-on with an opt-out.
- **Changelog volume.** Long-lived issues have large changelogs. One node per
  issue (append-only) vs chunking by page? Leaning one node until a real
  issue exceeds a size threshold.
- **Sprint/board scope.** Boards are often filter-backed and span projects;
  capturing them under one project is a simplification. Capture agile objects
  only when present and reachable from the project, defer cross-project
  boards.

## Relationship to the landed increment

`plugins/jira/` currently implements the **flat** capture+diff shape (one
`*all` JSON `EntryV1` per issue, `capture_receipt.TypeTagV1`). That is the
initial increment and the design of record is this FDR's protocol-capture
shape; the rework registers `ProtocolCapturePlugin` / `ProtocolDiffPlugin`,
emits the node types above, and threads the `updated` reuse index through
the `outcome` subtree.

## References

- [RFC 0002: capture plugin protocol](../rfcs/0002-capture-plugin-protocol.md)
  — the merkle-receipt wire protocol, identity/outcome subtrees, typed blob
  references, and traversal contract this plugin binds to.
- [RFC 0003: web archive binding](../rfcs/0003-web-archive-binding.md) — the
  precedent for a plugin's node-schema binding over RFC 0002.
- [FDR 0013: caldav plugin](0013-caldav-plugin.md) — the network +
  account-config + RootProvider/RootLister sibling.
- [FDR 0014: plugin root traversal](0014-plugin-root-traversal.md) — the
  `RootLister`/`Types()` primitive and node-type versioning (#79).
- [FDR 0017: google-photos plugin](0017-google-photos-plugin.md) — the
  capture+diff-only, restore-deferred precedent.
- [FDR 0018: unified type namespace](0018-unified-type-namespace.md) — the
  node-type naming this follows.
- [RFC 0007: config subsystem](../rfcs/0007-config-subsystem.md) — account
  config and credential resolution.
- `amarbel-llc/moxy` `moxins/sisyphus` — the interactive MCP-tool sibling
  speaking the same Jira REST surface.
