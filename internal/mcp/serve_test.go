package mcp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/internal/bridge"
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
	var log syncLog
	go func() { served <- serveListener(ctx, ln, serve, log.logf) }()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out, diag bytes.Buffer
			// Half the sessions announce themselves, half behave as an older
			// bridge that sends no hello: both must be served.
			hello := ""
			if i%2 == 0 {
				hello = bridge.HelloLine(fmt.Sprintf("sess-%d", i))
			}
			if got := bridge.Run(ctx, socket, hello, strings.NewReader(listRequest), &out, &diag); got != bridge.Done {
				t.Errorf("bridge outcome = %v, want Done: %s", got, diag.String())
				return
			}
			if !strings.Contains(out.String(), "faketest://h/") {
				t.Errorf("response lacks the root: %q", out.String())
			}
			if strings.Contains(out.String(), "cutting-garden/hello") {
				t.Errorf("hello reached the MCP server: %q", out.String())
			}
		}()
	}
	wg.Wait()

	cancel()
	if err := <-served; err != context.Canceled {
		t.Fatalf("serveListener = %v, want context.Canceled", err)
	}
	for _, want := range []string{`principal="sess-0"`, `principal="sess-2"`, `principal=""`} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log lacks %s:\n%s", want, log.String())
		}
	}
}

// syncLog collects serve-mcp's log lines from concurrent sessions.
type syncLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *syncLog) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(&l.b, format+"\n", args...)
}

func (l *syncLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// TestConsumeHello_ShortFirstLineIsNotHeldBack is the stall regression: a
// first message shorter than the hello prefix (a ping) must reach the server
// whole and at once, not wait for more bytes.
func TestConsumeHello_ShortFirstLineIsNotHeldBack(t *testing.T) {
	const ping = `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"
	principal, rest := consumeHello(strings.NewReader(ping))
	if principal != "" {
		t.Errorf("principal = %q, want none", principal)
	}
	got, _ := io.ReadAll(rest)
	if string(got) != ping {
		t.Errorf("first line altered: %q", got)
	}
}

// TestRunBridge_DaemonLostMidSession: the daemon hangs up while the client is
// still connected and never comes back; once the reconnect window has passed
// the bridge must say so, not wait for the next client message.
func TestRunBridge_DaemonLostMidSession(t *testing.T) {
	defer func(old time.Duration) { bridge.ReconnectFor = old }(bridge.ReconnectFor)
	bridge.ReconnectFor = 200 * time.Millisecond

	socket := filepath.Join(t.TempDir(), "mcp.sock")
	ln, err := listenOwnerOnly(socket)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		c, err := ln.Accept()
		if err == nil {
			_ = c.Close() // the daemon "dies"
		}
		_ = ln.Close() // and does not come back
	}()

	pr, pw := io.Pipe() // a client that stays connected and silent
	defer func() { _ = pw.Close() }()
	done := make(chan bridge.Outcome, 1)
	go func() {
		var out, diag bytes.Buffer
		done <- bridge.Run(context.Background(), socket, "", pr, &out, &diag)
	}()
	select {
	case got := <-done:
		if got != bridge.DaemonLost {
			t.Fatalf("outcome = %v, want DaemonLost", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bridge did not notice the daemon hanging up")
	}
}

// TestRunBridge_NoDaemonFallsBack reports false without touching the streams,
// so the caller can serve in-process.
func TestRunBridge_NoDaemonFallsBack(t *testing.T) {
	var out, diag bytes.Buffer
	in := strings.NewReader(listRequest)
	if got := bridge.Run(context.Background(), filepath.Join(t.TempDir(), "absent.sock"), "", in, &out, &diag); got != bridge.NoDaemon {
		t.Fatalf("outcome = %v, want NoDaemon", got)
	}
	if out.Len() != 0 {
		t.Errorf("stdout written on fallback: %q", out.String())
	}
	rest, _ := io.ReadAll(in)
	if string(rest) != listRequest {
		t.Errorf("stdin consumed on fallback: %q left", rest)
	}
}

// TestServeListener_ShutdownDoesNotWaitForASilentClient is the unit-stop
// regression: a connected client that never sends or closes must not keep the
// daemon alive once its context ends.
func TestServeListener_ShutdownDoesNotWaitForASilentClient(t *testing.T) {
	serve, _ := sharedServe(t)
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
	// Let the daemon accept it and block reading the first line.
	time.Sleep(200 * time.Millisecond)

	cancel()
	select {
	case err := <-served:
		if err != context.Canceled {
			t.Errorf("serveListener = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon kept running with a silent client connected")
	}
}

func TestApplyAgentSocket(t *testing.T) {
	t.Setenv("PIGGY_AUTH_SOCK", "unchanged")
	if err := applyAgentSocket(""); err != nil || os.Getenv("PIGGY_AUTH_SOCK") != "unchanged" {
		t.Fatalf("empty path touched the environment: err=%v env=%q", err, os.Getenv("PIGGY_AUTH_SOCK"))
	}
	if err := applyAgentSocket("relative/agent.sock"); err == nil {
		t.Error("a relative path was accepted")
	}
	if os.Getenv("PIGGY_AUTH_SOCK") != "unchanged" {
		t.Error("a rejected path still changed the environment")
	}
	// A socket that does not exist (yet) is fine: the unit may start before
	// the operator connects.
	absent := filepath.Join(t.TempDir(), "absent.sock")
	if err := applyAgentSocket(absent); err != nil {
		t.Fatalf("absent socket refused: %v", err)
	}
	if got := os.Getenv("PIGGY_AUTH_SOCK"); got != absent {
		t.Errorf("PIGGY_AUTH_SOCK = %q, want %q", got, absent)
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
	defer func() { _ = ln.Close() }()
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
