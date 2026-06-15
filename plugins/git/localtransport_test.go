package cutting_garden_plugin_git

import (
	"context"
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/pkgs/capture_plugin"
	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
)

// TestLocalPath_NoGitBinary is the regression guard for the plugin's
// git-free property: with PATH emptied (so neither `git` nor
// `git-upload-pack` can be exec'd), a local repo must still capture, diff,
// and restore. go-git's stock file transport would shell out to
// git-upload-pack here; the in-process file transport installed in
// localtransport.go serves the repo directly. Fixtures are built before
// PATH is cleared (buildRepo/appendCommit use go-git, no binary).
func TestLocalPath_NoGitBinary(t *testing.T) {
	dir, branch, _ := buildRepo(t, map[string]string{"f.txt": "v1"})

	t.Setenv("PATH", "")

	store := newMemStore(t)

	// capture
	res, err := captureProtocol(
		context.Background(), capture_plugin.NewBlobStoreWriter(store), dir, branch, cutting_garden_plugins.NopReporter{},
	)
	if err != nil {
		t.Fatalf("capture with empty PATH: %v", err)
	}
	if res.ObjectCount != 3 {
		t.Fatalf("ObjectCount = %d, want 3", res.ObjectCount)
	}

	// diff after a drift — exercises the in-process fetch negotiation
	appendCommit(t, dir, map[string]string{"f.txt": "v2"})
	arg := "git:" + dir + "#" + branch
	diff, err := (Plugin{}).DiffProtocol(cutting_garden_plugins.ProtocolDiffRequest{
		Context:       context.Background(),
		BlobStore:     store,
		ReceiptDigest: res.ReceiptDigest,
		Source:        mustParseURL(t, arg),
		RawSource:     arg,
	})
	if err != nil {
		t.Fatalf("diff with empty PATH: %v", err)
	}
	if len(diff.Differences) == 0 || !strings.HasPrefix(diff.Differences[0], "M ") {
		t.Fatalf("expected drift, got %v", diff.Differences)
	}
}
