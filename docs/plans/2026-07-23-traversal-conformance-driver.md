# Traversal conformance driver (#186) — slice-1 plan

Decided with Sasha 2026-07-23. Manifest-driven now; the fixture-tree
deep-equality tier ("class B") is deliberately future — recorded here and
on #186 so the intent survives.

## Why

Every `portable` case in `zz-tests_bats/traversal_serve.bats` tests the
LAUNCH contract only. A peer can pass the entire conformance lane while
getting every method's semantics wrong. Three peer-emitted surfaces now
exist that nothing machine-checks end-to-end: `node.patch`'s `applied`
(#182), caller-fault vs plugin-fault error codes (#185), and
`facets.counts`' `by_container` (#173). All three were sites of real
cross-implementation divergence found by hand in July 2026.

## Architecture

One Go binary: `cmd/cutting-garden-conformance-traversal`, backed by
`internal/traversal_conformance`. Exposed as a flake package (NOT shipped
in release artifacts — the caldav-testserver pattern) so an external peer
runs `nix run .#conformance-traversal -- --manifest peer.toml`. Output is
TAP (the bridged amarbel-llc/tap dep), so the bats lane wraps it with one
portable case and a standalone run reads the same.

**The driver speaks RAW NDJSON on the socket.** It MUST NOT go through
`WirePlugin`: the #173 host normalization (breakdown re-sort/cap/filter)
and any future boundary repair would correct a peer's non-conformance
before an adapter-mediated driver could see it. The seam that makes this
clean: `Launch`/`Session` (internal/traversal_serve) are BELOW the
adapter — spawn, cookie, announce, dial, with `Session.Call` accepting a
raw `json.RawMessage` result — so the driver reuses the single launch
grammar and still asserts bytes. One change needed: `Launch` currently
performs `initialize` itself (validating version/schema echo), but
initialize behavior is itself under test; factor the pre-initialize half
(spawn+announce+dial → Session) so the driver issues initialize as a
test case and keeps the raw result. The host's `Launch` becomes a thin
wrapper over that half plus its existing initialize validation —
behavior-preserving for production.

## Peer manifest (TOML)

Per-peer file supplying what the protocol cannot know generically
(patch bodies are plugin-defined — the parameterization fj-cg predicted):

- `command` — argv to launch the peer
- `config_toml` — optional config section to pass through initialize
- `schemes` — expected schemes echo
- `writable_container` — URI where create/patch/delete probes may run
- `create` — { type, body } for a probe node
- `patch_recognized` — { body, expect_applied = [keys] }
- `patch_unrecognized_only` — { body } (expect applied: [])
- `patch_wrong_typed` — { body } (expect -32602)
- optional `facet_container` — URI whose facets.counts should carry a
  breakdown, + a filter to use for the descend-target case

The cgtest testpeer's manifest lives in-tree; fj-cg/nebulous own theirs.
Probe nodes are created under `writable_container` and deleted in
cleanup, mirroring the probe-hygiene discipline from the #180 arc.

## Case list, slice 1

1. **initialize** — raw result: version token, schemes echo vs manifest,
   capability tokens well-formed (unknown tokens tolerated per RFC 0013).
2. **Error codes** — unadvertised method → -32601; malformed URI param →
   -32602; a plugin-fault probe is NOT generically constructible, so
   slice 1 asserts caller-fault codes only and leaves plugin-fault to
   the peer's own tests (documented limitation).
3. **node.patch applied tri-state** — recognized → exact
   `expect_applied` set; unrecognized-only → present `[]` (NOT omitted,
   NOT success-without-key); wrong-typed → -32602. This is fj-cg's
   known-wrong case: the driver MUST fail their pre-76d80b4 build —
   that pair is the driver's own acceptance test.
4. **by_container raw invariants** — when present in the RAW result:
   every count > 0, sorted (desc count, asc uri), ≤ 50 entries.
   Checkable only pre-normalization; this is the case that justifies
   the raw-wire architecture.
5. **Descend-target property (RFC 0012 §13 attribution ruling)** — for
   each breakdown entry: re-issue nodes.list/facets.counts against the
   entry URI with the SAME filter; attributed count must be reachable.
   Fully generic. (The union-narrowing §13 note means a conformant peer
   may simply omit the breakdown — omission passes.)

## Future (class B, deliberately deferred)

Fixture-tree tier: a peer opting in serves the exact cgtest tree and the
driver deep-equals listings/summaries/leaf content against known values
— the existing Go-testpeer indistinguishability bar, extended
cross-implementation. Higher assertion strength, real per-peer cost,
tests a peer's test-mode rather than its production tree. Do when a
second external peer exists or when a divergence slips past slice 1.

## Verification

- Go tests: driver vs in-tree testpeer (spawned, real socket) — all
  cases pass; disable-experiments per case where feasible.
- The known-wrong self-test: a deliberately broken manifest/peer variant
  (testpeer flag or fixture) proving the driver FAILS what it must fail.
- bats: one new portable case running the driver against
  CG_TEST_TRAVERSAL_SERVE + the in-tree manifest, in the hermetic lane.
- RFC 0013 §Conformance Testing gains the driver as the session-level
  ratification mechanism (launch-contract bats cases remain).

## Coordination

- fj-cg (forgejo-cli/firm-rowan): offered fixtures including the
  known-wrong pair; gets the manifest schema when it lands; their run
  closes their own e2e -32602 coverage gap.
- nebulous (light-elm): manifest for newsblur:// once the driver exists;
  their by_container emission becomes machine-checked.
