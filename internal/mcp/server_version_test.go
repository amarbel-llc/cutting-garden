package mcp

import (
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/buildinfo"
)

// TestServerVersion_CarriesTheBuildCommit pins cutting-garden#343: the version
// the MCP server reports in serverInfo is the full build identity, so a
// client that sees only that string can tell which commit is running.
func TestServerVersion_CarriesTheBuildCommit(t *testing.T) {
	prevVersion, prevCommit := buildinfo.Version, buildinfo.Commit
	t.Cleanup(func() { buildinfo.Set(prevVersion, prevCommit) })

	buildinfo.Set("0.1.30", "19bdee4")
	if got, want := serverVersion(), "0.1.30+19bdee4"; got != want {
		t.Errorf("serverVersion() = %q, want %q", got, want)
	}

	// An unstamped or dirty build says so rather than reusing a stale sha.
	buildinfo.Set("0.1.30", "19bdee4-dirty")
	if got, want := serverVersion(), "0.1.30+19bdee4-dirty"; got != want {
		t.Errorf("dirty serverVersion() = %q, want %q", got, want)
	}
	buildinfo.Set("dev", "unknown")
	if got, want := serverVersion(), "dev+unknown"; got != want {
		t.Errorf("unstamped serverVersion() = %q, want %q", got, want)
	}
}
