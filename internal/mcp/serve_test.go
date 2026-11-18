package mcp

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"code.linenisgreat.com/purse-first/libs/go-mcp/server"
	"code.linenisgreat.com/purse-first/libs/go-mcp/transport"
)

// sharedServe returns a serveFunc over ONE provider, like prepareServer, so
// concurrent connections demonstrably share it.
func sharedServe(t *testing.T) (serveFunc, *Resources) {
	t.Helper()
	root, err := url.Parse("faketest://h/")
	if err != nil {
		t.Fatal(err)
	}
	provider := &Resources{
		roots:    []*url.URL{root},
		resolve:  fakeResolve,
		facets:   newFacetCache(),
		listings: newListingCache(),
	}
	return func(ctx context.Context, tr transport.Transport) error {
		srv, err := server.New(tr, server.Options{
			ServerName:        serverName,
			Resources:         provider,
			PreferV1Providers: true,
		})
		if err != nil {
			return err
		}
		return srv.Run(ctx)
	}, provider
}

const listRequest = `{"jsonrpc":"2.0","id":1,"method":"resources/list"}` + "\n"

// TestServeListener_ConcurrentBridgesShareOneServer runs several bridge
// sessions against one listener at once; each must get its own full response.
func TestServeListener_ConcurrentBridgesShareOneServer(t *testing.T) {
	serve, _ := sharedServe(t)
	socket := filepath.Join(t.TempDir(), "d", "mcp.sock")
	ln, err := listenOwnerOnly(socket)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- serveListener(ctx, ln, serve) }()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out, diag bytes.Buffer
			if !runBridge(ctx, socket, strings.NewReader(listRequest), &out, &diag) {
				t.Errorf("bridge did not relay: %s", diag.String())
				return
			}
			if !strings.Contains(out.String(), "faketest://h/") {
				t.Errorf("response lacks the root: %q", out.String())
			}
		}()
	}
	wg.Wait()

	cancel()
	if err := <-served; err != context.Canceled {
		t.Fatalf("serveListener = %v, want context.Canceled", err)
	}
}

// TestRunBridge_NoDaemonFallsBack reports false without touching the streams,
// so the caller can serve in-process.
func TestRunBridge_NoDaemonFallsBack(t *testing.T) {
	var out, diag bytes.Buffer
	in := strings.NewReader(listRequest)
	if runBridge(context.Background(), filepath.Join(t.TempDir(), "absent.sock"), in, &out, &diag) {
		t.Fatal("bridge claimed to relay with no daemon")
	}
	if out.Len() != 0 {
		t.Errorf("stdout written on fallback: %q", out.String())
	}
	rest, _ := io.ReadAll(in)
	if string(rest) != listRequest {
		t.Errorf("stdin consumed on fallback: %q left", rest)
	}
}

// TestListenOwnerOnly_RefusesLiveListener keeps a second daemon from
// displacing the first.
func TestListenOwnerOnly_RefusesLiveListener(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "mcp.sock")
	ln, err := listenOwnerOnly(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	if _, err := listenOwnerOnly(socket); err == nil {
		t.Fatal("second listener displaced a live one")
	}
}
