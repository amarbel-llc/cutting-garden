package mcp

import (
	"net/url"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
)

// captureOnlyFake claims a scheme but is NOT a RootLister, so an `mcp`
// argument naming it is rejected as a usage error (a non-traversable
// endpoint cannot be served).
type captureOnlyFake struct{}

func (captureOnlyFake) Schemes() []string                     { return []string{"mcpnoroots"} }
func (captureOnlyFake) TypeTag() string                       { return "cutting_garden-test-v1" }
func (captureOnlyFake) ValidateSource(*url.URL, string) error { return nil }
func (captureOnlyFake) CaptureRoot(
	cutting_garden_plugins.CaptureRootRequest,
) cutting_garden_plugins.CaptureRootResult {
	return cutting_garden_plugins.CaptureRootResult{}
}

func init() {
	cutting_garden_plugins.MustRegisterCapture(captureOnlyFake{})
}

// driveMCP dispatches the mcp subcommand through a fresh Utility (flag
// parsing included), returning the exit code. The happy path starts a
// long-lived stdio server, so these cases exercise only the pre-server
// argument validation that returns before the transport opens.
func driveMCP(t *testing.T, args ...string) int {
	t.Helper()
	u := command.MakeUtility("cg-test", nil)
	u.AddCmd("mcp", New())
	return u.Run(append([]string{"cg-test", "mcp"}, args...))
}

func TestRun_MissingArgIsUsageError(t *testing.T) {
	if code := driveMCP(t); code != 64 {
		t.Fatalf("exit = %d, want 64 (EX_USAGE for no endpoint)", code)
	}
}

func TestRun_UnknownSchemeIsUsageError(t *testing.T) {
	if code := driveMCP(t, "bogus://x"); code != 64 {
		t.Fatalf("exit = %d, want 64 (unknown scheme)", code)
	}
}

func TestRun_NonListableSchemeIsUsageError(t *testing.T) {
	if code := driveMCP(t, "mcpnoroots://x"); code != 64 {
		t.Fatalf("exit = %d, want 64 (scheme has no RootLister)", code)
	}
}

func TestRun_OneBadEndpointAmongGoodIsUsageError(t *testing.T) {
	// A single unresolvable endpoint fails the whole invocation, so the
	// server never starts with a partially-valid root set.
	if code := driveMCP(t, "mcpnoroots://x", "bogus://y"); code != 64 {
		t.Fatalf("exit = %d, want 64", code)
	}
}
