package mcp

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/internal/bridge"
	"code.linenisgreat.com/purse-first/libs/go-mcp/server"
	"code.linenisgreat.com/purse-first/libs/go-mcp/transport"
)

// Write is syncLog as an io.Writer, so it can stand in for Tools.log.
func (l *syncLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

// A write made through a serve-mcp connection logs who made it; a stdio
// session's log is unchanged (its own stderr already says whose it is).
func TestMutateNodes_LogLineNamesTheCaller(t *testing.T) {
	var log strings.Builder
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.log = &log
	ctx := withCaller(context.Background(), Caller{Principal: "sess-A", HasPeer: true, PID: 7, UID: 1000})

	if _, err := tools.CallTool(ctx, "mutate_nodes", []byte(
		`{"groups":[{"anchor":"faketest://h/","ops":[{"kind":"put","id":"a.ics","body":"x"}]}]}`,
	)); err != nil {
		t.Fatal(err)
	}
	want := `mutate_nodes faketest://h/ op 1/1: applied a.ics faketest://h/a.ics principal="sess-A" pid=7 uid=1000`
	if !strings.Contains(log.String(), want) {
		t.Errorf("log lacks %q:\n%s", want, log.String())
	}
}

// Two sessions write at once through one daemon: each line carries its own
// principal and never the other's. This also proves go-mcp hands the
// connection's context to the tool handler.
func TestServeListener_WriteLogsCarryTheirOwnPrincipal(t *testing.T) {
	var writes syncLog
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.log = &writes

	socket := filepath.Join(t.TempDir(), "mcp.sock")
	ln, err := listenOwnerOnly(socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var daemonLog syncLog
	served := make(chan error, 1)
	go func() { served <- serveListener(ctx, ln, serveTools(tools), daemonLog.logf) }()

	var wg sync.WaitGroup
	for _, principal := range []string{"sess-A", "sess-B", "sess-C", "sess-D"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := fmt.Sprintf(
				`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"mutate_nodes","arguments":`+
					`{"groups":[{"anchor":"faketest://h/","ops":[{"kind":"put","id":"%s.ics","body":"x"}]}]}}}`+"\n",
				principal,
			)
			var out, diag bytes.Buffer
			if got := bridge.Run(ctx, socket, bridge.HelloLine(principal), strings.NewReader(req), &out, &diag); got != bridge.Done {
				t.Errorf("%s: outcome %v: %s", principal, got, diag.String())
			}
		}()
	}
	wg.Wait()
	cancel()
	<-served

	for _, line := range strings.Split(strings.TrimSpace(writes.String()), "\n") {
		for _, p := range []string{"sess-A", "sess-B", "sess-C", "sess-D"} {
			own := strings.Contains(line, " "+p+".ics ")
			named := strings.Contains(line, fmt.Sprintf("principal=%q", p))
			if own != named {
				t.Errorf("line for %s misattributed: %s", p, line)
			}
		}
	}
	for _, p := range []string{"sess-A", "sess-B", "sess-C", "sess-D"} {
		if !strings.Contains(writes.String(), p+".ics") {
			t.Errorf("no write logged for %s:\n%s", p, writes.String())
		}
	}
	if !strings.Contains(writes.String(), "pid=") {
		t.Errorf("no peer pid in the write log:\n%s", writes.String())
	}
}

// serveTools is a serveFunc over a Tools instance, as prepareServer builds.
func serveTools(tools *Tools) serveFunc {
	return func(ctx context.Context, tr transport.Transport) error {
		srv, err := server.New(tr, server.Options{
			ServerName:        serverName,
			Tools:             tools,
			PreferV1Providers: true,
		})
		if err != nil {
			return err
		}
		return srv.Run(ctx)
	}
}
