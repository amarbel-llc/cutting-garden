# One cutting-garden per host instead of one per session

Date: 2026-11-06. Session: cutting-garden/rapid-pine. Status: proposal for
the operator; nothing is implemented. Facts below were read from the moxy,
spinclass, clown and circus checkouts (read-only) and are cited by path;
anything not checked in code is marked *unverified*.

## The problem

Every clown session runs its own moxy, and each moxy spawns its own
`cutting-garden mcp` (and smith, and the rest). Each cutting-garden warms
its roots at startup and refreshes every cached container on a timer.
Through smith that is GitHub traffic, multiplied by the number of
sessions, on one shared 5,000/hour quota (#393). Today's mitigations (the
`[mcp] no_background_refresh` switch, backoff after a failed refresh;
circus 455a4186 turns it on for smith) cut the traffic per session; they
do not remove the multiplication.

## The two shapes

### (A) One cutting-garden backend per host, reached from every session's moxy

```
 session 1                session 2                 host
 ┌──────────────┐         ┌──────────────┐          ┌───────────────────────────────┐
 │ claude       │         │ claude       │          │ cutting-garden serve-mcp      │
 │  └ moxy      │         │  └ moxy      │          │  (systemd --user unit)        │
 │     ├ folio  │         │     ├ folio  │          │                               │
 │     ├ smith… │         │     ├ smith… │          │  shared listing/facet caches  │
 │     └ cg-    │ unix    │     └ cg-    │ unix     │  ONE refresh loop             │
 │       bridge ├────────►│       bridge ├─────────►│  plugins: caldav, org, ynab,  │
 └──────────────┘ socket  └──────────────┘ socket   │  fastmail, smith (wire), …    │
   caller = session principal (SO_PEERCRED + hello)  └───────────────────────────────┘
```

Each session's moxy keeps everything it does today: its cwd, its
permission tiers, the PreToolUse hook, folio's "inside CWD" rule, and the
in-unit placement FDR 0035 requires. Only the `cutting-garden` child
changes: instead of a cutting-garden process, moxy runs a small stdio
bridge that connects to the host's one cutting-garden over a unix socket.
That is the same pattern clown already uses for frontend-class servers
behind `clown-stdio-bridge` (clown FDR 0020 :347).

### (B) One moxy per host, fronting every child server for every session

```
 session 1 claude ─┐                      ┌ cutting-garden (one)
 session 2 claude ─┼─ HTTP ─► moxy ───────┼ smith (one)
 session 3 claude ─┘  serve-http          ├ folio (one, which cwd?)
                       (one process)      └ …
```

Krone's mcp-origin already runs this shape for a remote connector
(`circus/infra/hosts/krone/mcp-origin.nix:557`).

## What each shape needs, today versus then

| Concern | (A) central cutting-garden | (B) central moxy |
|---|---|---|
| Transport | A unix socket per host (0600, in `$XDG_RUNTIME_DIR`), served by `cutting-garden`. The bridge is a stdio↔socket pump. New, small. | `moxy serve-http` is streamable HTTP only, no unix socket, no inbound auth, and ONE server-wide `Mcp-Session-Id` that any client calling initialize receives (`moxy/internal/streamhttp/server.go:199-259, 311-344`). Needs a socket listener, auth, and real per-connection sessions. |
| Caller identity | The bridge sends the session principal (`CLOWN_SESSION_ID` / `SPINCLASS_SESSION_ID`) in a hello. The server checks `SO_PEERCRED` uid, plus the cgroup for a confined unit, the way clown FDR 0020 :839-844 checks its serve sockets. | moxy resolves ONE session id per process (`moxy/cmd/moxy/main.go:489-500`). The per-call `Caller{ID, Cwd, Policy}` exists but the HTTP path never sets it (`moxy/pkg/moxy/instance.go:343-352`). Every identity-bearing path would change. |
| Permissions | Unchanged: they stay in each session's moxy and hook, which already gate `mutate_nodes` as destructive. cutting-garden adds no permission model of its own; it attributes each write to its caller. | Permission predicates run in moxy's cwd (`moxy/internal/proxy/enforce.go:37-41`, `folio-perms:59`). The broker subject is per process (moxy RFC 0002 §1, §3). The moxyfile hierarchy loads from the process's HOME and cwd (`moxy/pkg/moxy/serving.go:87-97`). All are per-session assumptions to rework. |
| Confinement (spinclass FDR 0035) | Fits. The in-unit moxy stays in the unit. The bridge reaches out through a named socket inode bound into the unit, the mechanism FDR 0035 :740-743 already uses for frontend servers. | Conflicts as designed. FDR 0035 rejects cross-session MCP over loopback as grant theft and chose per-session unix sockets (open question 5, :1398-1404). It keeps moxy inside the unit, because a frontend moxy "would be a ProtectHome bypass through folio_write" (clown 0020 :348; spinclass 0035 :882-886). |
| Shared caches and quota | One process, one listing cache, one facet cache, one refresh loop. GitHub cost is bounded once per host, not once per session. | Same, for every child. |
| Per-session state inside cutting-garden | The file plugin's intrinsic root is the process cwd (`plugins/file/traversal.go:124-135`). Shared, that root comes from the caller (hello `cwd`, checked against its principal's worktree roots), or the file plugin stays per session (below). | Same problem for every cwd-relative child (folio, grit, …). Each child inherits one moxy cwd. |
| Restart blast radius | A cutting-garden restart cuts every session's cutting-garden tools at once; the bridge reconnects and moxy's own restart is unaffected. Other servers are untouched. | A moxy restart cuts every tool of every session. `restart{}` respawns every child and broadcasts `list_changed` (moxy-restart(7)). Crash respawn is open (moxy #409, *unverified in code*). |
| Migration | Per child: point the moxyfile `cutting-garden` entry at the bridge for some sessions, keep stdio for others. Reversible per session. | All or nothing per host, and it changes the confinement design. |

## Recommendation: (A) now, as the interim on the way to (B)

(A) removes the multiplication where the quota is spent (cutting-garden's
caches and refresh loop, and smith's traversal plugin under it). It
changes nothing about permissions or confinement, and it can be rolled
out one session at a time. (B) would need, in moxy alone: a unix-socket
listener, inbound auth, per-connection sessions, a per-call caller
(identity and cwd) threaded through tier resolution and the broker, and a
per-caller moxyfile. It also conflicts with FDR 0035's decision to keep
moxy inside the unit. The operator has since chosen (B) as the long-term
shape (below); its design belongs to moxy, with spinclass ruling on how it
fits confinement. This note sequences it but does not design inside those
repos.

## Operator ruling (2026-11-06)

(A) is acceptable for now; (B), one shared moxy per host, is the
long-term shape. Implementing (A) waits for the operator's go after
review. The path below sequences (B); its tracking epic is
linenisgreat/moxy#508.

## Path from A to B

Sizes are rough (S: a commit or two; M: a slice of several; L: a
multi-slice feature that needs its own FDR). Each phase ends on an exit
test, which is what "done" means for that phase.

| Phase | Owner | Size | Prerequisites | Exit test |
|---|---|---|---|---|
| **B1: per-caller identity in `moxy serve-http`.** Each connection gets its own MCP session and an authenticated caller (principal, and the caller's cwd), replacing the one server-wide `Mcp-Session-Id`. A unix-socket listener sits beside HTTP. | moxy | L | none | Two clients on one serve-http see distinct session ids. A client without a valid caller credential is refused. The per-call `Caller` is set on every tool call (a test reads it in a child). |
| **B2: per-caller permissions.** Tier resolution, the policy broker subject and folio's `[folio]` policy evaluate against the caller from B1, not the process. That covers its cwd, its principal and its sweatfile hierarchy. The moxyfile narrows per caller. | moxy (broker client and predicates); spinclass (`policybroker serve` subject per call) | L | B1 | Two callers with different worktrees on one moxy: a folio read inside caller 1's worktree is allowed for caller 1 and refused for caller 2. A broker `deny` for one caller does not affect the other. |
| **B3: confined units reach the shared moxy.** A per-session socket, forwarded into the unit the way FDR 0035 forwards frontend servers, identifies the unit by `SO_PEERCRED` and cgroup. This replaces the in-unit moxy. | spinclass (FDR 0035 amendment and ruling); clown (socket forwarding) | M–L | B1, B2, and a spinclass ruling that a shared moxy behind per-session sockets is not the ProtectHome bypass FDR 0035 rejected | A confined session's tool calls reach the shared moxy through its socket, attributed to its principal. A write outside its worktree is refused by B2's policy. A process outside the unit cannot use that session's socket. |
| **B4: resources as arguments that carry permissions.** A tool argument naming a resource (a node URI, a blob, a path) is checked by the broker against the caller before dispatch, so a child server never decides access itself. | moxy and spinclass (design: an RFC 0002 amendment) | M | B2 | A `mutate_nodes` call naming a node outside the caller's allowed resources is refused by moxy before cutting-garden sees it. The same call is allowed for a caller whose policy covers it. |
| **B5: move children behind the shared moxy.** cutting-garden first: retire (A)'s bridge, and the shared moxy runs the single cutting-garden. Then smith and the other stateful or quota-bearing children, then the rest. | cutting-garden (drop the bridge, keep the socket server or stdio as moxy chooses); circus (the host unit); each child's own repo for any per-session assumption | M per child | B1–B4 for the children that need per-caller scope; B1 alone for read-only, cwd-free children | Host-wide, one cutting-garden process and one refresh loop exist. Sessions on the shared moxy see their own file root (or none, per the open question) and their writes are attributed. A GitHub quota sample shows no per-session multiplication. |
| **B6: retire per-session moxy.** clown sessions connect to the shared moxy only; the per-session stdio moxy and (A)'s bridge are removed. | clown; moxy; circus | M | B5 for every child a session uses | A new session starts with no moxy process of its own, and every tool works through the shared instance. Stopping the shared moxy surfaces as a clear session-level error, not silent tool loss. |

(A) stays useful through B4: it is the interim that removes the quota
multiplication now, and B5 is where it is folded into (B).

## (A) in more detail

**Server.** `cutting-garden serve-mcp --socket $XDG_RUNTIME_DIR/cutting-garden/mcp.sock`:
the existing MCP server (`internal/mcp`) over a socket, one goroutine per
connection, sharing the `Resources` caches and one maintenance loop. A
systemd user unit runs it, with `Restart=on-failure` and the
`[mcp]`/`-config` settings as today.

**Bridge.** `cutting-garden mcp --connect <socket>` (or a generic bridge,
if clown's `clown-stdio-bridge` can be reused): it relays stdio, and first
sends a hello with the principal, the caller's cwd, and the client's MCP
`initialize`. If the socket is absent, it falls back to running the
in-process server, so a session never loses the tools because the daemon
is down.

**Per-caller scope.** Each connection carries its principal and cwd:
- The file plugin's root is the caller's cwd, accepted only inside that
  principal's worktree roots (the same roots spinclass's broker derives,
  `spinclass/internal/bravo/folioeval/roots.go`). Simpler alternative:
  exclude `file` from the shared server (`-exclude-scheme=file`, as krone
  does) and keep file traversal per session.
- Caches are keyed by URI and shared across callers, which is safe
  because they hold read results of shared backends (calendars, forges),
  not per-caller data. A write invalidates for everyone (#302's drop).
- `mutate_nodes` logs the principal on each per-op line (#392's stderr log).

**Quota bound.** One refresh loop per host. With `no_background_refresh`
for smith, GitHub calls happen only on reads. A per-scheme request budget
(an open question) would turn "bounded once" into a hard ceiling.

**Failure isolation.** A wire plugin that hangs or crashes is already
isolated per plugin (#165). A panic in the server takes every session's
cutting-garden tools down until systemd restarts it; the bridge reports
the disconnect as a tool error and retries on the next call.

## "Resources as args which carry permissions"

Nothing in moxy, spinclass, clown or circus designs this yet (searched;
see the research notes). The closest mechanisms are spinclass FDR 0032 D12
handles ("handles pass like SCM_RIGHTS", rights observe/close/grant) and
moxy RFC 0002's `kind: blob` resources checked by the broker. For (A) the
natural fit is this: a tool argument names a resource (a node URI), and
the session's moxy broker, not cutting-garden, decides whether that
principal may touch it. It is the same check folio's paths get today. The
shared server trusts the principal the bridge proved and checks nothing
itself. Defining that resource check is moxy and spinclass work.

## What each repo would change

- **cutting-garden:** a socket-serving MCP mode with per-connection
  principal and cwd; the bridge (or reuse of clown's); the file root per
  caller (or excluded); the principal in write logs; tests over two
  concurrent connections sharing one cache.
- **clown:** the plugin manifest's cutting-garden entry points at the
  bridge; the bridge needs the session principal in its environment
  (clown already injects `CLOWN_SESSION_ID`, `clown/cmd/clown/main.go:428-432`).
- **spinclass:** bind the cutting-garden socket inode into confined units
  (FDR 0035's named-socket mechanism); include its peer in the cgroup check.
- **circus:** a home-manager or NixOS user unit for the server on flic; on
  krone, nothing changes (it already runs one instance).
- **moxy:** nothing for (A).

## Open questions for the operator

1. File plugin under (A): a per-caller root, or exclude `file` from the
   shared server and keep it per session?
2. Should the bridge fall back to an in-process server when the daemon is
   down, or fail loudly so the daemon's absence is noticed?
3. Is a hard per-scheme request budget wanted (for example, GitHub calls
   per hour across the host), beyond `no_background_refresh`?
4. Is (B) still wanted as a later step? If so, moxy and spinclass own
   that design (per-connection identity, and how a shared moxy fits FDR
   0035).
5. Does smith's own MCP tool surface (the `smith_*` tools, which call
   GitHub directly per session) need the same treatment, or is the
   on-demand cost acceptable once background traversal is shared?
