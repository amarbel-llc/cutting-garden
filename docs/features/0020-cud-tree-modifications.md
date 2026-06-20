---
status: experimental
date: 2026-06-18
promotion-criteria: |
  Promote to `experimental` once the prototype CUD capability interface
  and its MCP-tool binding land in the caldav plugin (against the
  memstore test harness, no live server required) and a single
  create→update→delete round-trip on one VEVENT is exercised end-to-end
  through the `mcp` server with the #102 permission gating live. Promote
  to `testing` once a SECOND plugin (file) implements the same interface
  unchanged — confirming the verb set and node-addressing are not
  caldav-shaped — and the pre-RFC API is lifted into a real RFC. Promote
  to `accepted` once the RFC's wire/permission contract has gone two
  weeks without a lever moving (in particular without the verb set or the
  read/destructive classification changing).
---

# CUD tree modifications (plugin write capability + MCP tool binding)

> **Experimental / pre-RFC.** This FDR scopes the *write* axis of plugin
> trees: creating, updating, and deleting an addressable node, plus the
> MCP tools that expose those mutations to an agent. The Go interface is
> still **pre-RFC and subject to change** — its purpose is to shake out the
> capability shape on a prototype plugin (caldav) before it is lifted into
> a normative RFC.
>
> **Landed (every layer, unit- AND e2e-tested):** the `NodeMutator` SDK
> capability (`internal/cutting_garden_plugins`), caldav's implementation
> (`plugins/caldav/mutate.go` — strict create/update, delete, dual-format
> body), the MCP `create_node`/`update_node`/`delete_node` tools
> (`internal/mcp/tools.go`), and the shared `mcp_tool_perms` classifier
> feeding both the tools' destructive annotations and the #102 clown
> PreToolUse hook (`internal/claude_hooks`, gates them `ask`). The
> end-to-end exercise — create→update→delete driven through
> `cutting-garden mcp` over the stdio transport against the caldav
> testserver — runs in `zz-tests_bats/mcp.bats`, alongside the hook gating
> a CUD tool `ask`.

## Problem Statement

cutting-garden's tree model is **read-only end to end**. FDR 0014's
`RootLister` enumerates a plugin's capturable tree (`Types()` +
`ListRoots`); FDR 0015's `mcp` server exposes that tree over MCP as
**resources only** — its Non-Goals say so explicitly: "No tools — no
capture/restore mutation tools. A read-only window onto the tree." There
is no first-class way to *mutate* a node: create a calendar event, update
an `.ics`, delete a file at its URI.

That gap is felt most sharply in caldav. FDR 0013 records that the
original CalDAV capability in `amarbel-llc/bob` was an MCP server with
**~18 live tools for creating, updating, completing, and deleting** tasks
and events — and that cutting-garden deliberately re-homed only the
*snapshot* half (capture/restore/diff), listing "No live mutation tools"
as an explicit Non-Goal. The clown PreToolUse hook (#102) is already
scaffolded but inert precisely "until `cutting-garden mcp` grows
create/update/delete (write) tools." CUD is the deferred write half: the
mutating sibling of the read axis, specified once as a plugin capability
and bound to MCP tools an agent can call under permission gating.

## Interface

The feature has two layers, mirroring how the read axis split across
FDR 0014 (the `RootLister` primitive) and FDR 0015 (its MCP surface),
consolidated here into one feature record:

1. a **plugin CUD capability interface** — the write-side sibling of
   `RootLister`; and
2. its **MCP-tool binding + permission classification** — the
   agent-facing surface and the #102 `mcp_tool_perms` gating.

### Layer 1 — the CUD capability interface (pre-RFC)

A plugin opts into mutation by implementing a new capability interface,
probed exactly like `RootLister` (FDR 0014) and the protocol interfaces
(`ProtocolCapturePlugin`). Plugins with no meaningful write surface
simply don't implement it. **This signature is illustrative, not
normative** — the prototype exists to settle it:

```go
// NodeMutator is the optional write capability: create, update, or
// delete a single addressable node in the plugin's tree. The read-side
// sibling is RootLister (FDR 0014). A plugin implements it only when its
// scheme supports mutation; the file plugin (write/mkdir/rm) and caldav
// (PUT/MKCALENDAR/DELETE) are the first candidates.
type NodeMutator interface {
    Plugin

    // CreateNode creates a new node at uri with the given body. For a
    // container type the body MAY be empty (mkdir / MKCALENDAR); for a
    // leaf it is the object bytes (an .ics, a file's content). It is an
    // error if uri already exists (create is not upsert — see UpdateNode).
    CreateNode(ctx context.Context, uri *url.URL, body io.Reader, typ string) error

    // UpdateNode replaces the body of an existing leaf at uri. It is an
    // error if uri does not exist. Containers are not "updated" (their
    // children are mutated individually).
    UpdateNode(ctx context.Context, uri *url.URL, body io.Reader) error

    // DeleteNode removes the node at uri. Deleting a container removes its
    // subtree (the plugin decides whether that is recursive or refused on
    // non-empty — an open question below).
    DeleteNode(ctx context.Context, uri *url.URL) error
}
```

Node addressing reuses FDR 0014's URI scheme verbatim: a mutation targets
the same `*url.URL` a `ListRoots`/`resources/read` walk surfaces, so the
read and write axes share one address space. The `typ` on create is a
`NodeType.Tag` from the plugin's declared `Types()` (FDR 0014) — the
plugin validates that it can create a node of that type.

CUD is **not** receipt-based. Capture/restore operate on whole receipts;
CUD mutates a single live node with no blob store and no receipt
involved. This is the same separation FDR 0015 draws for reads
(structure traversal vs. capture's body-fetch).

### Layer 2 — MCP tool binding + permission gating

The `mcp` server (FDR 0015), today resource-only, gains **tools** for
plugins that implement `NodeMutator`. The mapping:

| MCP tool | Maps to |
|---|---|
| `describe_node_types()` | `RootLister.Types` + `BodyDescriber.DescribeBodies` |
| `list_nodes(uri?)` | the configured roots (no uri) / `resources/read` container (uri) |
| `read_node(uri)` | `resources/read` (#85) |
| `create_node(uri, body, type)` | `NodeMutator.CreateNode` |
| `update_node(uri, body)` | `NodeMutator.UpdateNode` |
| `delete_node(uri)` | `NodeMutator.DeleteNode` |

`list_nodes` and `read_node` expose the read tree as **tools**, because a
client may render only tools, not MCP resources (the claude.ai web UI;
circus#29). They make the tree browsable there — `list_nodes()` for the
entry points, `list_nodes(uri)` to descend, `read_node(uri)` to read a leaf
— so a human can discover the node URI the write tools need. `list_nodes()`
with no uri surfaces the configured **roots themselves** (each a container
to descend into), mirroring the `list` command's no-arg listing. This
deliberately differs from `resources/list`, which returns the roots'
*children*: when a root is itself a calendar (a per-calendar account),
listing its children would flatten every event into the entry-point listing
(circus#29). `read_node` and `list_nodes(uri)` are thin
wrappers over the `Resources.ReadResource` handler; the no-uri `list_nodes`
renders the roots directly (no plugin call). All read-only ⇒ `allow`.

`describe_node_types` is read-only schema discovery: it answers "which
plugin takes which node types and what their payloads are" so an agent can
call `create_node` without guessing the type tag or body. It reports each
scheme's node types (tag, container/leaf, mimetype, writable) and — for
writable types — the accepted body formats plus a concrete **example**,
via an optional `BodyDescriber` capability (caldav describes its object
leaf: raw `.ics` or the `{component, event|task}` JSON, with a sample
VEVENT). A formal per-type **JSON Schema** is a deliberate future addition
(the example is the pragmatic first cut). It is always advertised (even
without a mutator) and classifies read ⇒ `allow`.

Each tool is annotated with its read/destructive classification from a
shared `internal/mcp_tool_perms` classifier (the #102 ask, mirroring
dodder's `mcp_tool_perms` single-source-of-truth). The **same**
classifier feeds the clown PreToolUse hook decision table
(`internal/claude_hooks`): the three CUD tools classify as **destructive
⇒ `ask`** (all mutate live state), while the existing
`resources/list`/`read` stay read-only ⇒ `allow`. This is the trigger
condition #102 was scaffolded for — adding the decision table becomes a
localized edit, not new plumbing.

A `NodeMutator`-less plugin contributes no tools, exactly as a
`RootLister`-less plugin contributes no resources. The server stays
launch-with-no-args (FDR 0015): tools operate on the same
config-aggregated roots resources already expose.

### Prototype: caldav (pre-RFC API shakeout)

The prototype lands in **caldav**, for reasons the codebase already
makes the case for:

- **Worked prior art.** bob's ~18 CUD tools (FDR 0013) are a reference
  for the exact operations — create/update/complete/delete VEVENT/VTODO
  — so the prototype proves the *interface shape*, not unknown CRUD
  semantics.
- **Raw verbs exist.** caldav already PUTs `.ics` (`restore.go`);
  MKCALENDAR arrives via #77; DELETE is one more WebDAV verb on the same
  client. `CreateNode`/`UpdateNode`/`DeleteNode` are thin wrappers over
  verbs the plugin will already have.
- **Real tree-addressing.** caldav implements `RootLister`, so
  node-addressed mutation (mutate the node at *this* `caldav://…/x.ics`
  URI) genuinely exercises the address space — unlike the flat file
  plugin.
- **No live server needed for the shakeout.** caldav's `memstore_test.go`
  harness lets the prototype drive create→update→delete against an
  in-memory CalDAV double, so the API can be settled without risking a
  real calendar.

The file plugin (`write`/`mkdir`/`rm`) is the intended **second**
implementer — the one whose job is to confirm the verb set and
node-addressing are not caldav-shaped (a `testing`-promotion gate),
deferred to when the pre-RFC API is lifted into the RFC.

## Examples

    # An MCP client (agent) discovers a calendar, then mutates it.
    # resources/read surfaces the tree (FDR 0015, unchanged):
    #   caldav://dav.host/dav/me/work/  → [ {uri:.../event1.ics, ...} ]

    # create a new event (destructive ⇒ clown hook asks first)
    create_node(
      uri  = "caldav://dav.host/dav/me/work/new-standup.ics",
      type = "caldav-object-v1",
      body = "BEGIN:VCALENDAR…BEGIN:VEVENT…END:VEVENT…END:VCALENDAR",
    )

    # update its body
    update_node(
      uri  = "caldav://dav.host/dav/me/work/new-standup.ics",
      body = "BEGIN:VCALENDAR… (revised) …END:VCALENDAR",
    )

    # delete it
    delete_node(uri = "caldav://dav.host/dav/me/work/new-standup.ics")

Each tool call surfaces to the clown PreToolUse hook, which classifies
all three as destructive and emits `ask` — the agent must get user
approval before the mutation reaches the live server.

## Limitations

- **Not receipt-based.** CUD mutates one live node; it does not write or
  read a capture receipt. Capturing the post-mutation state is a separate
  `capture` invocation.
- **No transactions / batching.** Each verb is one node. A multi-object
  atomic change (create a calendar *and* its events as a unit) is out of
  scope for the prototype; batching is a tuning lever below.
- **No diff-then-apply.** CUD does not compute or apply a tree delta;
  reconciling a captured receipt against a live tree by mutating the
  difference is explicitly future work (and interacts with #18's
  cross-family diff).
- **Prototype is caldav-only.** Until the file plugin (or another)
  implements `NodeMutator`, the interface is validated against exactly
  one shape and remains pre-RFC.
- **MKCALENDAR dependency.** Container creation for caldav rides on #77;
  until that lands, `CreateNode` for a `caldav-calendar-v1` container is
  unimplemented (leaf create/update/delete is independent of it).

## Tuning Levers

| Lever | Current | Rationale | Change signal |
|---|---|---|---|
| CUD permission class | all three destructive ⇒ `ask` | every verb mutates live state; safe default is to gate all | a verb proves reliably non-destructive (e.g. idempotent create against an empty target) and the `ask` friction is unwanted |
| create semantics | strict create (error if exists) | distinguishes create from update; matches WebDAV `If-None-Match` intent | callers routinely want upsert and the create/update split adds friction |
| delete on non-empty container | open (refuse vs. recurse) | unsettled — see Open Questions | the prototype's first non-empty-collection delete forces the call |
| batching | one node per call | simplest prototype surface; matches one MCP tool call = one mutation | agents issue many sequential single-node calls where one batch tool would cut round-trips |

## Settled (prototype decisions)

- **Create vs. upsert → strict.** `create_node` errors if the node exists
  (caldav PUT `If-None-Match: *`); `update_node` is the explicit overwrite
  and errors if the node is absent (`If-Match: *`). Atomic preconditions, no
  racy GET-precheck. (Tuning lever if upsert friction shows up.)
- **Body typing → validate, dual-format.** caldav accepts both raw
  iCalendar AND the `objectView` JSON `resources/read` returns (symmetric
  read/write round-trip), normalized to `.ics` via the `ical` writers and
  rejected if it parses as neither. (Diverges from capture's opaque-blob
  stance, FDR 0013 — CUD is live mutation, not snapshotting.)
- **Scope → leaf-first.** caldav-object leaves only; creating a
  `caldav-calendar-v1` container errors (MKCALENDAR, #77). Containers are on
  the v1 docket — the interface already carries `typ` so container create
  slots in when #77 lands.
- **End-to-end → covered.** `zz-tests_bats/mcp.bats` drives create→update→
  delete through `cutting-garden mcp` over the stdio transport against the
  caldav testserver (dependent mutations serialized as one tool call per
  invocation, since a single connection's requests dispatch concurrently),
  plus the hook gating a CUD tool `ask`.
- **MCP tool prefix → confirmed.** clown preserves the hyphen in a plugin
  name (observed live: `mcp__plugin_clown-builtin-jobs_jobs__…`), and
  cutting-garden's plugin and stdio-server names are both "cutting-garden",
  so the hook's `mcp__plugin_cutting-garden_cutting-garden__` prefix is
  correct. (The matcher tolerance + the destructive annotation remain as
  independent fallbacks.)

## Open Questions

- **Delete recursion.** Does `DeleteNode` on a *container* refuse when
  non-empty (safe) or remove the subtree (convenient)? Unforced until
  container delete lands (leaf delete is unaffected).
- **Where the RFC line falls.** This FDR + the caldav prototype are
  pre-RFC. The RFC (when the file plugin joins) must decide whether CUD
  is a standalone interface or folds into a broader "tree mutation"
  protocol alongside the capture protocol (RFC 0002).

## More Information

- [FDR 0014](0014-plugin-root-traversal.md) — `RootLister`, the read-side
  traversal primitive whose URI address space CUD reuses.
- [FDR 0015](0015-mcp-resource-server.md) — the `mcp` server CUD adds
  tools to; its "No tools" Non-Goal is the gap this closes.
- [FDR 0013](0013-caldav-plugin.md) — records bob's ~18 CUD tools and the
  deliberate "No live mutation tools" Non-Goal CUD reverses; the
  prototype plugin.
- amarbel-llc/cutting-garden#102 — the `mcp_tool_perms` hook-parity ask
  that triggers exactly when these write tools land.
- amarbel-llc/cutting-garden#77 — caldav MKCALENDAR on restore; the
  container-create dependency for `CreateNode`.
- amarbel-llc/cutting-garden#85 — leaf body-fetch on `resources/read`; the
  read-side counterpart of CUD's body handling.
- [RFC 0002](../rfcs/0002-capture-plugin-protocol.md) — the capture
  protocol the eventual CUD RFC must decide whether to fold into.

---
*Drafted by Clown 0.3.12+e27f901 ([commit](https://github.com/amarbel-llc/clown/commit/e27f9018663d8af8c5e523962063d9195883bf46))*
