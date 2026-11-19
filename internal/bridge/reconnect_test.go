package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeServer is a line-protocol stand-in for the shared server: it answers
// `initialize` and any other request, never answers `hang`, and closes the
// connection on `die`. It records every line it receives.
type fakeServer struct {
	t     *testing.T
	ln    net.Listener
	mu    sync.Mutex
	conns []net.Conn
	lines []string
	// silentInit makes the server read initialize and never answer it.
	silentInit bool
}

func startFake(t *testing.T, socket string) *fakeServer {
	t.Helper()
	_ = os.Remove(socket)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeServer{t: t, ln: ln}
	go f.accept()
	return f
}

func (f *fakeServer) accept() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conns = append(f.conns, c)
		f.mu.Unlock()
		go f.serve(c)
	}
}

func (f *fakeServer) serve(c net.Conn) {
	r := bufio.NewReader(c)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			// The client closed its side: hang up, as the real server does
			// once its session ends.
			_ = c.Close()
			return
		}
		f.mu.Lock()
		f.lines = append(f.lines, strings.TrimSpace(line))
		f.mu.Unlock()
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal([]byte(line), &m) != nil || len(m.ID) == 0 {
			continue // a notification or a hello: no reply
		}
		switch m.Method {
		case "hang":
		case "initialize":
			if f.silentInit {
				continue
			}
			_, _ = io.WriteString(c, `{"jsonrpc":"2.0","id":`+string(m.ID)+`,"result":{"ok":true}}`+"\n")
		case "die":
			_ = c.Close()
			return
		default:
			_, _ = io.WriteString(c, `{"jsonrpc":"2.0","id":`+string(m.ID)+`,"result":{"ok":true}}`+"\n")
		}
	}
}

// stop ends the server and every connection, like a crash or a unit restart.
func (f *fakeServer) stop() {
	_ = f.ln.Close()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		_ = c.Close()
	}
}

func (f *fakeServer) received() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.lines...)
}

// lineBuffer collects the bridge's output lines.
type lineBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lineBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lineBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func (l *lineBuffer) waitFor(t *testing.T, substr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(l.String(), substr) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("output never contained %q; got:\n%s", substr, l.String())
}

// attached starts the bridge against socket with a client stdin pipe.
type attached struct {
	in      *io.PipeWriter
	out     *lineBuffer
	diag    *lineBuffer
	outcome chan Outcome
}

func attach(t *testing.T, socket, hello string) *attached {
	t.Helper()
	pr, pw := io.Pipe()
	a := &attached{in: pw, out: &lineBuffer{}, diag: &lineBuffer{}, outcome: make(chan Outcome, 1)}
	go func() { a.outcome <- Run(context.Background(), socket, hello, pr, a.out, a.diag) }()
	t.Cleanup(func() { _ = pw.Close() })
	return a
}

func (a *attached) send(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(a.in, line+"\n"); err != nil {
		t.Fatal(err)
	}
}

func (a *attached) wantOutcome(t *testing.T, want Outcome, within time.Duration) {
	t.Helper()
	select {
	case got := <-a.outcome:
		if got != want {
			t.Fatalf("outcome = %v, want %v\ndiag:\n%s", got, want, a.diag.String())
		}
	case <-time.After(within):
		t.Fatalf("bridge did not end within %v\ndiag:\n%s", within, a.diag.String())
	}
}

func shortSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "brg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

func setReconnectFor(t *testing.T, d time.Duration) {
	t.Helper()
	old := ReconnectFor
	ReconnectFor = d
	t.Cleanup(func() { ReconnectFor = old })
}

const (
	initReq  = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	initDone = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
)

// The server dies and comes back while the client stays attached: the bridge
// reconnects, replays initialize and initialized so the new server session is
// ready, and the client sees neither an EOF nor a second initialize reply.
func TestReconnect_ServerRestartIsInvisibleToTheClient(t *testing.T) {
	setReconnectFor(t, 5*time.Second)
	socket := shortSocket(t)
	a := startFake(t, socket)
	br := attach(t, socket, HelloLine("sess-1"))

	br.send(t, initReq)
	br.out.waitFor(t, `"id":1`)
	br.send(t, initDone)
	time.Sleep(100 * time.Millisecond)

	a.stop()
	b := startFake(t, socket)
	defer b.stop()

	br.send(t, `{"jsonrpc":"2.0","id":2,"method":"resources/list"}`)
	br.out.waitFor(t, `"id":2`)

	got := strings.Join(b.received(), "\n")
	for _, want := range []string{`"principal":"sess-1"`, `"method":"initialize"`, `notifications/initialized`} {
		if !strings.Contains(got, want) {
			t.Errorf("the new server never received %s:\n%s", want, got)
		}
	}
	if n := strings.Count(br.out.String(), `"id":1`); n != 1 {
		t.Errorf("client saw %d initialize replies, want exactly 1:\n%s", n, br.out.String())
	}
	select {
	case o := <-br.outcome:
		t.Fatalf("bridge ended (%v) though the server came back", o)
	default:
	}
	_ = br.in.Close()
	br.wantOutcome(t, Done, 5*time.Second)
}

// A request that was in flight when the server died can never be answered by
// the new one: the client gets a JSON-RPC error for its id, not silence.
func TestReconnect_InFlightRequestGetsAnError(t *testing.T) {
	setReconnectFor(t, 300*time.Millisecond)
	socket := shortSocket(t)
	a := startFake(t, socket)
	br := attach(t, socket, "")

	br.send(t, initReq)
	br.out.waitFor(t, `"id":1`)
	br.send(t, `{"jsonrpc":"2.0","id":7,"method":"hang"}`)
	time.Sleep(100 * time.Millisecond)
	a.stop()

	br.out.waitFor(t, `"id":7`)
	if !strings.Contains(br.out.String(), `"error"`) {
		t.Errorf("the lost request's reply is not an error:\n%s", br.out.String())
	}
	// The server never comes back: after the window the bridge gives up.
	br.wantOutcome(t, DaemonLost, 5*time.Second)
}

// If the server dies before it answered initialize, the replayed initialize
// is the answer: the client gets one reply and no error for that id.
func TestReconnect_LostInitializeIsAnsweredByTheReplay(t *testing.T) {
	setReconnectFor(t, 5*time.Second)
	socket := shortSocket(t)
	a := startFake(t, socket)
	a.silentInit = true
	br := attach(t, socket, "")

	br.send(t, initReq)
	time.Sleep(150 * time.Millisecond) // A has read it and said nothing
	a.stop()
	b := startFake(t, socket)
	defer b.stop()

	br.out.waitFor(t, `"id":1`) // B's answer to the replayed initialize
	if strings.Contains(br.out.String(), `"error"`) {
		t.Errorf("the client got an error for initialize:\n%s", br.out.String())
	}
	if n := strings.Count(br.out.String(), `"id":1`); n != 1 {
		t.Errorf("client saw %d initialize replies, want 1:\n%s", n, br.out.String())
	}
}

// When the server never returns, the bridge ends with DaemonLost once the
// window has passed, so its host sees the child die.
func TestReconnect_GivesUpAfterTheWindow(t *testing.T) {
	setReconnectFor(t, 300*time.Millisecond)
	socket := shortSocket(t)
	a := startFake(t, socket)
	br := attach(t, socket, "")
	br.send(t, initReq)
	br.out.waitFor(t, `"id":1`)
	a.stop()
	br.wantOutcome(t, DaemonLost, 5*time.Second)
}
