# Fastmail URIs: path-based identity instead of query parameters

Date: 2026-11-16
Status: investigation only. No code changed. Facts are from reading the
tree at master c13bd05 and the docs it carries; anything not read or run is
marked **unverified**.
Asked by: the operator, via the coordinator. Related: cutting-garden#404
(`PatchKeysDescriber` / `PatchTarget`), RFC 0018 (URI templates), issue #79
(plugin backward compatibility through versioned or horizontal changes).

## 1. Where fastmail URIs are minted and parsed

All in `plugins/fastmail/url.go`; nothing else builds or splits one by hand.

- **Parse**: `classifyURI(u)` returns a `nodeRef` of five kinds from the host
  (the config account, not a hostname), the path (mailbox name-path as decoded
  segments) and three query keys: `thread`, `email`, and a bare `raw`.
  Classification is pure, no network (the file's own comment says so).
- **Mint**: `accountRootURI`, `mailboxURI`, `threadURI`, `emailURI`, `rawURI`.
  The mailbox path always ends in `/` (`setPathSegments`); the thread, email
  and raw forms add the query.

| kind | URI today |
|---|---|
| account root | `fastmail://personal/` |
| mailbox | `fastmail://personal/area/finance/receipts/` |
| thread | `fastmail://personal/area/finance/receipts/?thread=T5501` |
| email | `...receipts/?thread=T5501&email=msg-9f2a` |
| raw bytes | `...receipts/?thread=T5501&email=msg-9f2a&raw=1` |

Callers of `classifyURI` (from a search of `plugins/fastmail`, non-test files):
`plugin.go`, `listing.go` (ListEnriched), `leaf.go`, `traversal.go`,
`mutate.go`, `bulk_mutate.go`, `facet.go` (two sites), `node_id.go`
(`classifyString`). Callers of the minters: `traversal.go`, `listing.go`,
`mutate.go` (move result), `bulk_mutate.go` (move result), `node_id.go`
(`ResolveNodeID`).

## 2. Every place a query component carries identity

1. `thread` in thread, email and raw URIs (the JMAP thread id).
2. `email` in email and raw URIs (the JMAP email id).
3. `raw` as a bare flag selecting the raw-bytes leaf; the only flag-style use.
4. Prose and hints that name the form: the `mutate.go` bad-request hint
   (`fastmail://<account>/<mailbox>/?thread=<id>`), `listing.go` (comment near
   line 174), `node_id.go` (comment), `docs/features/0024-fastmail-plugin.md`
   (argument forms, move, object-ids sections), `doc/cutting-garden-plugins.7.scd`
   (line ~483), `docs/features/0026-org-plugin.md` (line ~53, a pointer to
   fastmail's `?thread=` as the reason org implements `NodeIDer`),
   `docs/plans/2026-09-21-fastmail-tags-slice1.md`, and a copy of the
   `listing.go` comment in the generated `pkgs/cutting_garden_plugins/main.go`.

Identity also rides OUTSIDE the URI: organize's box id for a thread is the bare
thread id (or `<mailbox-segments>/<threadId>` for a descendant mailbox),
computed by `RelativeNodeID` and inverted by `ResolveNodeID` (`node_id.go`).
Both go through `classifyString` and `threadURI`, so they follow whatever shape
the URI has; the id text itself would not change.

Why the query was chosen, in the code's own words (`url.go`, and FDR 0024
§Argument forms): an opaque thread id is indistinguishable from a child-mailbox
name as a trailing path segment, and keeping ids out of the path keeps
classification possible without resolving the mailbox name-path against the
server.

## 3. Who depends on the current form

Verified by search (counts are matches of `?thread=`, `thread=T`, `&email=` or
`raw=1`, not distinct tests):

- Go tests: `plugins/fastmail/url_test.go` (8), `mutate_test.go` (3),
  `node_id_test.go` (1), `internal/organize/document_test.go` (3),
  `internal/node_view/espalier_view_test.go` (1).
- bats lanes: `zz-tests_bats/organize_fastmail.bats` (24),
  `zz-tests_bats/fastmail_mutate.bats` (11). Per AGENTS.md the organize lanes
  are whole-document vectors, so a changed URI shape in a printed document
  means regenerating vectors with `just test-bats-update-vectors`.
- Docs listed in section 2.

Not found / unverified:

- **Receipts.** AGENTS.md says fastmail is traversal-only (no capture, restore
  or diff), so no capture receipt should contain a fastmail URI. I did not
  search stored receipts (none are in the repo) and cannot see the operator's
  stores: **unverified** that none exist elsewhere.
- **Persisted organize documents or envelopes** holding a thread URI (as
  opposed to a thread id): none seen in the repo's fixtures other than the
  bats vectors above; **unverified** for documents saved outside the repo.
- **Out-of-tree consumers** of the thread URI form (scripts, a capability
  profile such as the FDR 0028 example, which scopes by `field=[mailbox]` and
  type, not by URI shape): **unverified**.
- **Another wire plugin** that calls fastmail: none known.

## 4. What the backward-compatibility rule says

Issue #79 and `traversal.go` (the `NodeType.Tag` comment) say compatibility is
carried by versioned or horizontal changes: a format change adds a `-v2` type
tag beside `-v1` and the `-v1` entry stays readable. That rule is about node
TYPE tags and the readability of old shapes, not about URI spelling. A URI
spelling change keeps the types (`cutting_garden-fastmail-thread-v1` and so
on) unchanged, so the question is only whether an old URI still resolves; I
read the rule as asking for exactly that (accept old, mint new), but whether the
operator wants new type tags too is a ruling, not a fact.

## 5. What a path-based scheme would look like

Constraints that shape any option (all from the code read above):

- A thread's URI names the mailbox it is listed under (FDR 0024, cutting-garden
  #401): Fastmail runs in labels mode, a thread can sit in several mailboxes,
  and a move has a source and a target. A move's result URI is the thread under
  the target mailbox (`mutate.go`, `bulk_mutate.go`).
- organize anchors at the shared `.../finance/` boundary of a mailbox's thread
  URIs (`setPathSegments` comment), and `RelativeNodeID` strips the anchor's
  mailbox path.
- Mailbox names are arbitrary text and can contain reserved characters; each is
  one escaped path segment today. Mailbox depth is variable.
- JMAP ids are URL-safe (RFC 8620 §1.2, as `node_id.go` states), so they are
  fine as path segments.
- RFC 0018 §1-§2 templates are RFC 6570 Level 1: each variable is one
  `/`-free segment, and a type whose identifiers cannot be single `/`-free
  segments must omit the template. A variable-depth mailbox path therefore
  cannot sit inside a template as plain segments.

Three candidate shapes (examples use the same account and ids as above):

**A. Thread-centric, mailbox out of the identity path.**

    fastmail://personal/thread/T5501
    fastmail://personal/thread/T5501/email/msg-9f2a
    fastmail://personal/thread/T5501/email/msg-9f2a/raw
    fastmail://personal/mailbox/area/finance/receipts/

Templates are trivial (`fastmail://{account}/thread/{thread}` and so on). Cost:
the thread URI no longer names a source mailbox, so #401's "source as well as
target" and organize's anchor boundary need another carrier (a query, which
reintroduces what this removes), and one thread has one URI where it today has
one per mailbox. It also moves mailbox URIs under a `mailbox/` prefix.

**B. Keep the mailbox path, add a reserved marker segment.**

    fastmail://personal/area/finance/receipts/-/thread/T5501
    fastmail://personal/area/finance/receipts/-/thread/T5501/email/msg-9f2a/raw

Mailbox URIs and the anchor boundary are unchanged. Cost: the marker can collide
with a mailbox of the same name, and a template cannot express a variable-depth
mailbox path (so thread types get no URITemplate; resolution would rely on
`PatchTarget`). Whether Fastmail allows a mailbox literally named like the
marker: **unverified**.

**C. Mailbox path as ONE escaped segment.**

    fastmail://personal/mailbox/area%2Ffinance%2Freceipts/thread/T5501
    fastmail://personal/mailbox/area%2Ffinance%2Freceipts/thread/T5501/email/msg-9f2a/raw

Fits a Level 1 template (`fastmail://{account}/mailbox/{mailbox}/thread/{thread}`)
and keeps the source mailbox in the thread URI. Cost: unreadable mailbox
segment, and it changes every mailbox URI too if mailboxes move under
`mailbox/`. Whether `Match` (RFC 0018 §4) percent-decodes `%2F` inside one
variable: **unverified** (I read the template parser's tests only by name).

My view, as a theory and not a recommendation to merge: C is the only shape that
keeps the source mailbox AND yields a template; B is the smallest change. The
choice depends on whether the operator wants fastmail's types to resolve by
template (C) or is content with `PatchTarget` (B). That is for the operator.

## 6. How old URIs keep resolving

- `classifyURI` accepts BOTH forms on read: if the query has `thread`, `email`
  or `raw`, classify as today; otherwise parse the path form. The two forms are
  distinguishable without a network call in B and C (a reserved first-segment
  or marker); in A the old form is unambiguous too, because `thread`/`mailbox`
  as a first segment is the new form, but an old mailbox actually NAMED `thread`
  or `mailbox` would be misread, so A needs a rule for that (**unverified** that
  such a mailbox exists in any account).
- Mint only the new form. `RelativeNodeID`/`ResolveNodeID` need no change in the
  id text; they call `classifyString`/`threadURI`, so they follow the minters.
- A node reached by an old URI and listed again reports its new URI; the move
  result already reports a `uri` in the outcome (`mutate_nodes` `uri` member),
  so a caller already handles "address changed".
- Keeping the query parse indefinitely is cheap (a few lines); dropping it is a
  separate ruling.

## 7. Effect on #404 (`PatchTarget`) and RFC 0018

Only a thread is patchable (`mutate.go`: other kinds are a bad request). If a
thread type declares a URITemplate (shape A or C), `ResolvePatchKeys` resolves
it with no plugin call and `PatchTarget` can answer `ok == false`. With B (or
today's query form) the thread has no template and `PatchTarget` classifies the
URI itself (a thread when `classifyURI` says `kindThread`); that is a small
function and does not depend on this note's outcome. So the path-based form
helps #404 but is not on its critical path.

The mailbox type cannot declare a template under any shape (variable depth),
which is acceptable: it declares no patch keys.

RFC 0018 §5's consistency invariant (every URI the plugin emits for a type
matches its template) would have to hold across `traversal.go`, `listing.go`,
`mutate.go` and `bulk_mutate.go`; the minters are the only construction sites,
which makes it checkable. A conformance case already exists that runs the host
resolver over declared templates (`internal/traversal_conformance/cases.go`);
whether it covers a linked plugin with no wire peer: **unverified**.

## 8. Migration order

1. Decide the shape (section 5) and whether new type tags are wanted
   (section 4). Operator.
2. Add read acceptance of the new form to `classifyURI`, with tests; mint
   unchanged. Ship. (Safe alone: nothing emits the new form yet.)
3. Switch the minters to the new form in one change, together with the
   `RelativeNodeID`/`ResolveNodeID` tests, the mutate hint, and the two bats
   lanes' vectors (`just test-bats-update-vectors`), reviewing the vector diff.
4. Declare URITemplates for the thread/email/raw types (shapes A or C) and add
   the conformance case; add fastmail's `PatchKeysDescriber` (#404 slice).
5. Update FDR 0024, the plugins manpage (`doc/cutting-garden-plugins.7.scd`),
   the org FDR pointer and the generated facade comment.
6. Later, separately ruled: whether to stop parsing the query form.

Steps 2 and 3 are deliberately two merges so an old host or stored URI never
meets a minted form it cannot read.

## 9. Questions for the operator

1. Which shape: A, B or C (section 5)? Recommended answer: C if fastmail types
   should resolve by template, else B.
2. New `-v2` type tags, or keep `-v1` with only the URI spelling changed?
   Recommended: keep `-v1` (types are unchanged).
3. Keep parsing the query form indefinitely? Recommended: yes.
4. Does the operator hold stored fastmail URIs (receipts, saved organize
   documents, scripts) that step 2's compatibility must cover? Not answerable
   from the repo.

## 10. Out of scope here

The org plugin's `?id=` / `?h=` addressing (the mutate_nodes schema text and
FDR 0026 show queries carrying identity there too); not read in detail. A
parallel note would be a separate task.
