# jira (plugins/jira)

The Jira Cloud capture/diff backend for cutting-garden. Lives outside
`internal/` (in `plugins/`), consuming the public plugin SDK (`pkgs/`,
RFC 0009) like an out-of-tree plugin would — it imports `pkgs/`, never
`internal/` (enforced by the `internal/sdklayering` guard). Registered in
`init()` under the single `"jira"` URI scheme, in two argument forms
(mirroring the caldav opaque-vs-hierarchical split, see `url.go`):

- hierarchical `jira://[user[:token]@]host[/PROJECT[/ISSUE-KEY]]` →
  `https://host/…` (the common form; TLS assumed, matching Jira Cloud at
  `*.atlassian.net`).
- opaque `jira:<http(s)-url>` → the inner URL verbatim (the only way to
  reach a plain-HTTP self-hosted instance, e.g. a LAN Jira at
  `jira:http://10.0.0.2:8080/PROJ`).

This is the capture-shaped sibling of the **`sisyphus`** moxin in
[`amarbel-llc/moxy`](https://code.linenisgreat.com/moxy), which exposes
the same Jira Cloud REST v3 surface (search-by-JQL, issue GET, project
enumeration, `JIRA_URL`/`JIRA_USERNAME`/`JIRA_API_TOKEN` basic auth) as
interactive MCP tools. sisyphus mutates a live tracker; this plugin
snapshots issue state for archival/backup — it offers no interactive
mutation, exactly as the caldav plugin re-homes bob's CalDAV MCP tools as a
read-only capture backend.

## Two capture paths: protocol (default) and flat (fallback)

The plugin implements **both** the SDK's capture surfaces, and the
orchestrator prefers the protocol one (caldav/git's pattern):

- **Protocol capture** (`ProtocolCapturePlugin.CaptureProtocol`,
  `protocol.go`) — the design of record (FDR 0019). Emits an RFC 0002
  **merkle tree of typed, content-addressed nodes** rather than flat file
  entries: `receipt → identity/outcome/payload → site → projects → project
  → issues → issue → {fields, description, comments}`. The `issue` node is
  the severable unit — an unchanged issue's whole subtree is grafted from
  the prior receipt by markl-id (no re-fetch). Because the plugin
  type-asserts to `ProtocolCapturePlugin`, the orchestrator routes
  `capture jira:…` here.
- **Flat capture** (`CapturePlugin.CaptureRoot`, `capture.go`) — the
  original increment, kept as the fallback the `CapturePlugin` interface
  requires. One opaque canonical-JSON file entry per issue, keyed
  `PROJECT/KEY.json`, reusing `capture_receipt.TypeTagV1`.

Diff mirrors this: `DiffProtocol` (`diff_protocol.go`, routed by the `jira`
receipt kind) for protocol receipts; `ScanForDiff` (`diff.go`) for flat
ones. Restore is deferred for both (see below). The remaining FDR 0019
node taxonomy (catalog, actors, attachments+binaries, worklogs, changelog,
links, agile) is a follow-up — this increment realizes the machinery plus
the issue subtree (fields/description/comments), which is all the single
`*all` fetch carries.

## The node address

A `jira:` URL's path is the in-Jira address relative to the REST origin
(`scheme://host`):

- `jira://host` — the **root**: every browsable project.
- `jira://host/PROJECT` — one **project**: every issue in it.
- `jira://host/PROJECT/KEY-1` — one **issue**.

A Jira instance served under a context path (e.g. `https://host/jira`) is
out of scope — the path is interpreted as project/issue, matching the Jira
Cloud target.

## Credentials

Credentials resolve by the RFC 0007 precedence (`connectionFromArg`,
`url.go`): the URL's userinfo (`user:token@host`) when present, else a
configured account matched by host + longest project-path prefix
(`[[jira.accounts]]`, injected via `SetConfiguredAccounts`; the API token
comes from the account's `password_env`), else the global `JIRA_USERNAME` /
`JIRA_API_TOKEN` (the same env vars `sisyphus` uses — username is the
Atlassian account email, the "password" is the API token, sent as HTTP
basic auth). A request with no resolvable credentials is sent
unauthenticated — the server's 401 is surfaced as the capture/diff error.

`Roots` (`config.go`) exposes the configured accounts' endpoints as
credential-free traversal roots for the `RootProvider` capability
(`list`/`mcp` with no argument). See
[RFC 0007](../../docs/rfcs/0007-config-subsystem.md).

## What lives here

- `protocol.go` — `CaptureProtocol`: the RFC 0002 merkle-tree capture
  (`protocolCapture` walks projects → issues, writing typed container and
  leaf nodes via `pkgs/capture_plugin`). `captureIssue` holds the severing
  logic (graft an unchanged issue's prior subtree by markl-id).
- `decompose.go` — `decomposeIssue`: splits one `*all` issue into its merkle
  leaves — the field values, the ADF `description` (lifted out), and one
  node per comment — so a comment or description edit rewrites only that
  leaf, not the whole issue (FDR 0019 §Dedup properties).
- `protocol_consume.go` — the read side: `loadPriorIndex` reads the prior
  receipt's outcome reuse index (key → {updated, issue-node digest}) that
  drives subtree reuse; best-effort (any miss → full re-fetch).
- `diff_protocol.go` — `DiffProtocol`: compares a `jira` receipt against the
  live source using the bodiless `updated` probe; reports A/M/D by issue
  key, descending only changed issues.
- `types_register.go` — registers the protocol node type-strings (and their
  ref-alias constants) into the RFC 0002 type-signature registry.
- `Plugin.CaptureRoot` (`capture.go`) — the **flat** fallback path:
  enumerates issues via the enhanced JQL search endpoint
  (`/rest/api/3/search/jql`, `nextPageToken` pagination) and streams each
  issue's canonical JSON body as one file entry keyed `PROJECT/KEY.json`.
- `Plugin.ScanForDiff` (`diff.go`) — the flat diff analogue for
  `capture_receipt.TypeTagV1` (fs-kind) jira receipts.
- `Plugin.ListRoots` / `Types` (`traversal.go`) — the `RootLister`
  traversal tree: root → projects (containers) → issues (leaves). It shares
  the search/project endpoints with capture so discovery and capture cannot
  disagree about the tree.
- `client.go` — a minimal, context-aware Jira REST v3 client (JQL search +
  the bodiless `updated` probe, issue GET, project search). It carries no
  Jira object model of its own: each issue arrives as opaque JSON,
  canonicalized (key-sorted) so an unchanged issue hashes identically. The
  protocol path's structural decomposition lives in `decompose.go`, not the
  client.
- `url.go` — argument coercion (`baseURLFromArg`, `connectionFromArg`),
  origin/project/issue parsing (`nodeFromBase`), and the inverse
  node-URI builder (`jiraURIForNode`).

## Canonical JSON, stable hashing

Jira does not guarantee a stable key order across fetches, so a raw body
would hash differently each time and defeat both diff and merkle dedup.
`canonicalJSON` (`client.go`) round-trips every body through `encoding/json`
(which sorts map keys on marshal) with stable indentation, so an unchanged
issue (or field-blob, description, comment) produces byte-identical bytes.
The flat path canonicalizes the whole issue; the protocol path canonicalizes
each decomposed leaf (`decompose.go`), including a load-bearing re-canonical
round-trip of the trimmed fields so nested key order can't leak through a
`json.RawMessage`.

## No Jira SDK, no new dependencies

The plugin carries no third-party Jira SDK. The flat path treats each issue
as verbatim canonical JSON; the protocol path adds a **minimal, in-package**
object model in `decompose.go` (it knows only that `fields.description` is
the ADF body and `fields.comment.comments[]` are the comments, lifting those
into their own merkle leaves) — no ADF parser, no field-schema model. The
package depends only on the stdlib plus the dewey/madder facades the other
plugins already use — no `gomod2nix`/flake change.

## Restore deferral

`init()` registers **capture + diff only** (flat capture, flat diff, and
protocol diff). Restore is intentionally not implemented for either path:
re-creating or updating issues on a live tracker is a lossy, destructive
mutation (read-only rendered fields, ADF bodies, issue-creation semantics),
and a captured snapshot is for archival/backup, not round-trip mutation. The
captured `.json` files still restore to a local directory through the
filesystem plugin. Same posture as the google-photos and yt-dlp plugins. See
[FDR 0019](../../docs/features/0019-jira-plugin.md) §Restore Deferral.

## Receipt kinds and node types

The two paths write different receipt kinds:

- **Flat:** `Plugin.TypeTag()` returns `capture_receipt.TypeTagV1`
  (`cutting_garden-capture_receipt-fs-v1`) — flat jira captures are regular
  file entries, byte-identical EntryV1 shape to fs captures, so a receipt
  mixing fs and flat-jira roots carries one type-tag and the `.json` blobs
  restore through the filesystem plugin. `ScanForDiff` handles these.
- **Protocol:** the merkle receipt carries the `jira` **kind**
  (`cutting_garden-capture_receipt-jira-v1`, via `capture_plugin.ReceiptType`),
  and its tree is built from the node types registered in `types_register.go`
  (`cutting_garden-jira-site/projects/project-node/issues/issue-node-v1`
  containers; `jcs-jira-issue-fields/description/comment-v1` leaves; the
  `jcs-jira-outcome-index-v1` reuse index under the outcome subtree).
  `DiffProtocol` (routed by the `jira` kind) handles these.

The `cutting_garden-jira-project-v1` / `-issue-v1` tags in `traversal.go`
are the separate **traversal** node-type namespace used by `list`/`mcp`
(FDR 0018), distinct from both the receipt type-tag and the protocol
capture-tree node types above.

## References

- [FDR 0019: jira plugin](../../docs/features/0019-jira-plugin.md) — behavior.
- [FDR 0013: caldav plugin](../../docs/features/0013-caldav-plugin.md) — the
  closest sibling (pure-Go, network, account-config, RootProvider +
  RootLister) this plugin mirrors.
- [FDR 0005: URI-scheme plugins](../../docs/features/0005-uri-scheme-plugins.md)
  — the scheme-keyed plugin model this implements.
