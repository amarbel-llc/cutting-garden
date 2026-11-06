# mutate_nodes write path: proposals for #376, #321, #322

Date: 2026-11-05. Session: cutting-garden/rapid-pine. Status: proposal,
nothing implemented. Each section ends with the rulings it needs.

## #376: name the fields a patch did not apply

Today a patch reports `fields` (the keys that applied, #320) and a body of
only unapplied keys is `patched_nothing`. A caller learns a key was dropped
only by diffing what it sent against `fields` (how `due_tzid` and
`parent_uid` went unnoticed until #375).

### Proposal

Two layers. The first needs no plugin and no wire change.

1. **Host-derived `not_applied` (mcp only).** When an op's body is a JSON
   object and the plugin reported `fields` (non-nil), mutate_nodes adds
   `not_applied`: the sent top-level keys missing from `fields`, sorted.
   It is omitted when empty, and when the plugin reported no `fields`
   (nothing to diff). The cost is a few lines in `mutateGroup.outcomes`.
   It is honest only about what it measures: "sent, and the plugin did not
   say it applied it". It cannot tell an unknown key from a key the plugin
   accepted but skipped because the value was unchanged, if a plugin leaves
   such keys out of `applied`.

2. **Pre-dispatch check against declared fields.** describe_node_types
   already lists the fields each writable type accepts (BodyDescriber). A
   patch naming a key the target type does not declare is refused before
   anything is sent, naming the key and the accepted ones, the same way
   `checkCreateType` refuses an undeclared create type. This answers the
   issue's "strict" question without a strict flag: an unknown key is an
   error, always. The one thing it cannot catch is a declared key the
   plugin then skips; that is what layer 1 reports.

3. **Plugin-reported `ignored` (wire, later and only if 1+2 fall short).**
   This is the issue's original proposal: `PatchDetail.Ignored`,
   `BulkOutcome.Ignored`, an optional `ignored` member on RFC 0013
   `node.patch` results and RFC 0017 `outcomes[]`, nil meaning "not
   reported". It is worth it only if a plugin has keys that are declared
   but conditionally skipped, with a reason to give.

`patched_nothing` keeps its meaning (no key applied). A body made only of
unknown keys never reaches it, because layer 2 refuses it first.

### Rulings needed

- Is refusing undeclared patch keys (layer 2) acceptable as a behaviour
  change? A caller sending an extra key today gets a silent success.
- Name: `not_applied` (host-derived, says what it measures) or `ignored`
  (the issue's name, which suggests the plugin decided)?
- Is layer 3 wanted at all, or deferred until a plugin needs it?

## #321: temp ids inside op bodies

A temp id (`+name`) can be an op's `id` or `in`, but not a value inside a
body, so "create a parent task and a child whose RELATED-TO is the parent"
takes two calls.

### Proposal

- **Syntax: a reserved object, JSON bodies only.** Any JSON value
  `{"$ref": "+name"}` in a body is replaced, before the op is dispatched,
  by the created node's reference. A bare string `"+name"` is never
  substituted: it is legitimate content.
- **What it resolves to.** `{"$ref":"+name"}` is the created node's id
  under the group's anchor (what organize prints). `{"$ref":"+name",
  "as":"uri"}` gives the full URI, and `"as":"key"` gives the source's own
  identity (caldav UID, jira issue key), which is what RELATED-TO or an
  issue link wants. `key` needs a new optional `BulkCreated.Key` that a
  plugin fills in; a ref asking for `key` from a plugin that did not
  report one fails that op ("depends on …", like a failed create).
- **Raw bodies (iCalendar) are refused** when they contain `$ref`. The
  JSON patch/create body is the supported path. A text token syntax for
  raw bodies can come later if needed.
- **Who resolves.** The host, in the SDK's best-effort loop
  (`BestEffortBulkMutate`), which already runs ops in order and knows each
  create's URI before the next op. A plugin with its own `BulkMutate`
  (ynab batches into one request) cannot be handed unresolved refs: the
  host refuses a `$ref` there unless the plugin declares it resolves refs
  itself (a new marker interface). That is an RFC 0017 amendment.
- **Validation** at build time, like `in`: a `$ref` to a temp id that no
  earlier create in the group defines is refused before dispatch.

### Rulings needed

- `$ref` object syntax (versus a string sigil such as `"+{name}"`).
- Whether `as: key` and `BulkCreated.Key` are in scope now, or only id/uri.

## #322: temp-id creates in atomic batches

`BulkRequest.Validate` refuses temp ids under `atomicity: atomic`. None of
the in-tree writers (caldav, jira, fastmail, org, ynab, nebulous) can
create a child, learn its server-assigned URI, and address it in the same
transaction: caldav and org have no multi-resource transaction, jira's bulk
create does not chain, and ynab's batch has no references between
transactions.

### Proposal

Keep the refusal. Write down the contract a future plugin would meet: an
`AtomicBulkMutator` that also implements `ResolvesTempIDsAtomically() bool`
receives the request unresolved and returns `BulkResult.Created`; the host
lifts the refusal only for such a plugin. Nothing changes until a plugin
needs it. Lowest value per token of the sixteen issues.

### Ruling needed

- Leave #322 open with this contract recorded, or close it as not planned
  until a transactional backend appears.
