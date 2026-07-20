package command_components

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/capture_wire"
	"code.linenisgreat.com/cutting-garden/internal/cgconfig"
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/traversal_serve"
	"code.linenisgreat.com/cutting-garden/internal/traversal_serve_testpeer"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// testpeerMainEnv re-execs this test binary as the RFC 0013 test peer:
// Launch spawns os.Args[0], the child inherits the environment, and
// TestMain diverts to the peer before any test runs.
const testpeerMainEnv = "CG_COMMAND_COMPONENTS_TESTPEER_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(testpeerMainEnv) == "1" {
		os.Exit(traversal_serve_testpeer.Main())
	}
	os.Exit(m.Run())
}

// TestRegisterTraversalPlugins_EndToEnd is the RFC 0013 §Host
// integration proof: a [[traversal_plugins]] stanza in a real
// config.toml, loaded through the real loader, registers a WirePlugin
// that resolves via the scheme registry and serves the spawned test
// peer's tree — with the stanza's config section crossing the wire
// wrapper-stripped.
func TestRegisterTraversalPlugins_EndToEnd(t *testing.T) {
	t.Setenv(testpeerMainEnv, "1")
	configOut := filepath.Join(t.TempDir(), "received-config.toml")
	t.Setenv(traversal_serve_testpeer.ConfigOutEnv, configOut)

	path := writeTempConfig(t, `
[cgtest]
token_env = "CGTEST_TOKEN"

[[cgtest.roots]]
uri = "cgtest://fixture/box"

[[traversal_plugins]]
name = "cgtest"
command = ["`+os.Args[0]+`"]
schemes = ["cgtest"]
`)

	var warn nullWriter
	raw, cfg, err := loadConfigWithRaw(path, warn)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.TraversalPlugins) != 1 {
		t.Fatalf("stanzas = %+v, want 1", cfg.TraversalPlugins)
	}

	if err := registerPlugins(cfg, raw); err != nil {
		t.Fatal(err)
	}

	plugin, err := cutting_garden_plugins.ResolveScheme("cgtest")
	if err != nil {
		t.Fatal(err)
	}
	wire, ok := plugin.(*traversal_serve.WirePlugin)
	if !ok {
		t.Fatalf("registered plugin is %T, want *WirePlugin", plugin)
	}
	t.Cleanup(func() { _ = wire.Close() })

	ctx := context.Background()

	roots, err := wire.Roots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 ||
		roots[0].String() != traversal_serve_testpeer.RootBox {
		t.Fatalf("roots = %v, want [%s]",
			roots, traversal_serve_testpeer.RootBox)
	}

	children, err := wire.ListRoots(ctx, mustURL(t, roots[0].String()))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(children))
	for i, child := range children {
		names[i] = child.Name
	}
	if len(children) != 3 {
		t.Fatalf("children of root box = %v, want 3", names)
	}

	// The section crossed wrapper-stripped: keys section-relative, no
	// [cgtest] header, the sub-array re-headed as [[roots]].
	received, err := os.ReadFile(configOut)
	if err != nil {
		t.Fatalf("config passthrough probe: %v", err)
	}
	want := `token_env = "CGTEST_TOKEN"

[[roots]]
uri = "cgtest://fixture/box"

`
	if string(received) != want {
		t.Errorf("config_toml = %q, want %q", received, want)
	}
}

// TestRegisterTraversalPlugins_SchemeClashIsBadRequest pins the
// misconfiguration contract: claiming a scheme a linked plugin already
// serves is EX_USAGE, not a panic.
func TestRegisterTraversalPlugins_SchemeClashIsBadRequest(t *testing.T) {
	cutting_garden_plugins.MustRegisterScheme(clashPlugin{})

	raw := []byte(`
[[traversal_plugins]]
name = "clash"
command = ["true"]
schemes = ["cc-clash-test"]
`)
	doc, err := cgconfig.DecodeConfigV0(raw)
	if err != nil {
		t.Fatal(err)
	}

	err = registerPlugins(doc.Data(), raw)
	if err == nil {
		t.Fatal("scheme clash must error")
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("want EX_USAGE (400 bad request), got %v", err)
	}
}

// TestRegisterPlugins_GeneralTableTraversalStanza pins
// cutting-garden#146 slice 2's generalization: a [[plugins]] stanza
// declaring protocols = ["traversal"] registers a traversal WirePlugin
// under its configured scheme exactly like a [[traversal_plugins]]
// compatibility-alias stanza does, with Command treated as the base
// binary invocation (the host appends "traversal-serve"). This checks
// registration bookkeeping only (WirePlugin.Schemes returns the
// configured claim without spawning); the live wire-call path — the
// schemes echo validated against the peer's real advertisement — is
// already covered end-to-end by TestRegisterTraversalPlugins_EndToEnd
// against the "cgtest" scheme that test claims first in this same
// process-global registry.
func TestRegisterPlugins_GeneralTableTraversalStanza(t *testing.T) {
	path := writeTempConfig(t, `
[[plugins]]
name = "cgtest2"
command = ["`+os.Args[0]+`"]
schemes = ["cgtest2"]
protocols = ["traversal"]
`)

	var warn nullWriter
	raw, cfg, err := loadConfigWithRaw(path, warn)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Plugins) != 1 {
		t.Fatalf("stanzas = %+v, want 1", cfg.Plugins)
	}

	if err := registerPlugins(cfg, raw); err != nil {
		t.Fatal(err)
	}

	plugin, err := cutting_garden_plugins.ResolveScheme("cgtest2")
	if err != nil {
		t.Fatal(err)
	}
	wire, ok := plugin.(*traversal_serve.WirePlugin)
	if !ok {
		t.Fatalf("registered plugin is %T, want *WirePlugin", plugin)
	}
	t.Cleanup(func() { _ = wire.Close() })

	if got := wire.Schemes(); len(got) != 1 || got[0] != "cgtest2" {
		t.Fatalf("Schemes() = %v, want [cgtest2]", got)
	}
}

// TestRegisterPlugins_GeneralTableCaptureStanza pins cutting-garden#146
// slice 2 phase 2: a [[plugins]] stanza declaring protocols =
// ["capture"] registers a capture_wire.Plugin under its configured
// scheme AND under its name in the protocol-diff registry — diff
// dispatches RFC 0002 protocol receipts by receipt KIND, a registry
// independent of the scheme registry RegisterScheme populates, so
// capture-side registration needs both.
func TestRegisterPlugins_GeneralTableCaptureStanza(t *testing.T) {
	raw := []byte(`
[[plugins]]
name = "chresttest"
command = ["chrest"]
schemes = ["chresttest"]
protocols = ["capture"]
`)
	doc, err := cgconfig.DecodeConfigV0(raw)
	if err != nil {
		t.Fatal(err)
	}

	if err := registerPlugins(doc.Data(), raw); err != nil {
		t.Fatal(err)
	}

	plugin, err := cutting_garden_plugins.ResolveScheme("chresttest")
	if err != nil {
		t.Fatal(err)
	}
	cw, ok := plugin.(*capture_wire.Plugin)
	if !ok {
		t.Fatalf("registered plugin is %T, want *capture_wire.Plugin", plugin)
	}
	if got := cw.Schemes(); len(got) != 1 || got[0] != "chresttest" {
		t.Errorf("Schemes() = %v, want [chresttest]", got)
	}
	if got := cw.ProtocolKind(); got != "chresttest" {
		t.Errorf("ProtocolKind() = %q, want %q", got, "chresttest")
	}

	pp, err := cutting_garden_plugins.ResolveProtocolDiff("chresttest")
	if err != nil {
		t.Fatal(err)
	}
	if gotCW, ok := pp.(*capture_wire.Plugin); !ok || gotCW != cw {
		t.Error("ResolveProtocolDiff did not return the same registered plugin")
	}
}

// TestRegisterPlugins_WebMigrationConfig is the cutting-garden#146
// phase-3 retirement proof at the launcher level (deliberately NOT
// live chrest): a [[plugins]] stanza shaped exactly like the one a
// real user migrates plugins/web's retired hardcoded "web" scheme to
// — name "web", schemes ["web"], protocols ["capture"] — registers a
// *capture_wire.Plugin that reproduces every dispatch surface the
// deleted plugins/web package used to provide via its init():
// resolvable by the "web" scheme (capture/diff arg classification,
// RFC 0005 §Resolution) AND by the "web" receipt kind (protocol diff
// dispatch) — both routes landing on the SAME plugin instance, and
// ProtocolKind() matching the frozen legacy receipt-type segment
// (capture_plugin's frozenHyphenKinds) so old "web"-kind receipts'
// diff dispatch keeps working unmodified.
func TestRegisterPlugins_WebMigrationConfig(t *testing.T) {
	raw := []byte(`
[[plugins]]
name = "web"
command = ["chrest"]
schemes = ["web"]
protocols = ["capture"]
`)
	doc, err := cgconfig.DecodeConfigV0(raw)
	if err != nil {
		t.Fatal(err)
	}

	if err := registerPlugins(doc.Data(), raw); err != nil {
		t.Fatal(err)
	}

	byScheme, err := cutting_garden_plugins.ResolveScheme("web")
	if err != nil {
		t.Fatal(err)
	}
	cw, ok := byScheme.(*capture_wire.Plugin)
	if !ok {
		t.Fatalf("registered plugin is %T, want *capture_wire.Plugin", byScheme)
	}
	if got := cw.ProtocolKind(); got != "web" {
		t.Errorf("ProtocolKind() = %q, want %q (the frozen legacy receipt kind)", got, "web")
	}

	byKind, err := cutting_garden_plugins.ResolveProtocolDiff("web")
	if err != nil {
		t.Fatal(err)
	}
	if kindCW, ok := byKind.(*capture_wire.Plugin); !ok || kindCW != cw {
		t.Error("scheme and receipt-kind dispatch resolved to different plugin instances")
	}
}

// TestRegisterPlugins_CombinedProtocols_NotYetSupported pins the
// current limitation: a stanza declaring BOTH protocols on one entry
// is a clear configuration error rather than silently registering
// only one capability surface.
func TestRegisterPlugins_CombinedProtocols_NotYetSupported(t *testing.T) {
	raw := []byte(`
[[plugins]]
name = "bothtest"
command = ["true"]
schemes = ["bothtest"]
protocols = ["capture", "traversal"]
`)
	doc, err := cgconfig.DecodeConfigV0(raw)
	if err != nil {
		t.Fatal(err)
	}

	err = registerPlugins(doc.Data(), raw)
	if err == nil {
		t.Fatal("combined-protocol stanza must error")
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("want EX_USAGE (400 bad request), got %v", err)
	}
}

// nullWriter swallows warnings (the fixture's [cgtest] section would
// otherwise be filtered anyway — the stanza claims it).
type nullWriter struct{}

func (nullWriter) Write(p []byte) (int, error) { return len(p), nil }

type clashPlugin struct{}

func (clashPlugin) Schemes() []string { return []string{"cc-clash-test"} }
func (clashPlugin) TypeTag() string   { return "cutting_garden-capture_receipt-clash-v1" }

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
