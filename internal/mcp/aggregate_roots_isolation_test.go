package mcp

// The cutting-garden#165 fault-isolation proof at the AggregateRoots
// layer, the aggregation every no-arg `mcp` and `list` invocation goes
// through. This lives in internal/mcp (not command_components, where
// AggregateRoots is defined) so it gets a CLEAN global scheme registry:
// AggregateRoots enumerates cutting_garden_plugins.RegisteredPlugins(),
// which is process-global, and command_components' own test files
// already register several WirePlugin fixtures under commands that
// only behave correctly inside the specific test (and env-var window)
// that dials them — a second, unrelated test calling the bare
// AggregateRoots from that same package/process would sweep those
// dormant fixtures up too and could hang re-dialing them. internal/mcp
// starts each test run with only its own tiny in-memory RootProvider
// fakes (exclude_test.go) already registered, so this test's wire
// plugins are the only ones with a real subprocess command in this
// process.

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/command_components"
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/traversal_serve"
)

// crashBeforeAnnounceArg, when passed as this re-exec'd test binary's
// first argument, makes the process exit nonzero immediately with NO
// stdout output — simulating a wire plugin that crashes before it ever
// writes its RFC 0013 announce line. This is the exact production
// symptom cutting-garden#165 reports: "read announce line (child
// exited before announcing?): EOF".
const crashBeforeAnnounceArg = "__cg_mcp_test_crash_before_announce"

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == crashBeforeAnnounceArg {
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// TestAggregateRoots_IsolatesFailingWirePlugin registers two wire
// plugins that CANNOT come up — one with a missing/bad command path,
// one that crashes before it ever announces — alongside the package's
// existing healthy in-memory RootProvider fakes (excludeSchemeFakeA/B,
// registered in exclude_test.go's init()), then asserts
// command_components.AggregateRoots: returns no error, still returns
// the healthy fakes' roots, omits both bad plugins' contributions, and
// warns about each by name on warnw. Before this fix, either bad
// plugin's Roots() error aborted the WHOLE aggregation — discarding the
// healthy roots too — which on `mcp` meant one misconfigured wire
// plugin failed cutting-garden's own MCP initialize handshake with its
// host and took down every scheme, not just its own.
func TestAggregateRoots_IsolatesFailingWirePlugin(t *testing.T) {
	badCommand := traversal_serve.NewWirePlugin(traversal_serve.PluginSpec{
		Name:    "cg165-badcommand",
		Command: []string{"/nonexistent/cutting-garden-test-binary-does-not-exist"},
		Schemes: []string{"cg165-badcommand"},
	})
	crash := traversal_serve.NewWirePlugin(traversal_serve.PluginSpec{
		Name:    "cg165-crash",
		Command: []string{os.Args[0], crashBeforeAnnounceArg},
		Schemes: []string{"cg165-crash"},
	})
	t.Cleanup(func() {
		_ = badCommand.Close()
		_ = crash.Close()
	})

	cutting_garden_plugins.MustRegisterScheme(badCommand)
	cutting_garden_plugins.MustRegisterScheme(crash)

	var warnBuf bytes.Buffer
	roots, err := command_components.AggregateRoots(context.Background(), &warnBuf)
	if err != nil {
		t.Fatalf(
			"AggregateRoots returned an error — a bad wire plugin must be"+
				" isolated, not fatal to the whole aggregation: %v", err,
		)
	}

	var sawHealthyA, sawHealthyB bool
	for _, r := range roots {
		switch r.Scheme {
		case excludeSchemeFakeA.scheme:
			sawHealthyA = true
		case excludeSchemeFakeB.scheme:
			sawHealthyB = true
		case "cg165-badcommand", "cg165-crash":
			t.Errorf("unhealthy plugin contributed a root: %v", r)
		}
	}
	if !sawHealthyA || !sawHealthyB {
		t.Errorf(
			"an already-healthy plugin's root is missing from the"+
				" aggregation (a bad plugin must not crowd it out): %+v",
			roots,
		)
	}

	warnings := warnBuf.String()
	if !strings.Contains(warnings, "cg165-badcommand") {
		t.Errorf("warnings %q do not mention the bad-command plugin", warnings)
	}
	if !strings.Contains(warnings, "cg165-crash") {
		t.Errorf("warnings %q do not mention the crashing plugin", warnings)
	}
}

// labelerFake is a minimal RootProvider + RootLabeler fake for
// AggregateRootLabels' cutting-garden#165 fault-isolation proof
// (cutting-garden#120's aggregator). Unlike TestAggregateRoots_
// IsolatesFailingWirePlugin above, no real subprocess is needed here:
// AggregateRootLabels' isolation logic doesn't care how a plugin's error
// arrives, only that one plugin's RootLabels failure must not crowd out
// another's successful labels.
type labelerFake struct {
	scheme string
	label  string
	fail   bool
}

func (f labelerFake) Schemes() []string { return []string{f.scheme} }
func (f labelerFake) TypeTag() string   { return "cutting_garden-test-" + f.scheme + "-v1" }

func (f labelerFake) Types() []cutting_garden_plugins.NodeType {
	return []cutting_garden_plugins.NodeType{{Tag: f.scheme + "-leaf-v1"}}
}

func (f labelerFake) ListRoots(
	context.Context, *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	return nil, nil
}

func (f labelerFake) Roots(context.Context) ([]*url.URL, error) {
	return []*url.URL{f.rootURL()}, nil
}

func (f labelerFake) RootLabels(context.Context) (map[string]string, error) {
	if f.fail {
		return nil, fmt.Errorf("labeler for %q is broken", f.scheme)
	}
	return map[string]string{f.rootURL().String(): f.label}, nil
}

func (f labelerFake) rootURL() *url.URL {
	return &url.URL{Scheme: f.scheme, Path: "/root"}
}

var _ cutting_garden_plugins.RootLabeler = labelerFake{}

// TestAggregateRootLabels_IsolatesFailingLabeler mirrors
// TestAggregateRoots_IsolatesFailingWirePlugin at the RootLabels layer
// (cutting-garden#120): a plugin whose RootLabels call errors must not
// abort the whole aggregation or crowd out another plugin's successfully
// resolved labels — its roots simply keep the framework's default label
// derivation instead, with a warning naming the plugin.
func TestAggregateRootLabels_IsolatesFailingLabeler(t *testing.T) {
	healthy := labelerFake{scheme: "cg120-healthy", label: "Healthy Label"}
	broken := labelerFake{scheme: "cg120-broken", fail: true}
	cutting_garden_plugins.MustRegisterScheme(healthy)
	cutting_garden_plugins.MustRegisterScheme(broken)

	var warnBuf bytes.Buffer
	labels := command_components.AggregateRootLabels(context.Background(), &warnBuf)

	if got := labels[healthy.rootURL().String()]; got != "Healthy Label" {
		t.Errorf("labels[healthy] = %q, want %q (a broken labeler must not crowd out a healthy one)",
			got, "Healthy Label")
	}
	if _, ok := labels[broken.rootURL().String()]; ok {
		t.Errorf("broken labeler contributed a label: %+v", labels)
	}
	if !strings.Contains(warnBuf.String(), "cg120-broken") {
		t.Errorf("warnings %q do not mention the broken labeler", warnBuf.String())
	}
}
