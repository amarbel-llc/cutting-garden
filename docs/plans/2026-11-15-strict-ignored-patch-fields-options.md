# Options: opt-in strict handling for ignored mutate_nodes patch fields

Date: 2026-11-15
Issue: cutting-garden#404 (follow-up to #376; contract from #182)
Status: ruled 2026-11-16 (see Ruling). Step 1 (host validation, the
`PatchKeysDescriber` capability, jira) is built and described in FDR 0020; the
rest of the follow-ups below are not.

## Where things stand

- A patch tolerates keys the plugin does not know (#182): a newer caller
  stays safe against an older plugin.
- Since #376 each per-op outcome carries `ignored`: the keys tolerated but
  not applied (`PatchDetail.Ignored` in `internal/cutting_garden_plugins/
  bulk_dispatch.go`, copied to `BulkOutcome.Ignored`; wire member `ignored`
  in RFC 0017 and the mutate_nodes outcome).
- The SDK helper `IgnoredPatchFields(fields, supported)` computes that list
  from a plugin's static supported-key list, and so can run BEFORE any
  write. A plugin that fills `Ignored` this way already knows the answer
  before touching anything; a plugin that reports nothing is
  indistinguishable from "ignored nothing".
- Atomicity is a separate, existing call-level choice (RFC 0017:
  `best-effort` default, `atomic` only where a plugin advertises
  `bulk-atomic`; reject, never silently downgrade).

The ask: an opt-in strict mode in which an op naming a key the plugin would
ignore fails before anything is written, naming the keys. Default stays
tolerant. Three questions are open in the issue; they are the decisions
below.

## Decision 1: where the flag lives

- **(1a) Per call** (a top-level mutate_nodes argument, and a
  `node.bulk_mutate` request member). One switch, matches how `atomicity`
  is already a call-level member. A mixed call cannot be strict for some
  ops only.
- **(1b) Per group.** mutate_nodes already batches by anchor; strictness
  could differ per anchor/plugin. More surface; groups are not a wire
  concept for `node.patch`.
- **(1c) Per op.** Most flexible, most verbose; an agent sweep repeats the
  flag on every op, and a sweep's op template would carry it.

## Decision 2: who enforces, and when

- **(2a) Plugin before writing**, via a pre-write check (a plugin that
  uses `IgnoredPatchFields` computes the list first and fails if strict and
  non-empty). Guarantees nothing is written. Requires the strict flag to
  reach the plugin: a new member on the patch call (wire: a new optional
  member, ignorable by older plugins under the "unknown tokens ignored"
  rule, so an older plugin would silently stay tolerant, which defeats
  the flag unless the host detects support via a capability token).
- **(2b) Framework after the plugin reports**, failing the op when
  `outcome.Ignored` is non-empty. No plugin change, works for every
  plugin that already reports `ignored`. But it runs after the write, so
  the op may already have applied its recognized keys: the failure would
  say "applied X, ignored Y", not "nothing written". That makes strict
  mean "report as failure", not "do not write".
- **(2c) Framework pre-write probe**: a new optional plugin capability
  returning the supported patch keys for a node type (the data
  `describe_node_types` mostly already exposes as writable fields and facet
  writes), so the framework rejects before dispatch. Single enforcement
  point and a real no-write guarantee for linked plugins; wire plugins need
  the declaration over the wire. Drift risk: the declared list must match
  what the plugin actually applies.

## Decision 3: plugins that do not report `ignored`

- **Refuse** under strict: safe (never lets a silently tolerated key
  through), but makes strict unusable against plugins that have not adopted
  the reporting yet, with a clear message.
- **Pass** under strict: usable everywhere, but strict then gives no
  guarantee for those plugins, and the caller cannot tell.
- **Pass with a warning in the outcome** (e.g. strict could not be
  verified): middle ground, adds a new outcome field.

## Recommendation

(1a) per call, (2c)-style probe for linked plugins falling back to (2b)
for plugins that only report, and **refuse** under strict when neither is
available, until every in-tree plugin reports.

Reasoning, stated with its uncertainty: the no-write guarantee is the whole
reason to want strict mode (a typo'd field name should fail, not half
apply), so a design that only fails after the write (2b alone) is weak;
but a new wire declaration (2c over the wire) is a larger RFC 0013/0017
change than the issue implies. The smaller first step is per-call strict,
enforced pre-write for the in-tree plugins that already use
`IgnoredPatchFields` (caldav, org, ynab, jira, per the #376 work; I have not
re-verified which ones do) and refusing elsewhere, leaving the wire member
and capability token as a second, separately ruled step.

If the operator would rather ship the cheapest honest thing first: (1a) +
(2b) + refuse, documented as "strict reports and fails, it does not
prevent the recognized keys from applying", is a small host-only change.

## Questions for the operator

1. Is a strict mode that fails after the write (2b) acceptable, or is the
   no-write guarantee required?
2. Per call (recommended), per group, or per op?
3. Should the wire member be part of this change, or a follow-up RFC 0017
   revision?

Answered by the ruling below: no-write is required; per call; the wire
member ships with the change, but as a per-type declaration in
`node_types`, not as a strict flag on `node.patch`.

## Ruling (2026-11-16)

The operator asked for a synthetic upfront design against every known use
case (compat for a newer caller, visible reporting, a pre-write refusal for
a typo'd key, sweep ergonomics, linked/wire indistinguishability, atomicity
untouched, uneven plugin adoption) and ruled the decisions as that design,
rather than as the incremental options above.

**Decision 1: (1a) per call.** One top-level mutate_nodes argument and one
`node.bulk_mutate` request member. Reason: smallest surface, mirrors the
existing call-level `atomicity`; a caller needing mixed behaviour splits the
call.

**Decision 2: host-side validation against a declared per-type writable key
set** ((2c) made primary, with host-side stripping as the tolerant path).
Each plugin declares, per node type, the static set of patch keys it
accepts; for a wire plugin that is a new additive member of the
`initialize` result's `node_types` entry (the pattern `creatable` already
set). The host validates every patch body against it before dispatch:
tolerant strips the undeclared keys, dispatches the rest and reports them as
`ignored`; strict refuses the op pre-write, naming them. Plugins never see
an unknown key and never see the strict flag. Reason: the no-write
guarantee is the point of strict, and this gives it to linked and wire
plugins alike from one enforcement point, with no capability token and no
flag on `node.patch`; drift inverts from "declared list vs applied keys" to
an ordinary plugin bug, since the plugin cannot apply an undeclared key.
The first step is the full host-side validation, not the declaration
alone: a declaration with no consumer would sit unexercised, repeating the
tolerance-then-reporting-then-enforcement chain that produced this issue.

**Decision 3: refuse under strict** for a plugin or node type with no
declaration; tolerant pass-through otherwise. Reason: it falls out of
decision 2, and "undeclared" is a visible capability state rather than a
silence indistinguishable from compliance.

### De-risking inventory (2026-11-16, read-only, unverified by test)

Five in-tree plugins patch: caldav, jira, fastmail, org, ynab. Every
writable key set is static per (plugin, node type), held as a Go literal or
derived from a Go map, and every one already runs `IgnoredPatchFields`
against it. The one key-level dependence is caldav's bare-form patch,
whose accepted set follows the stored object's component; since VTODO,
VEVENT and VJOURNAL are distinct node types, that is type-dependence the
host can resolve, not state-dependence. Everything else is value-level
(ynab refuses `category` on splits and `cleared` on reconciled
transactions, fastmail refuses non-thread nodes, org checks move targets)
and stays plugin-side. Today's declarations are prose: the wire
`node_types` entry carries `tag_set`, `inline_fields`, `trailer_field`,
`creatable` and a free-text per-body `accepts`; no machine-readable
writable-keys member exists anywhere, so the declaration is new. A likely
live drift the design would catch: org's listing marks `tags` writable
while its patch key is `own_tags`, so a direct `{"tags": …}` patch is
silently ignored today (inferred from the key lists, not tested).

### Follow-ups implied

- Add the per-type writable-keys declaration to the SDK describer surface
  and, for wire plugins, as an additive `node_types` member in RFC 0013
  (RFC 0017 gains the per-call strict member and the host-computed
  `ignored` semantics).
- Move `ignored` computation into the host dispatch path
  (`bulk_dispatch.go`); the five plugins' own `IgnoredPatchFields` calls
  become redundant and are removed, their literal lists becoming the
  declarations.
- Implementation question, not ruled: how the host knows the target node's
  type before dispatch for the caldav bare form (resolve the type first, or
  require the wrapped form under strict). Decide at implementation.
- Verify and fix the org `tags` / `own_tags` listing drift.
- `cutting-garden-mcp(1)` omits ynab from its mutate_nodes plugin list.
