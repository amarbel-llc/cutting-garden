---
status: testing
date: 2026-08-10
promotion-criteria: |
  Promote to `experimental` once Slice 1 lands: `plugins/fastmail/` speaks
  JMAP against a real account, declares the node types below, and
  `cg list`/`cg mcp` traverse `account → mailbox/tag tree → thread →
  message` with the read-only facets rolling up and drilling down. Promote
  to `testing` once Slice 2's `organize` tag-write round-trips against a
  real account (add/remove a tag across a set of threads, fanned out to
  member messages) — which is gated on the framework growing `write:many`
  apply (see §Slicing). MET 2026-09-23 (fastmail tags slice 1): the
  membership-write engine carries the thread tag set end to end, pinned by
  the whole-document round-trip vectors of `zz-tests_bats/organize_fastmail.bats`
  against the fastmail testserver; the one wire form the testserver could not
  vouch for — a creation id used as a patch-object KEY — was verified against
  the real Fastmail API on 2026-09-22 (§Writes). The live-inbox triage run is
  the user-driven UAT gate after merge. Promote to `accepted` once the plugin
  ships in the default binary and the tag-membership write contract has gone
  two weeks without a correctness lever moving.
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
rewritten (a thread's tag set, through `organize`) the same way every other
substrate can. Archival
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
per RFC 0007: `[[fastmail.accounts]]` entries carry the token from the
environment variable their `password_env` names (e.g. `FASTMAIL_API_TOKEN`).
There is no URL-userinfo path (bearer tokens do not fit `user:pass@host`).
Configured accounts double as credential-free `RootProvider` roots, so
no-argument `cg list` / `cg mcp` enumerate them. An account MAY set
`session_url` (RFC 0007 `Account.SessionURL`, `cutting-garden-config(5)`) to
replace the fixed session endpoint — how the bats lane points
`fastmail://test/` at its in-memory JMAP testserver
(`cmd/cutting-garden-fastmail-testserver`); every later call follows the
`apiUrl` the returned session advertises.

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
  precise (non-thread-wide) edit would be made — not writable yet (tag writes
  are thread-level; see Limitations).

Mailbox membership is *also* a facet dimension (the thread's `tags`), so the
flat-corpus power — "threads tagged `payee-one_medical` **and** `from=acme`
**and** dated 2026" — is available via AND-ed facet filters without
descending, exactly the shape mail actually has (a thread has a tag set *and* a
date *and* senders, none subordinate).

**Label → tag.** A user-label mailbox's tag is the dodder-hyphen join
(RFC 0019 §6) of its name-path from its NEAREST BARE ancestor onward, where a
segment is bare iff it does not start with `-`: `area/-career/proj-x/-msft`
→ `proj-x-msft`, `payee/-one_medical` → `payee-one_medical`,
`zz-archive/proj/-24-t/-10x` → `proj-24-t-10x`. Bare ancestors above that
point are tags *on the tag* (a tag graph), never atoms of the object. A
mailbox named exactly `_` is an ergonomic grouping root only: it never renders
as a tag, and its children are plain tags (`_/req-others` → `req-others`).
Role mailboxes contribute no label tag.

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

### Fields and facets

The plugin declares its fields ONCE through the unified field-codec model
(FDR 0025, `DescribeUnified` in `plugins/fastmail/unified.go`); the facet
declaration (RFC 0012), the facet-write mapping and the box-atom presentation
all derive from it. On a thread every value is derived across its member
messages:

| Field | Kind | Flags | Derivation (thread-level) |
|---|---|---|---|
| **`tags`** | `FieldTag` (interpreter `dodder-hyphen`) | groupable, multi-valued, **writable** — the thread type's designated tag set | the union of the members' label tags (§The tree) **plus** the state tags below — **the primary organize target** |
| `from` | labelled, open | groupable, multi-valued, inline box atom, read-only | union of member senders (top-20 capped; a capped summary reports `complete: false`) |
| `date` | `FieldDate` | groupable, read-only, listing field (not a box atom) | ISO day of the thread's newest `receivedAt`; `--group-by date=(year\|month\|day)` coarsens it by prefix (#230) |
| `has_attachment` | categorical, closed `yes`/`no` | groupable, read-only (no listing counterpart) | any member has an attachment |
| `subject` | text | the box trailer, read-only | the thread's display name (its subject) |

The mailbox type carries its direct `threads`/`emails` counts; the email type
carries `from`/`date`/`subject` for drill-down, none of them groupable. The
earlier `read`, `flagged`, `folder` and `year` dimensions are retired:
`read`/`flagged`/`folder` are subsumed by the state tags, `year` by `date`.

**State tags.** Beside its label tags, a thread's tag set carries four
`_`-prefixed state tags derived from substrate state, each present iff ANY
member satisfies it:

| Tag | Present when |
|---|---|
| `_inbox` | a member is in the `inbox` role mailbox |
| `_unread` | a member lacks `$seen` |
| `_flagged` | a member has `$flagged` |
| `_trash` | a member is in the `trash` role mailbox |

They are literal tags to every interpreter (RFC 0019 §7) and sort ahead of the
lowercase label tags. `sent` and `junk` are never emitted.

**Role mailboxes are writable through the state tags.** Earlier revisions kept
role mailboxes read-only; inbox triage is now the point. Writing `_inbox` or
`_trash` adds or removes that role mailbox on every member, and `_unread` /
`_flagged` write `$seen` / `$flagged`. `_sent`, `_junk`, `_archive`, and any
other `_` tag are refused by name as a bad request, before any request is
issued. Archive is never named directly — it is the fallback of the archive
invariant (§Writes).

Counts are designed to roll up recursively through the mailbox tree
(progressive disclosure; today they are direct — see Limitations):
`area/` advertises the mass of everything beneath it so you drill toward it.
Because a thread can carry several tags, these are **membership counts, not
distinct-thread counts** — a thread under both `area/finance` and
`area/finance/receipts` counts under each, so a subtree sum can exceed the
distinct-thread total. Summaries say so rather than implying otherwise.

### Writes (`organize`) — implemented (fastmail tags slice 1)

The `organize` payoff is bulk tag editing, and it rides the framework's
membership-write engine unchanged: the plugin implements `NodeMutator.PatchNode`
(FDR 0020), `FacetWriteDescriber` (derived from the unified declaration, so
`tags` is a `write:many` dimension) and `MembershipWriteApplier`, whose patch
is the thread's COMPLETE new tag set, `{"tags": [...]}`, as the dodder-hyphen
interpreter's `Complete` resolved it (RFC 0019 §6.2). Per RFC 0015, a thread's
presence under a heading *is* its membership and a tag atom typed into or
deleted from its box is a membership edit; `--group-by _inbox` is an ordinary
namespace grouping whose G10a root heading holds every inbox thread, so moving
a line above `# _inbox` (or deleting it) removes exactly `_inbox`.

`PatchNode` accepts thread URIs only (a mailbox, email or raw node is a bad
request). It re-reads the live members and mailbox tree, diffs the requested
set against the presented one, and issues **ONE JMAP request** per thread:

- **Thread-level fan-out.** Every added or removed tag is written to EVERY
  member message through `Email/set` patch objects (RFC 8620 §5.3) — a label
  tag as its mailbox id (`mailboxIds/<id>: true|null`; removing a tag clears
  every member mailbox joining to it), `_inbox`/`_trash` as the role mailbox,
  `_unread` removed as `keywords/$seen: true` (added: cleared), `_flagged` as
  `keywords/$flagged`. Keyword writes are unconditional, so a thread whose
  members disagreed converges.
- **Archive invariant.** JMAP requires every message to be in at least one
  mailbox, so a member the patch would leave in none lands in the `archive`
  role mailbox instead. Removing `_inbox` from a still-labelled thread
  therefore just drops the inbox; from an unlabelled one it archives. Adding
  `_inbox` to a member in Archive takes it out of Archive.
- **Unknown tags are created.** A tag no mailbox realizes gets a
  `Mailbox/set create` in the SAME request, ahead of the `Email/set`, and the
  member patches reference it by creation id used as a patch-object KEY
  (`"mailboxIds/#c1": true`), so the apply stays atomic. RFC 8620 does not
  spell out that key form, so it was verified against the real Fastmail API on
  2026-09-22: one request answered `created: {c1: {id: …}}` and
  `updated: {<id>: null}`, and the readback showed the message in both its
  original mailbox and the new one.
- **Placement of a created mailbox.** Every existing mailbox whose tag shares
  at least one leading `-`-separated segment with the new tag is a candidate:
  (a) if its tag is a proper prefix of the new tag at a `-` boundary, the new
  mailbox would be its `-`-continuation child; (b) otherwise a BARE sibling
  under its parent. Candidates of both kinds rank together by the number of
  shared leading segments — most wins, (a) beats (b) on a tie, a remaining tie
  goes to the lexically first tag — and with no candidate the mailbox is (c) a
  bare top-level one. So `payee-acme` becomes `-acme` under `payee`, while
  `proj-trips-26-10-hike` becomes a bare sibling of `proj-trips-26-09-yoga`
  (three shared segments) rather than a continuation under a bare interior
  `zz-archive/proj` (tag `proj`, one segment). The created mailbox always
  re-derives the requested tag under the join rule.

`PatchNode` reports `applied = ["tags"]` plus one `mailbox:<full/path>` entry
per created mailbox; a set that already matches the live one issues nothing and
reports an empty `applied`.

**Object ids are thread ids.** organize's default box id is the node URI's
host+path relative to the anchor, which cannot see a thread id (it rides in
the query), so every thread in a mailbox would collapse to one id. The plugin
implements the optional `NodeIDer` SDK capability instead: a thread under the
anchor mailbox gets its bare thread id (`T1`), a thread in a descendant mailbox
`<remaining/mailbox/segments>/<threadId>`; organize and `list -format espalier`
resolve every id through it, and a `NodeIDer` plugin whose distinct nodes
collide on one id is refused at generate.

The plugin does **not** advertise `bulk-atomic` (RFC 0017) yet: each thread's
patch is already one JMAP request, and the capability flag lands with the MCP
`bulk_mutate` wiring.

**Excluded from writes**, deliberately: sending, draft creation,
delete/expunge, and message-content edits. Those are compositional/destructive
and belong to the interactive Fastmail MCP, mirroring how the caldav plugin
declines live-mutation tools.

### Slicing

- **Slice 1 — read-only live tree (unblocked; the natural start).** JMAP
  session + bearer-token accounts + `RootProvider`/`RootLister` traversal
  (`account → mailbox/tag tree → thread → message`) over `cg list` and
  `cg mcp`, with all facets **read-only**. Independently useful on its own.
- **Slice 2 — `organize` tag writes (DONE 2026-09-23, fastmail tags slice 1,
  `docs/plans/2026-09-21-fastmail-tags-slice1.md`).** The framework's
  membership-write engine (native tags) carries the thread's `tags` set; the
  plugin fans `Email/set` out to a thread's messages, creates unknown tags with
  `Mailbox/set` in the same request, and folds read/flagged/inbox/trash into
  the writable state tags instead of separate `write:one` dimensions.
- **Slice 3 — capture/diff (separate follow-on FDR).** Protocol/merkle capture
  (the jira analog: the node shapes above serialized, immutable `raw`/attachment
  blobs deduped, JMAP `state` + `Email/changes` as the incremental oracle);
  restore deferred. Out of scope for *this* record — archive is secondary.

## Examples

    # progressive disclosure (the eventual recursive rollup — see Limitations)
    $ cutting-garden list --facets fastmail://personal/
    tags:  payee-… 2.3k   proj-… 6.1k   _inbox 40   (+10 more)
    # membership counts (a thread under several tags counts under each)

    # a mailbox's threads, narrowed by an independent axis
    $ cutting-garden list --facets --filter from=billing@acme.example fastmail://personal/area/finance/receipts/
    date:  2026-07-14 1   2026-06-12 1   …
    tags:  _unread 2   receipts 72
    # threads in that mailbox AND from acme, by day and tag

    # descend to threads, then a thread to its messages
    $ cutting-garden list -format json fastmail://personal/area/finance/receipts/
    {"uri":"fastmail://personal/area/finance/receipts/?thread=T5501","name":"Your July receipt","type":"cutting_garden-fastmail-thread-v1","tags":["receipts"]}
    …
    $ cutting-garden list "fastmail://personal/area/finance/receipts/?thread=T5501"
    msg-9f2a   "Your July receipt"   from=acme
    # descend a message → its structured body (read) + the raw-bytes child
    $ cutting-garden list "fastmail://personal/area/finance/receipts/?thread=T5501&email=msg-9f2a"
    raw

    # MCP: a container read carries the facet summary inline (FDR 0021)
    #   resources/read fastmail://personal/area/finance/receipts/
    #   → { "nodes": [ {"uri":".../T5501","type":"cutting_garden-fastmail-thread-v1",…} ],
    #       "facets": { "tags": {...}, "date": {...}, "from": {...}, "complete": true } }

    # organize inbox triage (the bats lane's synthetic fixture): one G10a root
    # heading, `_inbox` stripped as its placement tag, one box per thread id
    $ cutting-garden organize -group-by _inbox fastmail://test/Inbox/
    # _inbox

    - [T1 _unread payee-one_medical from="billing@example.com"] Your statement is ready
    - [T2 proj-x-msft req-others from="recruiter@example.com"] Interview loop
    - [T3 _flagged from="friend@example.com"] Lunch next week?
    - [T4 proj-24-t-10x from="team@example.com"] Project wrap-up
    # (envelope elided) move T1's line above `# _inbox` → T1 leaves the Inbox,
    # keeps payee-one_medical; delete `_unread` from a box → $seen on every
    # member; type `payee-acme` into a box → mailbox payee/-acme is created

## Limitations

- **Only the thread tag set is writable.** No per-message edit, no
  delete/expunge (`_trash` is the soft, reversible form), no direct Archive
  write (Archive is only the archive-invariant fallback), and `_sent`/`_junk`
  are refused. drafts, scheduled, snoozed, and memos (Fastmail Notes) are
  excluded from the tree.
- **Thread-level edits are union/any-of.** A thread whose messages are
  *non-uniformly* tagged surfaces the union, and a thread-level "remove tag"
  clears it from every member; per-message asymmetry is only visible by
  drilling to the message level, and not writable at all.
- **No tag normalization on the MCP path.** organize resolves a box edit
  through the interpreter's `Complete` before it reaches the plugin; an MCP
  `patch_node` hands the plugin its `tags` array verbatim, as the complete
  new set — the plugin refuses reserved `_` tags,
  empty tags and hyphen-leading tags, but otherwise takes the array as given,
  and a tag left out of it is removed.
- **New tags in one apply are placed independently.** Placement reads the
  mailbox tree as it stood BEFORE the apply, so new tags related by prefix to
  each other do not see one another: `foo-a` and `foo-a-b` introduced in the
  same document are each placed against the existing tree (here, with no
  `foo` relative, both as top-level mailboxes) rather than nesting `-b` under
  the newly created `foo-a`. Each still realizes its tag, and nothing moves
  it afterward; only a `foo-a-b` introduced in a LATER apply nests.
- **Duplicate labels.** Two mailboxes whose paths join to the same tag are
  legal in Fastmail; the read side shows the tag once, a removal clears both,
  but an ADD resolves the tag to the first in listing order.
- **The tag graph is not exposed.** Bare ancestors are tags on the tag
  (§The tree), but nothing lets a query or grouping follow them —
  `zz-archive` does not select the threads under its subtree transitively.
  Needs an RFC 0019 extension.
- **The apply summary shows the full-set diff.** A box edit's summary line is
  `tags=[-…-]{+…+}` over the whole set rather than the atoms the user touched,
  and created mailboxes appear only in `PatchNode`'s `applied`, not the
  summary (cutting-garden#260).
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
| date facet value | ISO day of the newest `receivedAt`, coarsened by prefix (`date=(year\|month)`) | one value serves every granularity (#230) | a per-member date axis is wanted |
| tag write scope | user labels + the four state tags `_inbox`/`_unread`/`_flagged`/`_trash`; other `_` tags refused | inbox triage without exposing Sent/Junk/Archive as editable | a need to write another role (e.g. junk-marking) appears |
| new-tag placement | rank candidates by shared `-` segments; continuation beats sibling on a tie; else top level | keeps a new tag beside its closest family, not inside a bare interior like `zz-archive/proj` | created mailboxes routinely land somewhere the user moves them from |

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
  writes rely on.
- FDR 0025 — the unified field-codec model the plugin's declaration uses.
- RFC 0019 — the tag interpreter contract: §6 `dodder-hyphen` (the label→tag
  join and `Complete`), §7 `_` literal (state tags).
- FDR 0020 — `NodeMutator.PatchNode` and its `applied` contract.
- `docs/plans/2026-09-21-fastmail-tags-slice1.md` — the slice that made the tag
  set writable (decisions D1–D7); its vectors are indexed in
  `docs/plans/2026-08-30-native-tags-vectors.md`.
- FDR 0018 — unified type namespace: node-type naming.
- RFC 0007 — config subsystem: `[[fastmail.accounts]]` and credential
  resolution.
- RFC 0017 — bulk mutation: the `bulk-atomic` posture the JMAP `Email/set`
  fan-out will advertise once the plugin implements `BulkMutator`.
- The interactive `Fastmail` MCP server (email/contacts/calendar/notes) — the
  live-mutation sibling, the same relationship sisyphus has to the jira plugin
  and bob's caldav has to the caldav plugin.
