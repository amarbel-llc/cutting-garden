# Options: opt-in strict handling for ignored mutate_nodes patch fields

Date: 2026-11-15
Issue: cutting-garden#404 (follow-up to #376; contract from #182)
Status: options note for an operator ruling. Nothing here is implemented.

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
