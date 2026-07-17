# RFC 0013 Traversal Plugin Transport Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use eng:subagent-driven-development to
> implement this plan task-by-task.

**Goal:** Implement RFC 0013 host-side: an out-of-process plugin serves
RootLister/RootProvider/LeafReader/facet/mutation capabilities over
newline-delimited JSON-RPC on an AF_UNIX stream socket, and cutting-garden
adapts the session into the existing capability interfaces so `list`/`mcp`/the
facet cache render a wire plugin identically to a linked one.

**Architecture:** Mirror the RFC 0008 `capture_serve` package layout in a new
`internal/traversal_serve` (+ dagnabit facade `pkgs/traversal_serve` for
out-of-tree Go peers). Extract the launch handshake shared with capture_serve
into `internal/plugin_handshake` (parameterized by cookie env / network /
subprotocol), with capture_serve delegating so its exported surface is
unchanged. The host-side adapter implements ALL capability interfaces with
decline defaults, gating each wire call on the plugin's advertised
capabilities. Config lands as a `[[traversal_plugins]]` stanza in ConfigV0
(tommy codegen) plus raw-TOML section extraction for `config_toml`.

**Tech Stack:** Go 1.26; `encoding/json` + `bufio` NDJSON framing (no new
deps); dewey errors; tommy codegen; bats-emo conformance lane; nix flake test
peer derivation mirroring `cutting-garden-test-capture-serve`.

**Rollback:** Purely additive until Task 10 (config/registration). Reverting =
dropping the new packages + the ConfigV0 field. No existing wire format or
receipt bytes change.

**Conformance bar (RFC 0013 §Conformance):** `list` and `mcp` output over the
wire peer MUST equal the same tree served by a linked in-process plugin.

---

### Task 1: Extract `internal/plugin_handshake` (shared launch pattern)

capture_serve/handshake.go is protocol-pinned by constants
(`CAPTURE_PLUGIN_COOKIE`, `unixpacket`, `capture-plugin`) but the logic is
protocol-independent. Extract; delegate; zero behavior change.

**Files:**
- Create: `internal/plugin_handshake/handshake.go`
- Modify: `internal/capture_serve/handshake.go` (delegate, keep exported API +
  docs verbatim)
- Test: `internal/plugin_handshake/handshake_test.go` (move the
  parameterizable parts of `internal/capture_serve/handshake_test.go`; the
  capture_serve test file keeps its protocol-constant assertions)

**Step 1:** Write `plugin_handshake.Protocol` + failing test.

```go
package plugin_handshake

// Protocol pins one plugin transport's launch identity: which cookie env
// authenticates the child, which socket family the rendezvous uses, and
// which subprotocol token terminates the announce line. capture_serve
// (RFC 0008) and traversal_serve (RFC 0013) each declare one.
type Protocol struct {
    CookieEnv    string // e.g. "CAPTURE_PLUGIN_COOKIE"
    Network      string // "unixpacket" (RFC 0008) or "unix" (RFC 0013)
    Subprotocol  string // e.g. "capture-plugin"
    SocketPrefix string // MkdirTemp prefix, e.g. "cg-serve-"
}

func (p Protocol) NewCookie() (string, error)
func (p Protocol) CookieFromEnv() (string, error)
func (p Protocol) AnnounceLine(cookie string, h Handshake) (string, error)
func (p Protocol) ParseAnnounceLine(line, wantCookie string) (Handshake, error)
func (p Protocol) ReadAnnounce(stdout io.Reader, wantCookie string) (Handshake, error)
func (p Protocol) ListenRendezvous() (*net.UnixListener, string, func(), error)
func (p Protocol) DialAnnounced(h Handshake) (*net.UnixConn, error)
```

Handshake struct moves here unchanged. Body of each method = the current
capture_serve implementation with the constants swapped for fields
(`sunPathMax`, `announceFields` stay package consts). The io.EOF-refuses-wrap
comment and behavior in ReadAnnounce carries over verbatim.

Test: table-driven over a fake Protocol{CookieEnv:"X_COOKIE", Network:"unix",
Subprotocol:"test-proto"} — announce round-trip, pollution rejection, cookie
mismatch, delimiter rejection, sun_path fallback. Run:
`go test ./internal/plugin_handshake/` → FAIL (package missing).

**Step 2:** Implement; test passes.

**Step 3:** Rewrite `capture_serve/handshake.go` as a thin delegation:

```go
var proto = plugin_handshake.Protocol{
    CookieEnv:    CookieEnv,
    Network:      HandshakeNetwork,
    Subprotocol:  HandshakeSubprotocol,
    SocketPrefix: "cg-serve-",
}
type Handshake = plugin_handshake.Handshake
func NewCookie() (string, error) { return proto.NewCookie() }
// … etc; exported names, signatures, and doc comments unchanged.
```

**Step 4:** `go build ./... && go vet ./... && go test ./internal/capture_serve/... ./internal/plugin_handshake/`
(vet+test both — package move rule). Expected: PASS, capture_serve tests
untouched and green.

**Step 5:** Commit: `refactor(handshake): extract plugin_handshake — parameterized announce/dial shared by RFC 0008 + RFC 0013`

### Task 2: `internal/traversal_serve/wire.go` — schema + wire types

**Files:**
- Create: `internal/traversal_serve/wire.go`, `wire_test.go`

**Step 1:** Failing round-trip test (`TestWireEncodings_RoundTrip`): a
`NodeView` with facets ⇄ `cutting_garden_plugins.Node` (URI parse, order
omitted when 0), `NodeTypeView` ⇄ `NodeType`, `FacetDimensionView` with
closed-domain values, `FacetFilter` from `[]PredicateView`, summary maps.

**Step 2:** Implement:

```go
const (
    SchemaV1 = "traversal-plugin/v1"

    MethodInitialize   = "initialize"
    MethodShutdown     = "shutdown"
    MethodNodesList    = "nodes.list"
    MethodRootsList    = "roots.list"
    MethodLeafRead     = "leaf.read"
    MethodFacetCounts  = "facets.counts"
    MethodFacetVersion = "facets.version"
    MethodLabelsResolve = "labels.resolve"
    MethodNodeCreate   = "node.create"
    MethodNodeUpdate   = "node.update"
    MethodNodeDelete   = "node.delete"

    CodeUnsupportedVersion = -32000
    CodeInvalidConfig      = -32002
    CodeMethodNotFound     = -32601
    CodeInvalidParams      = -32602

    CapRoots        = "roots"
    CapLeafRead     = "leaf-read"
    CapFacetCounts  = "facet-counts"
    CapFacetVersion = "facet-version"
    CapFacetLabels  = "facet-labels"
    CapMutate       = "mutate"
)
```

Param/result structs per RFC 0013 §initialize/§Wire encodings (
`InitializeParams{ProtocolVersions []string; ConfigTOML string}`,
`InitializeResult{Schema, Plugin PluginInfo, Schemes []string, TypeTag string,
Capabilities []string, NodeTypes []NodeTypeView, Facets []NodeTypeFacetsView,
Bodies []NodeTypeBodyView}`, `NodesListParams/Result`, `RootsListResult`,
`LeafReadParams/Result{OK, Structured json.RawMessage, RawBase64, RawMimeType}`,
`FacetCountsParams/Result`, `FacetVersionParams/Result`,
`LabelsResolveParams/Result`, `NodeCreateParams` etc.) + conversion helpers
to/from the `cutting_garden_plugins` types (`NodeViewFrom(Node)`,
`(NodeView).ToNode() (Node, error)`, same for types/facets/dimensions/filters).
JSON-RPC envelope: reuse the `jsonrpc.Message` types the same way
capture_serve/wire.go does (check its import and mirror).

**Step 3:** `go test ./internal/traversal_serve/` PASS. **Step 4:** Commit:
`feat(traversal_serve): RFC 0013 wire schema + view conversions`

### Task 3: `peer.go` — NDJSON stream peer

Simpler than capture_serve's datagram peer: `bufio.Scanner` lines in, single
writer mutex out, id-correlated calls. Host is sole initiator, but implement
symmetric request handling anyway ONLY if free — v1 needs: `Call(ctx, method,
params, &result)` (client) and a serve loop dispatching to a handler (plugin).

**Files:**
- Create: `internal/traversal_serve/peer.go`, `peer_test.go`

**Steps:** failing test over `net.Pipe()`-style in-process pair (use a real
socketpair via `net.Dial("unix")` against a listener in the test, or
`net.Pipe` — NDJSON needs only an io.ReadWriteCloser; make the peer take
`io.ReadWriteCloser`, not *net.UnixConn, so tests use net.Pipe): call/response
correlation, pipelined requests answered out of order correlate correctly,
notification send, oversized-line guard (bufio.Scanner buffer: set
`scanner.Buffer(make([]byte, 0, 64<<10), 16<<20)` — 16 MiB max line, covering
inline base64 bodies), remote-close → all pending calls fail with a
fresh (non-wrapped-EOF) error, Done()/Err(). Then implement; vet+test; commit
`feat(traversal_serve): NDJSON JSON-RPC peer`.

### Task 4: `server.go` — plugin-side Serve

The Go library a wire plugin's `traversal-serve` subcommand calls (also the
test peer's core; exported later via pkgs facade for out-of-tree Go peers).

**Files:**
- Create: `internal/traversal_serve/server.go`, `server_test.go`

```go
type ServeConfig struct {
    Plugin  cutting_garden_plugins.Plugin // required; capabilities probed by type assertion
    Info    PluginInfo                    // name/version for initialize
    // ConfigApply, when non-nil, receives InitializeParams.ConfigTOML before
    // the initialize response; an error fails initialize with CodeInvalidConfig.
    ConfigApply func(configTOML string) error
}
func Serve(ctx context.Context, conn io.ReadWriteCloser, cfg ServeConfig) error
```

Serve derives the initialize result from cfg.Plugin itself: capabilities via
type assertions (RootProvider→CapRoots, LeafReader→CapLeafRead, …), node_types
from Types(), facets from FacetDescriber, bodies from BodyDescriber. Dispatch
each method to the corresponding interface; unadvertised method →
CodeMethodNotFound; scheme-mismatched URI → CodeInvalidParams; version
mismatch → CodeUnsupportedVersion. `ok=false` outcomes marshal as results.
Test with a fake plugin (in-memory tree implementing every capability) driving
Serve over net.Pipe with raw NDJSON lines: initialize negotiation + mismatch,
nodes.list round trip, ok=false paths, unadvertised-method rejection. Commit
`feat(traversal_serve): plugin-side Serve`.

### Task 5: `launch.go` — host session bring-up

Mirror `capture_serve/launch.go` (announceTimeout 10s, shutdownGrace 5s)
using `plugin_handshake.Protocol{CookieEnv: "TRAVERSAL_PLUGIN_COOKIE",
Network: "unix", Subprotocol: "traversal-plugin", SocketPrefix: "cg-trav-"}`.

```go
type Session struct { /* cmd, peer, Init InitializeResult */ }
func Launch(ctx context.Context, argv []string, configTOML string) (*Session, error)
func (s *Session) Close() error // shutdown notification, stdin close, grace, SIGKILL
```

Launch = cookie → spawn(argv, env+cookie) → ReadAnnounce under deadline →
Dial → initialize (validating schema echo) → Session. Any bring-up failure
kills the child and returns an error (NO fallback signal — RFC 0013 has no
v1 fallback). Tests via a re-exec TestMain fake peer (mirror
`capture_serve` launch_test modes: serve / exit2 / pollute / hang-no-announce
deadline) + stdin-EOF-unblocks-accept covered in Task 7's testpeer. Commit
`feat(traversal_serve): host Launch/Session`.

### Task 6: `adapter.go` — the capability adapter (host dispatch core)

**Files:**
- Create: `internal/traversal_serve/adapter.go`, `adapter_test.go`

```go
// WirePlugin adapts a launched session into the full capability surface.
// It implements EVERY optional interface; unadvertised capabilities return
// the contract's decline value (ok=false / empty roots / nil declarations)
// WITHOUT a wire call, so type-assertion probing over-matches harmlessly —
// the decline paths are exactly the "plugin omits the interface" fallbacks.
// Mutations on an unadvertised plugin return an error (no silent decline).
type WirePlugin struct { /* config PluginConfig; lazy session; mu */ }

var _ cutting_garden_plugins.RootProvider = (*WirePlugin)(nil)
var _ cutting_garden_plugins.LeafReader   = (*WirePlugin)(nil)
var _ cutting_garden_plugins.FacetDescriber = (*WirePlugin)(nil)
var _ cutting_garden_plugins.FacetCounter = (*WirePlugin)(nil)
var _ cutting_garden_plugins.FacetVersioner = (*WirePlugin)(nil)
var _ cutting_garden_plugins.FacetLabeler = (*WirePlugin)(nil)
var _ cutting_garden_plugins.NodeMutator  = (*WirePlugin)(nil)
var _ cutting_garden_plugins.BodyDescriber = (*WirePlugin)(nil)
```

Lazy session: first call Launches (once; sync.Once-per-generation), a dead
session respawns at most once per operation, mutations never retried
(RFC 0013 §Session lifecycle). Schemes()/TypeTag()/Types()/DescribeFacets()/
DescribeBodies() answer from the cached InitializeResult (spawning on first
need). Schemes-echo validation: initialize result MUST cover config schemes
or the session is rejected. Tests drive WirePlugin against an in-process
Serve over net.Pipe (no subprocess): every capability round-trip, decline
gating (unadvertised facet-counts → ok=false with NO wire traffic — assert
via a counting conn wrapper), respawn-once, mutation-no-retry. Commit
`feat(traversal_serve): WirePlugin capability adapter`.

### Task 7: test peer binary + indistinguishability end-to-end

**Files:**
- Create: `internal/traversal_serve_testpeer/testpeer.go` (fixed in-memory
  tree: 2 containers / 3 leaves with facets on every dimension kind, every
  capability implemented; `Main()` = cookie→listen→announce→accept honoring
  stdin-EOF→Serve — mirror `capture_serve_testpeer` including the
  accept-unblock watcher)
- Create: `cmd/cutting-garden-test-traversal-serve/main.go`
- Create: `internal/traversal_serve/endtoend_test.go`

End-to-end (the conformance bar): render the SAME fixed tree (a) via the
testpeer plugin linked in-process and (b) via WirePlugin → spawned built peer
(re-exec TestMain mode, as capture_serve endtoend does), through
`internal/list`'s rendering path and through `internal/mcp`'s resources view;
assert byte-equal output. Also: stdin-EOF launch test (spawn, never dial,
close stdin, reap promptly). Commit
`feat(traversal_serve): test peer + indistinguishability end-to-end`.

### Task 8: flake packaging + bats conformance lane

**Files:**
- Modify: `flake.nix` (derivation `cuttingGardenTestTraversalServe` mirroring
  the capture-serve test binary at flake.nix bats-capture wiring; inject
  `CG_TEST_TRAVERSAL_SERVE`)
- Create: `zz-tests_bats/traversal_serve.bats` (+ reuse lib helpers)

Bats cases per RFC 0013 §Covered Requirements: bare invocation (no cookie) →
exit non-zero + empty stdout; announce shape + single-line stdout; stdin-EOF
unblocks accept → exit 0. Use bats-emo `require_bin CG_TEST_TRAVERSAL_SERVE`.
Wire into the same bats aggregate as capture_serve.bats (check justfile —
add as leaf under `test`). `git add` everything BEFORE `nix build` (dirty-tree
rule). Run `nix build .#bats-capture` (or the correct bats lane attr — check
`just --list`). Commit `test(traversal_serve): bats conformance lane + nix test peer`.

### Task 9: `pkgs/traversal_serve` facade

`just codemod-generate-dagnabit` after adding the export marker (mirror how
`internal/capture_serve/export.go` → `pkgs/capture_serve` is produced —
read `internal/capture_serve/export.go` first and copy its shape for the
plugin-side surface: Serve, ServeConfig, PluginInfo, CookieFromEnv-equivalent,
ListenRendezvous, AnnounceLine, SchemaV1, wire views needed by a Go peer).
Verify `internal/sdklayering` passes (pkgs must not leak internal). Commit
`feat(pkgs): export traversal_serve plugin-side surface`.

### Task 10: config stanza + registration

**Files:**
- Create: `internal/traversal_serve/config.go`:

```go
// PluginConfig is one [[traversal_plugins]] stanza (RFC 0013 §Host
// integration).
//go:generate tommy generate
type TraversalPluginsConfig struct {
    Plugins []PluginConfig `toml:"traversal_plugins"`
}
type PluginConfig struct {
    Name          string   `toml:"name"`
    Command       []string `toml:"command"`
    Schemes       []string `toml:"schemes"`
    ConfigSection string   `toml:"config_section,omitempty"` // defaults to Name
}
```

  (Validate: non-empty name/command/schemes; unique names; schemes not
  claiming an already-registered scheme is enforced at registration, not
  decode.)
- Modify: `internal/cgconfig/config.go` (embed the stanza field), run
  `just codemod-generate`; the raw-section extraction: LoadConfig keeps the
  raw bytes; add `SectionTOML(raw []byte, name string) (string, error)` in
  traversal_serve config.go using the tommy `document` API to find and
  re-render the named top-level table (empty string when absent). tommy's
  Undecoded warning must treat stanza-named sections as consumed — mark them
  seen in the loader after decode (check how Undecoded is computed; if
  marking isn't reachable from the loader, filter the warned keys by
  configured section names in `command_components.LoadConfig`).
- Modify: `internal/cgconfig/inject.go` + the composition root
  (`internal/cgapp/build.go` — find where Inject is called): after Inject,
  construct a `WirePlugin` per stanza and `MustRegisterScheme` it (duplicate
  scheme → startup error, not panic — wrap with a BadRequest).

Tests: config decode/validate table; SectionTOML extraction incl. absent
section; registration duplicate-scheme error; an integration test that a
configured stanza makes `list <scheme>://…` work against the built test peer.
Run the full local gate for a package-moving change:
`go vet ./... && go test ./... && conformist-fmt check`. Commit
`feat(traversal_serve): [[traversal_plugins]] config + scheme registration (Closes #140 host side)`
— note: do NOT close #140 until the RFC is accepted; use `Refs #140` instead.

### Task 11: docs + merge + coordination

- Update `CLAUDE.md` project-status paragraph (wire plugins exist; RFC 0013).
- FDR? No — RFC 0013 already carries the spec; skip per fdr criteria until
  user-facing docs need it.
- `spinclass merge-this-session` (async; attestation first). The merge gate
  runs the full suite.
- Comment on #140: host side landed, what a Rust peer needs, link the pkgs
  facade + bats lane; ping firm-banyan (chat) that the conformance lane is
  runnable against their binary via `require_bin` injection.

---

## Verification

1. `go test ./internal/plugin_handshake/ ./internal/traversal_serve/...` —
   unit + e2e green.
2. `go vet ./...` after every package-touching task (analyzer gate parity).
3. `nix build` after flake changes (remember: `git add` first).
4. Bats lane green under the sandbox.
5. The indistinguishability e2e (Task 7) is the RFC conformance bar.
6. Final: merge gate (`just`) green.

## Open items tracked elsewhere

- fj-cg's review of framing/initialize (issue #140) — may adjust wire.go
  field names before acceptance; everything else insulated.
- RFC flips to accepted only after fj-cg (or another non-Go peer) passes the
  bats lane (RFC 0013 §Conformance).
- RFC 0009 §Non-goals edit happens at acceptance (RFC 0013 §Compatibility).
