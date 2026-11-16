# ajour shadow-mode harness: design note

Status: draft proposal (docs only; nothing implemented). Author: swift-plum.
Related: FDR 0028 (ajour), ajour(7), the operator's "shadow mode early" ruling.

## Problem

An enforcer (moxy, a clown hook, spinclass) will gate agent tool calls with
`ajour.Evaluate`. Before any profile is allowed to refuse for real, the
operator wants to see what it WOULD have decided on real traffic, with no
effect on the session ("shadow mode").

## Proposal

A replay mode on the existing `cutting-garden-ajour` binary, not a new tool:

    cutting-garden-ajour shadow --profile <stencil-or-layer-files> --calls <ndjson>

- Input: NDJSON of recorded calls, one per line, in the shape the enforcer
  builds an `ajour.Call` from (tool, arguments, subject, posture). The exact
  field set is OPEN until the enforcer-side `Call` builder exists (spinclass
  hook; not mine).
- Per call: `Evaluate`, then `Resolve(posture, canEscalate)`; print one line
  `<n> <outcome> <action> <layer>:<node>`, reusing `Verdict.String()`.
- Summary: counts of Admitted / Ask / Refused, plus the top refusing
  (layer, node) pairs, so a profile author sees which node bites most.
- Exit code mirrors `diff(1)`: 0 nothing would be refused or asked, 1 some
  would, 2 trouble (parse error, bad input).
- Pure and posture-free like `Evaluate`: no network, no card, no writes.

## What must be fixed by ready-elder's catalog types before this starts

(Asks; I have not verified which are already settled.)

1. The catalog's tool/node type names and the `Call` argument shape the
   catalog assigns to each tool (so a recorded call can be turned into
   path/op steps without guessing).
2. Where blessed profiles live and how layers are ordered
   (`MOXY_PROFILE_LAYERS`-style list), so `--profile` takes the same list.
3. The validator ruleset's finding format, if shadow should also run the
   validator first and refuse to replay an invalid profile.

## Non-goals

No enforcement, no escalation channel, no timeout policy (the enforcer's).

## Open question

Where do recorded calls come from: a hook-written NDJSON log, or freud
transcripts? Unverified which exists; decides whether a converter is needed.
