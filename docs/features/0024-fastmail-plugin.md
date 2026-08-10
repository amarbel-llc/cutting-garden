---
status: proposed
date: 2026-08-10
promotion-criteria: |
  Promote to `experimental` once Slice 1 lands: `plugins/fastmail/` speaks
  JMAP against a real account, declares the node types below, and
  `cg list`/`cg mcp` traverse `account → mailbox/tag tree → thread →
  message` with the read-only facets rolling up and drilling down. Promote
  to `testing` once Slice 2's `organize` tag-write round-trips against a
  real account (add/remove a tag across a set of threads, fanned out to
  member messages) — which is gated on the framework growing `write:many`
  apply (see §Slicing). Promote to `accepted` once the plugin ships in the
  default binary and the tag-membership write contract has gone two weeks
  without a correctness lever moving.
---

# Fastmail (JMAP) plugin

## Problem Statement

cutting-garden can browse and organize several substrates over MCP and the
`organize` command — a calendar (caldav), a feed reader (nebulous), a
filesystem — but a Fastmail *mailbox* is not one of them. For a user whose
mail is organized as a deep tree of hundreds of overlapping **tags** (Fastmail
"labels mode"), the durable pain is not archival — it is *reorganizing*:
bulk-retagging threads across that ontology, which the Fastmail web UI makes
tedious. This plugin makes a Fastmail account a first-class `organize` and
`mcp` substrate over **JMAP**, so the tag tree can be traversed, faceted, and
(Slice 2) rewritten the same way every other substrate can. Archival
capture/restore is a deliberate *secondary* goal, deferred to its own record.

## Interface

### Scope and API

The plugin speaks **JMAP** (RFC 8620 core + RFC 8621 mail) exclusively — never
IMAP. JMAP is Fastmail-native, carries mail *and* the horizon data types
(contacts, notes, masked-email) under one session, and — decisively for a
traversal/organize tool — hands back every field a facet needs
(`mailboxIds`, `keywords`, `receivedAt`, `from`, `threadId`) in a single
`Email/get`, so facet values are always in hand at list time with no per-node
fetch (RFC 0012's cheapness rule).

**v1 is Mail only.** Contacts, notes, and masked-email are on the horizon
behind the same JMAP session; a sibling **addy.io** plugin (which will lean on
the same Fastmail tags + Sieve filters) is planned separately. Calendar is
*not* in scope — the existing caldav plugin (FDR 0013) owns it.

### Scheme, credentials, addressing

The plugin claims the single **`fastmail`** URI scheme. It is vendor-named on
purpose: the horizon (masked-email, notes) is Fastmail-specific JMAP, not
portable standard JMAP. The *client* is kept internally generic-JMAP so a
`jmap:` sibling for other JMAP servers is a later possibility, but the shipped
scheme is `fastmail`.

Fastmail JMAP auth is a **bearer token** (an API token / app password),
presented as `Authorization: Bearer …` to the fixed session endpoint
`https://api.fastmail.com/jmap/session`. Because the API host is fixed, the
URL's "host" slot names a **config account**, not a hostname — a deliberate
divergence from caldav/jira, where host is a real endpoint. Credentials resolve
per RFC 0007: `[[fastmail.accounts]]` entries carry the token from a `token_env`
(e.g. `FASTMAIL_API_TOKEN`). There is no URL-userinfo path (bearer tokens do
not fit `user:pass@host`). Configured accounts double as credential-free
`RootProvider` roots, so no-argument `cg list` / `cg mcp` enumerate them.

Argument forms:

    fastmail:                                     all configured accounts' roots
    fastmail://personal/                          one account's mailbox/tag tree root
    fastmail://personal/area/finance/receipts/    a tag (mailbox) by name-path
    fastmail://personal/area/finance/receipts/?thread=T5501                       a thread
    fastmail://personal/area/finance/receipts/?thread=T5501&email=msg-9f2a        one message
    fastmail://personal/area/finance/receipts/?thread=T5501&email=msg-9f2a&raw=1  its raw bytes

The mailbox name-path stays as readable path segments; the thread, email, and
raw ids ride in **query parameters**, not trailing path segments. An opaque JMAP
id is otherwise indistinguishable from a child-mailbox name as a trailing
segment, so query discriminators keep URI classification pure — no network
round-trip needed to place a URI in the tree.

### The tree

Fastmail runs in **labels mode**: JMAP Mailboxes act as multi-assignment
labels, so a single message can carry several at once, and the "folder tree" is
really a **tag hierarchy** (mailboxes nest via `parentId`, path up to several
levels deep). The plugin exposes this as a tree that is *both* descendable and
faceted:

- **`account → mailbox/tag tree`** — the account root lists its top-level
  mailboxes; each mailbox container lists its child mailboxes (nested tags) and
  the threads tagged at that exact level. Descending narrows by tag.
- **`mailbox → thread`** — the **thread is the primary organizing unit**, not
  the individual message. This matches how Fastmail is actually used: labels
  are applied per-conversation. A thread's facet values are derived as the
  **union / any-of** across its member messages (see Facets).
- **`thread → message`** — messages are the drill-down. A message is where the
  structured `Email` fields and the raw bytes live, and the level at which a
  precise (non-thread-wide) edit can be made.

Mailbox membership is *also* a facet dimension (`tag`), so the flat-corpus
power — "threads tagged `area/finance` **and** `from=acme` **and** `year=2026`"
— is available via AND-ed facet filters without descending, exactly the shape
mail actually has (a thread has a tag set *and* a date *and* senders, none
subordinate).

### Node types

Declared via `Types()`, hyphenated and horizontally versioned (FDR 0018):

| Tag (`cutting_garden-fastmail-…`) | Kind | Holds |
|---|---|---|
| `mailbox-v1` | container | own JMAP Mailbox attributes (id, name, path, parentId, role) + refs to child mailboxes and to threads tagged here |
| `thread-v1` | container | JMAP `threadId`; refs to its member `email` nodes; union/any-of facet values |
| `email-v1` | container | **body = structured JMAP `Email` JSON**; refs to `raw` + `attachments/*` |
| `attachment-v1` | container | attachment metadata (name, type, size) + a `content` ref to the immutable bytes |
| (raw message) | leaf | verbatim RFC 5322 bytes, stamped `message/rfc822` |
| (attachment content) | leaf | raw binary, stamped its IANA media type |

The **email blob is shared/deduped** across every tag container that references
its thread; retagging changes *which containers point at a thread*, never the
message bytes. `raw` and attachment `content` are immutable, so they
content-address perfectly and are fetched lazily — only on an explicit read of
that child, never during listing or faceting.

The message-container shape, in the repo's hyphence grammar (synthetic
tags/addresses):

    # a tag (mailbox) container: child mailboxes + threads tagged here
    ---
    - receipts   < @blake2b256-mbx7q…  !cutting_garden-fastmail-mailbox-v1@sig
    - T5501      < @blake2b256-thr5501… !cutting_garden-fastmail-thread-v1@sig
    ! cutting_garden-fastmail-mailbox-v1
    ---
    {"id":"Mb17","name":"finance","parentId":"Mb02","path":"area/finance","role":null}

    # a thread container: its member messages; facet values are union/any-of
    ---
    - msg-9f2a   < @blake2b256-eml9f2a… !cutting_garden-fastmail-email-v1@sig
    - msg-3c7e   < @blake2b256-eml3c7e… !cutting_garden-fastmail-email-v1@sig
    ! cutting_garden-fastmail-thread-v1
    ---
    {"threadId":"T5501"}

    # an email container: body = structured JMAP Email; children = raw + attachments
    ---
    - raw            < @blake2b256-rfc822a1… !message/rfc822
    - attachments/2  < @blake2b256-att2pdf…  !cutting_garden-fastmail-attachment-v1@sig
    ! cutting_garden-fastmail-email-v1
    ---
    {"from":[{"email":"billing@acme.example","name":"Acme Billing"}],"hasAttachment":true,"keywords":{"$seen":true},"mailboxIds":{"Mb17":true,"Mb90":true},"receivedAt":"2026-07-14T09:12:03Z","subject":"Your July receipt","threadId":"T5501"}

### Facets

Declared per RFC 0012; on a thread they are derived across member messages:

| Dimension | Write | Kind | Derivation (thread-level) |
|---|---|---|---|
| **`tag`** | **many** | labelled, open | union of member messages' user-mailbox (`role == null`) membership — **the primary organize target** |
| `read` | one | categorical, closed bool | `unread` if **any** message lacks `$seen`, else `read` |
| `flagged` | one | categorical, closed bool | `flagged` if **any** message has `$flagged` |
| `folder` | none | categorical, closed | union of *role*-mailbox membership (inbox/archive/sent/junk/trash); archive is the silent default, `inbox` shown when present |
| `year` | none | numeric-bucket | bucket of the thread's newest `receivedAt` |
| `from` | none | labelled, open | union of member senders (top-N capped) |
| `hasAttachment` | none | categorical, closed bool | any member has an attachment |

**Role mailboxes are read-only.** The `tag` write-dimension is scoped to user
tags (`role == null`), so an `organize` edit can never move a message to Trash
or un-archive it as a side effect. Inbox↔archive is exposed *read-only* through
the `folder` facet (and, once the deferred `%`-atom grammar of cutting-garden#218
lands, a `%inbox` read-only box atom); a *writable* inbox/archive toggle is a
future slice, not v1.

Counts roll up recursively through the mailbox tree (progressive disclosure):
`area/` advertises the mass of everything beneath it so you drill toward it.
Because a thread can carry several tags, these are **membership counts, not
distinct-thread counts** — a thread under both `area/finance` and
`area/finance/receipts` counts under each, so a subtree sum can exceed the
distinct-thread total. Summaries say so rather than implying otherwise.

### Writes (`organize`) and the framework dependency

The `organize` payoff is bulk tag editing. The plugin declares `tag` as
`write:many` and provides a `FacetWriteApplier` that builds the JMAP
`Email/set mailboxIds` patch. Per RFC 0015 §`write:many`, a thread's presence
under a heading *is* its membership: add = the thread's line appears under a new
tag heading; remove = its line is dropped from a heading (removing that one tag
only, un-gated because a mailbox-membership change is a soft mutation, not an
object delete); move = remove-here + add-there. Because the unit is the thread,
every such write **fans out to all member messages** in one `Email/set` — which
is why the plugin advertises **`bulk-atomic`** (RFC 0017): Fastmail transacts
the batch. `read`/`flagged` are `write:one` toggles over `$seen`/`$flagged`.

**This is blocked on a framework feature, not plugin code.** The organize apply
engine today implements `write:one` only; `internal/organize/apply.go` stubs
`FacetWriteMany` as "out of scope in this slice." So the tag-membership write —
the spine of this plugin — requires the engine to grow generic `write:many`
apply (clone-per-heading membership diff). This plugin is the motivating
consumer for that work, alongside nebulous's `user_tag`. See §Slicing.

**Excluded from writes**, deliberately: sending, draft creation,
delete/expunge, and message-content edits. Those are compositional/destructive
and belong to the interactive Fastmail MCP, mirroring how the caldav plugin
declines live-mutation tools.

### Slicing

- **Slice 1 — read-only live tree (unblocked; the natural start).** JMAP
  session + bearer-token accounts + `RootProvider`/`RootLister` traversal
  (`account → mailbox/tag tree → thread → message`) over `cg list` and
  `cg mcp`, with all facets **read-only**. Independently useful on its own.
- **Slice 2 — `organize` tag writes (blocked on the framework).** Land generic
  `write:many` apply in `internal/organize/` (with Fastmail `tag` + nebulous
  `user_tag` as the motivating consumers), then the plugin's `FacetWriteApplier`
  fans `Email/set mailboxIds` out to a thread's messages; add `read`/`flagged`
  `write:one`.
- **Slice 3 — capture/diff (separate follow-on FDR).** Protocol/merkle capture
  (the jira analog: the node shapes above serialized, immutable `raw`/attachment
  blobs deduped, JMAP `state` + `Email/changes` as the incremental oracle);
  restore deferred. Out of scope for *this* record — archive is secondary.

## Examples

    # progressive disclosure: the account root shows top-level tags with rolled-up counts
    $ cutting-garden list --facets fastmail://personal/
    tag:   area/… 6.1k   payee/… 2.3k   _/… 1.2k   (+10 more)
    # membership counts (a thread under several tags counts under each)

    # drill toward the mass, then narrow by an independent axis
    $ cutting-garden list --facets --filter from=acme fastmail://personal/area/finance/receipts/
    year:  2026 41   2025 22   2024 9
    read:  read 70   unread 2
    # threads tagged area/finance/receipts AND from acme, by year

    # descend to threads, then a thread to its messages
    $ cutting-garden list fastmail://personal/area/finance/receipts/
    T5501   "Your July receipt"   from=acme  year=2026  read
    …
    $ cutting-garden list "fastmail://personal/area/finance/receipts/?thread=T5501"
    msg-9f2a   "Your July receipt"   from=acme
    # descend a message → its structured body (read) + the raw-bytes child
    $ cutting-garden list "fastmail://personal/area/finance/receipts/?thread=T5501&email=msg-9f2a"
    raw

    # MCP: a container read carries the facet summary inline (FDR 0021)
    #   resources/read fastmail://personal/area/finance/receipts/
    #   → { "nodes": [ {"uri":".../T5501","type":"cutting_garden-fastmail-thread-v1",…} ],
    #       "facets": { "year": {...}, "read": {...}, "from": {...}, "complete": true } }

    # organize (Slice 2 — requires framework write:many apply): bulk-retag by moving
    # thread lines between tag headings; the applier fans Email/set out to each message
    $ cutting-garden organize --group-by tag fastmail://personal/area/career/

## Limitations

- **v1 is read-only** (Slice 1). Tag writes (Slice 2) are gated on the organize
  framework growing `write:many` apply; until then `organize` over this
  substrate can traverse and group but not commit.
- **Thread-level edits are union/any-of.** A thread whose messages are
  *non-uniformly* tagged surfaces the union, and a thread-level "remove tag"
  clears it from every member; per-message asymmetry is only reachable by
  drilling to the message level.
- **Role mailboxes are read-only** and excluded from the `tag` write-dimension;
  inbox/archive triage and delete/expunge are not writable here. drafts,
  scheduled, snoozed, and memos (Fastmail Notes) are excluded from the v1 tree.
- **No sending, no drafts, no content edits, no restore.** Snapshot/organize,
  not composition; those stay with the interactive Fastmail MCP.
- **Membership counts, not distinct counts.** Multi-tag threads inflate subtree
  sums; summaries mark this rather than implying a distinct-thread total.
- **Slice 1 counts are direct, not recursive.** Each mailbox node carries its own
  `threads`/`emails` count (JMAP `totalThreads`/`totalEmails` — direct
  membership). The **recursive subtree rollup** (`area/` showing the sum of
  everything beneath it) and the account-root `list --facets` account-wide
  aggregate are **deferred**: `FacetCounts` computes a single mailbox's threads
  only, and the account root reports `ok=false`. The rolled-up
  progressive-disclosure examples above (e.g. `list --facets fastmail://personal/`)
  describe the eventual design, not Slice 1. Tracked as a follow-up.
- **Calendar is out of scope** — owned by the caldav plugin (FDR 0013).

## Tuning Levers

| Lever | Current | Rationale | Change signal |
|---|---|---|---|
| listing unit | thread (drill to message) | matches Fastmail's per-conversation tagging and the user's mental model | users routinely need per-message ops without drilling |
| account root children | mailboxes only (no unscoped email dump) | emails must be scoped by a tag or filter; bounds output | a genuine need for an account-wide flat listing appears |
| page size | JMAP default, newest-first | bounds a large tag (some hold thousands) | clients consistently page deep and want a bigger window |
| top-N per open facet (`from`) | capped with "(+N more)" | `from` is high-cardinality; must not dwarf output | clients consistently need the full sender distribution |
| numeric bucket | year | coarse axis mail wants first | users consistently need month granularity |
| tag write scope | user mailboxes only (`role == null`) | prevents trash/archive-by-retag | a deliberate writable inbox/archive toggle is wanted |

## More Information

- FDR 0013 — caldav plugin: the network + RFC 0007 accounts +
  `RootProvider`/`RootLister` + `FacetWriteApplier` reference sibling; owns
  calendar so this plugin does not.
- FDR 0019 — jira plugin: the protocol-capture graph-service precedent Slice 3
  will follow (immutable blobs, per-object dedup, a change oracle).
- FDR 0021 / RFC 0012 — faceted progressive disclosure and the normative facet
  contract (including `write:none|one|many` and the write-descriptor extension).
- FDR 0023 / RFC 0015 — `organize` and the organize document dialect; RFC 0015
  §`write:many` is the normative membership-diff semantics this plugin's tag
  writes rely on, and `internal/organize/apply.go`'s `FacetWriteMany` stub is
  the Slice 2 blocker.
- FDR 0018 — unified type namespace: node-type naming.
- RFC 0007 — config subsystem: `[[fastmail.accounts]]` and credential
  resolution.
- RFC 0017 — bulk mutation: the `bulk-atomic` posture the JMAP `Email/set`
  fan-out advertises.
- The interactive `Fastmail` MCP server (email/contacts/calendar/notes) — the
  live-mutation sibling, the same relationship sisyphus has to the jira plugin
  and bob's caldav has to the caldav plugin.
