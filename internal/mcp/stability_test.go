package mcp

import (
	"context"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/go-mcp/transport"
)

// go-mcp runs each request on its own goroutine with no recover, so a panic in
// one plugin call would kill the whole process: in the shared daemon, every
// session's tools. The call must come back as an ordinary tool error.
func TestCallTool_APanickingPluginIsAToolErrorNotACrash(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.resolve = func(string) (*url.URL, cutting_garden_plugins.NodeMutator, error) {
		panic("plugin bug")
	}

	res, err := tools.CallTool(context.Background(), "mutate_nodes", []byte(
		`{"groups":[{"anchor":"faketest://h/","ops":[{"kind":"put","id":"a.ics","body":"x"}]}]}`,
	))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !res.IsError || !strings.Contains(resultText(res), "panicked") {
		t.Fatalf("want a panicked tool error, got %+v", res)
	}
}

func TestReadResource_APanickingPluginIsAnErrorNotACrash(t *testing.T) {
	root, _ := url.Parse("faketest://h/")
	provider := &Resources{
		roots: []*url.URL{root},
		resolve: func(string) (*url.URL, cutting_garden_plugins.RootLister, error) {
			panic("plugin bug")
		},
		facets:   newFacetCache(),
		listings: newListingCache(),
	}
	_, err := provider.ReadResource(context.Background(), "faketest://h/work")
	if err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("want a panicked error, got %v", err)
	}
}

// A plugin call that never returns holds its session's handler, and a session
// waits for its handlers before it ends. Stopping the daemon must not wait
// forever for such a session: after a grace period it returns anyway, and the
// process exit ends the stuck goroutine.
func TestServeListener_ShutdownIsBoundedByAHungHandler(t *testing.T) {
	defer func(old time.Duration) { shutdownGrace = old }(shutdownGrace)
	shutdownGrace = 200 * time.Millisecond

	hang := make(chan struct{})
	defer close(hang)
	serve := func(ctx context.Context, tr transport.Transport) error {
		<-hang // ignores ctx, as a stuck plugin call does
		return nil
	}

	socket := filepath.Join(t.TempDir(), "mcp.sock")
	ln, err := listenOwnerOnly(socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var log syncLog
	served := make(chan error, 1)
	go func() { served <- serveListener(ctx, ln, serve, log.logf) }()

	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // the session is now inside serve

	cancel()
	select {
	case err := <-served:
		if err != context.Canceled {
			t.Errorf("serveListener = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon waited forever for a hung session")
	}
}
