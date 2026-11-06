# MCP proposals: agent-prepared organize documents, human-applied (#390)

Date: 2026-11-06. Session: cutting-garden/rapid-pine. Status: exploration;
options and a recommendation, nothing implemented.

## The problem

An agent writes through `mutate_nodes`, and every write lands at once. The
operator already has a review-then-apply surface for exactly these
changes: the organize document (RFC 0015, FDR 0023). It is faceted, edited
in an editor, pins its pre-edit state as `_base`, three-way-merges against
live data on apply, and records creations in the ledger
(`internal/organize/ledger.go`, keyed by `_base` digest) so a re-apply never
duplicates. The ask is a way for a session to *prepare* such a document and
stop, so nothing changes until a human applies it.

## What already exists and is reused unchanged

- **Generation and the base pin.** `cg organize <anchor> [query] <group-by>`
  emits the document with `_base` (a madder digest of the pre-edit state).
- **Apply.** `cg organize -apply <file>`: three-way merge of the edits
  against `_base` and a re-query of live state. At a terminal it confirms;
  piped it is dry-run until `-commit`.
- **Stale-base handling.** A field changed both in the document and live
  since `_base` is a conflict and is refused. Disjoint changes merge.
- **Creation idempotency.** The ledger skips a `+temp` box already created
  under the same `_base`.

## Options

### A. Proposal file in cutting-garden's state dir (recommended)

Two MCP tools:

1. **`generate_organize`** (read-only). Takes `anchor`, optional `query` and
   `group_by`, and returns the document text with `_base` pinned — the same
   bytes `cg organize` prints. The agent now holds the editable document.
2. **`propose_organize`** (writes only local state, not the substrate).
   Takes the edited document and a one-line `note` (why). The server:
   - parses it and checks `_base` resolves in the store (refusing a
     document it cannot later apply);
   - runs the apply planner as a dry run against live data and returns its
     summary: the writes it would make, any conflicts already present;
   - saves the document to
     `$XDG_STATE_HOME/cutting-garden/proposals/<id>.organize`, with a sidecar
     `<id>.json` (`note`, `base`, `created_at`, the proposing session's
     identity when the host passes one, the dry-run summary);
   - returns the proposal id and the file path.

The operator reviews by opening the file, edits it if they like, and runs
`cg organize -apply <file>`: no new apply path. A small `cg proposals`
subcommand (list, show, reject) makes the directory navigable.

**Approval record.** Apply already knows the document's path. When it is a
proposal file, a successful `-commit` (or a terminal-confirmed apply)
appends to the sidecar: `approved_at`, the approving user (`$USER`, and
whether the apply was a TTY confirmation), and the writes and ledger
entries it made. A rejected proposal gets `rejected_at` and moves to
`proposals/done/`. The creation ledger keeps doing its own job, keyed by
`_base`.

**Notification.** Out of cutting-garden's scope: the proposing session
tells the operator (chat, or its own summary) and names the proposal
path. cutting-garden only guarantees the file and sidecar exist.

**Stale base.** Nothing new is needed: apply's three-way merge decides.
Two refinements are cheap: the sidecar carries the dry-run summary taken
at proposal time, so `cg proposals show` can rerun it and say "N writes,
M conflicts now"; and a proposal older than a configurable age is listed
as stale, though still applicable.

### B. Draft nodes in the substrate

The agent writes into the data itself (an org `proposals.org`, a caldav
"Proposals" calendar). Rejected: it is per-substrate, it is a real write
(the thing we are trying to defer), and it pollutes the operator's data.

### C. A deferred mutate_nodes changeset

`mutate_nodes` with `propose: true` stores the batch (the ops JSON) for
later approval instead of applying it. It covers writes organize cannot
express (bodies, deletes, raw iCalendar), but the operator reviews JSON
ops, not a document, and there is no three-way merge (ops address nodes,
not a base). Worth having as a complement for those writes, not as the
main path.

## Which writes stay direct

A per-scheme policy in config, defaulting to today's behaviour:

```toml
[mcp.writes]
default = "direct"   # direct | propose | off
org = "propose"
```

- `direct`: `mutate_nodes` is advertised for the scheme (today).
- `propose`: `mutate_nodes` refuses for that scheme and names
  `propose_organize`. `generate_organize` and `propose_organize` work.
- `off`: no write tool for the scheme.

The clown PreToolUse hook keeps gating `mutate_nodes` as destructive.
`propose_organize` is classed as a local-state write, not destructive.

## Recommendation

Option A, with the per-scheme write policy defaulting to `direct`, so
nothing changes until an operator opts a scheme into `propose`. Option C
can follow for the writes organize cannot express. Order:
`generate_organize` first (useful alone), then `propose_organize` with the
sidecar, then the apply-time approval record and `cg proposals`, then the
policy switch.

## Needs before code

- An FDR (this crosses organize, mcp, config and a new subcommand), with a
  ruling on the proposals directory location and the sidecar fields.
- Whether `generate_organize` belongs to swift-plum's organize lane (RFC
  0020 multi-root documents change what a document can anchor on).
