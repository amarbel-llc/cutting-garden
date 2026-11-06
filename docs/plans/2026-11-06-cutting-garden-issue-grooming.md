# cutting-garden issue grooming, 2026-11-06

Session: cutting-garden/rapid-pine. Read-only pass over the 202 open issues
on linenisgreat/cutting-garden at master 3493deb, plus a cleanup survey.
Nothing was closed, edited or relabelled; every close or relabel below is a
**proposal for the operator**.

Method and its limits: each issue was read with its comments and checked
against the code, at grep level for most, by three read-only agents split by
number range. "Unverified" marks a call that rests on the issue text alone.
Treat a FIXED or STALE call as a lead to confirm, not a verdict.

## 1. Propose to close or rescope

| Issue | Call | Evidence |
|---|---|---|
| #102 | FIXED | `internal/claude_hooks/claude_hooks.go:51` (tool-name prefix), `:84-88` (ask for destructive tools, allow for read tools). The hook is live, not inert. |
| #267 | FIXED | The org plugin takes a WebDAV LOCK (`plugins/org/dav.go`, `orgtestserver`). |
| #353 | FIXED (read side) | `(tree)` grouping landed in 0d18906 / 5e45002 / 675de66. Edits are #380. |
| #378 | FIXED (read side) | `tag_files` join landed in 0d18906. Edits are #380. |
| #79 | FIXED (design) | RFC 0010 is written (status `proposed`). Close when RFC 0010 is accepted. |
| #63 | design FIXED | RFC 0006 and FDR 0010 are written. Implementation not started; rescope to "implement". |
| #231 | mostly FIXED | dodder-hyphen interpreter and the `[tags]` override landed (327fe3e, e3b8a33, 80330f7). Unverified whether a config-linkable interpreter plugin type remains. |
| #186 | FIXED in substance | The conformance driver exists (73e26eb, 44e876b). Only the deferred "class B" tier remains; rescope. |
| #235 | STALE | year/month facets were retired by #230 (128ad2d), so the `FacetWrite.Field` mismatch is moot (unverified). |
| #130 | STALE | v0.1.21 AX feedback; many items have landed since. Split what remains into current issues. |
| #124 | STALE checklist | The framework fold and `FacetLabeler` appear shipped. Refresh the checklist. |
| #43 | STALE (likely) | Appears absorbed by RFC 0002/0003/0005 and `internal/capture_wire` (unverified). |
| #350 | FIXED (unverified) | Quoted box ids already parse (`internal/trellis/literal.go`, `organize_org.bats:114`). The title is vague; confirm with the author. |
| #355 | DUPLICATE | of #314 (a fork of Orgzly as a cutting-garden client). |
| #324 | DUPLICATE | of #330. |
| #209 | DUPLICATE (umbrella) | of #204 / #206 / #208. |

### Close list (prepared, NOT executed; waits for the operator's go)

Only issues proven fixed or duplicated, each with its evidence and the
one-line close comment to post:

| Issue | Evidence | Close comment |
|---|---|---|
| #102 | 0c2cf2a (`feat(hooks): gate CUD write tools as ask via the PreToolUse hook (#102)`); a17773d moved the gate to `mutate_nodes` | "Done in 0c2cf2a: the PreToolUse hook asks for the destructive MCP tool (now `mutate_nodes`, a17773d) and allows the read tools (`internal/claude_hooks/claude_hooks.go:51, 84-88`)." |
| #267 | f392ca0 (`feat(org): … under a WebDAV lock`); every org write path takes the lock and reads back (`plugins/org/dav.go`, `lockWithRetry`) | "Done: org writes take a WebDAV LOCK and verify by read-back (f392ca0 onward, `plugins/org/dav.go`)." |
| #355 | same request as #314 | "Duplicate of #314 (a fork of Orgzly as a cutting-garden/trellis client)." |
| #324 | same exploration as #330 | "Duplicate of #330." |
| #107 | moot: every test binary is now built through `buildCuttingGardenGo`, which sets `version` the same way for each, and none of the five test servers (`cmd/cutting-garden-*-test*`) carries build identity; the issue's own option (b) | "Closing as decided: test binaries carry no build identity, uniformly (the test-git-sshd and the five testservers alike); the shared derivation helper replaced the dangling per-derivation `version` attr." Needs the operator to accept option (b). |
| #209 | umbrella over #204 / #206 / #208 | "Tracked by #204, #206, #208 (one case each)." Or keep it open as the umbrella; the operator's choice. |

Not on the close list, despite §1: #353 (swift-plum holds that close),
#378 (its placement rule, the edit side, is still open), and #79, #63,
#186, #231, #235, #124, #130, #43, #350. Each of these needs a rescope or
a confirmation first.

## 2. Ready to work, small, no ruling needed

These have a clear acceptance test and fit one commit each:

- **#122**: one `rootLabel` instead of two: `internal/mcp/tools.go:1217` and
  `internal/list/list.go:244` are identical.
- **#96**: one exec-a-tool helper. `tailWriter` is triplicated:
  `plugins/ytdlp/exec.go:266`, `plugins/optical/exec.go:142`,
  `plugins/googlephotos/exec.go:59`.
- **#299**: a `jmap_live_env` justfile variable for the JMAP token block
  pasted at justfile:648, 694, 734, 763, 1177.
- **#342**: `internal/traversal_serve/server.go:62-67` does not re-check
  `shutdownCh` on `ctx.Done`, unlike `:81-85`. Unit test: cancel after
  shutdown, expect nil.
- **#264**: `plugins/fastmail/session.go:74-81` session cache has no lock;
  add `sync.Once` or a mutex and a `-race` test.
- **#265**: `internal/organize/apply.go:401/408` runs `resolveWrites` before
  `checkMoveWritable`; swap them.
- **#280**: `internal/organize/create.go:589/600` has no trailing-`/` trim
  before the container-type check.
- **#245**: `internal/organize/select.go` does not pass `WithTagsInterpreter`.
- **#239**: `internal/traversal_serve/server.go` never calls
  `filter.Validate`; map the failure to -32602 and add a conformance case.
- **#366**: three conformance cases (patch `uri`, delete `name` / -32004, bulk
  `outcomes`) in `internal/traversal_conformance/cases.go`.
- **#107**: the test-git-sshd `main.go` has no `buildinfo.Set`.
- **#108**: a `-config` flag on `mcp` and `list` (the NixOS module still uses
  the `XDG_CONFIG_HOME` hack); needs a one-line RFC 0007 note.
- **#94**: FILES/ENV manpage entries for `mcp` and `list`.
- **#93**: a `zz-tests_bats/config.bats` lane.
- **#95**: expose `DeleteLoose` for `capture --pack`.
- **#379**: a `doc/snob_case.7.scd` page.
- **#269**: the justfile still uses `go run` at line 1312; switch to
  `godyn-go`.
- **Organize hardening batch**: #255, #256, #257, #258, #244, #283. Small
  diagnostics and refactors from review follow-ups; bats vectors churn, which
  `just test-bats-update-vectors` regenerates.
- **Conformance batch**: #204, #206, #208, #276. Each is one driver case;
  together they form one slice.

## 3. Ready, but needs a ruling or a design record first

- **Rulings on the MCP surface** (this session's triage, relayed by the
  coordinator): #382, #376, #321, #322, #174, #181, and #392's
  continue-after-signal question.
- **#393's tick bound or eviction**: held for the operator, since it changes
  cache behaviour.
- **#393's backoff:** honour a plugin's rate-limit reset time and add jitter.
  The coordinator asked for both; 3493deb did NOT add them (it backs off
  exponentially only). This is a gap I should have caught. It needs the
  plugin error to carry a reset time across the wire (RFC 0013).
- **#389**: null for absent declared fields. Ruling: always, or only when
  named in `fields`.
- **#388**: filter OR, negation and missing. An RFC 0012 amendment. It also
  unblocks #382.
- **#387**: set-delta sweeps. Ruling on the patch syntax.
- **#59**: remove the legacy capture formats. The earliest-close date
  (2026-06-21) has passed; it needs a ruling that the window is closed.
- **#347**: `internal/command_components/config.go:103` only exempts
  `[[traversal_plugins]]` sections from the unknown-key warning. Ruling on
  exempting plugin-owned tables.
- **#285**: secret handoff to wire plugins, convention (a) or (b).
- **#344, #301, #169**: RFC 0013 amendments (a wire `FacetFoldRefuser`,
  `RootNamer`, per-root health in `initialize`).
- **#354**: optional grouping. `internal/organize/organize.go:71/157` still
  require one.
- **#351, #271, #268, #277, #284, #338, #325, #312, #339**: organize and
  plugin behaviour choices, each named in its issue.
- **#6 and #17**: near-duplicates (ignore patterns for capture and diff);
  settle one pattern syntax for both.

## 4. Parked (explorations, blocked, or cross-repo)

About 75 issues: explorations (#126, #127, #139, #157, #179, #303, #314,
#318, #329, #330, #345, …), items blocked on other work (#295–#298 on #356;
#335 and #336 on chrest; #381 on igloo; #259 and #225 on #211), cross-repo
work (#220, #348 for smith, #46 epic), and design-first items (#147, #188,
#272, #273, #278, #252, #219). No action proposed beyond keeping them open.

## 5. Reporter follow-up needed

#287, #289, #288 and #371 have repros too vague, or too old, to act on;
the reporter should restate them against the current build.

## 6. Cleanup survey (code and docs)

- `AGENTS.md:21-22`: says `hook` is "inert until the MCP server grows write
  tools, #102". `mutate_nodes` exists and the hook emits ask/allow (§1, #102).
  **Doc drift; fix together with closing #102.**
- `AGENTS.md:345`: "`CommandWithMCPAnnotations` … future MCP wiring (still
  inert)". Unverified whether it is still inert.
- `internal/command/command_line_input.go:9`: a TODO with no issue.
- `internal/command/complete.go:116`: a bare "TODO #48". It probably cites a
  dodder/madder issue, not cutting-garden#48 (open, about git); spell it
  `TODO(dodder#48)` once confirmed.
- `examples/nixstore/nixstore.go:71, 84`: `TODO(nix_store repo)` cites a repo,
  not an issue.
- Duplicated helpers: `rootLabel` (#122), `tailWriter` (#96), and the justfile
  JMAP block (#299). All three are already tracked.

Not covered: a systematic dead-code sweep, a check of every `TODO(#N)` for a
closed issue, and a wider search for near-duplicate helpers.

## Proposed order if the operator wants a cleanup slice

1. Close or rescope §1. The AGENTS.md hook line goes with #102.
2. One commit each: #122, #96, #299, #342, #264.
3. The organize hardening batch, then the conformance batch.
