package plugin_handshake

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// testProto is a fake protocol identity: the tests here pin the
// parameterized launch logic, not any real protocol's constants (those
// stay pinned in each consumer's own tests, e.g. capture_serve).
var testProto = Protocol{
	CookieEnv:    "X_COOKIE",
	Network:      "unix",
	Subprotocol:  "test-proto",
	SocketPrefix: "cg-test-",
}

// TestProtocol_AnnounceRoundTrip pins the line shape end-to-end: a fresh
// cookie, a rendered announce, and a parse that recovers every field.
func TestProtocol_AnnounceRoundTrip(t *testing.T) {
	cookie, err := testProto.NewCookie()
	if err != nil {
		t.Fatalf("NewCookie: %v", err)
	}
	in := Handshake{
		Version:  "test/v1",
		Network:  testProto.Network,
		Address:  "/tmp/cg-test-x/s",
		Metadata: "pid=123",
	}
	line, err := testProto.AnnounceLine(cookie, in)
	if err != nil {
		t.Fatalf("AnnounceLine: %v", err)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Errorf("announce line is not newline-terminated: %q", line)
	}
	if strings.Count(line, "|") != announceFields-1 {
		t.Errorf("announce line has wrong delimiter count: %q", line)
	}

	out, err := testProto.ParseAnnounceLine(line, cookie)
	if err != nil {
		t.Fatalf("ParseAnnounceLine: %v", err)
	}
	if out != in {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", out, in)
	}
}

// TestProtocol_CookieMismatchRejected pins the magic-cookie guard.
func TestProtocol_CookieMismatchRejected(t *testing.T) {
	line, err := testProto.AnnounceLine("expected-cookie", Handshake{
		Version: "test/v1",
		Network: testProto.Network,
		Address: "/tmp/x/s",
	})
	if err != nil {
		t.Fatalf("AnnounceLine: %v", err)
	}
	if _, err := testProto.ParseAnnounceLine(line, "different-cookie"); err == nil {
		t.Fatal("expected cookie-mismatch rejection, got nil")
	}
}

// TestProtocol_PollutionRejected pins that stdout noise — a log line, a
// wrong subprotocol, an empty line — never parses as an announce.
func TestProtocol_PollutionRejected(t *testing.T) {
	cases := map[string]string{
		"log line":          "starting test-serve on /tmp/x\n",
		"empty":             "\n",
		"too few fields":    "cookie|v1|unix\n",
		"too many fields":   "c|v|n|a|m|extra|test-proto\n",
		"wrong subprotocol": "c|test/v1|unix|/tmp/x/s||sftp\n",
		"empty address":     "c|test/v1|unix|||test-proto\n",
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := testProto.ParseAnnounceLine(line, "c"); err == nil {
				t.Fatalf("polluted line parsed as announce: %q", line)
			}
		})
	}
}

// TestProtocol_ReadAnnounceTakesFirstLine pins ReadAnnounce semantics:
// the FIRST line decides — a valid announce parses even with trailing
// noise after it, and a polluted first line rejects even when a valid
// announce follows.
func TestProtocol_ReadAnnounceTakesFirstLine(t *testing.T) {
	valid, err := testProto.AnnounceLine("c", Handshake{
		Version: "test/v1",
		Network: testProto.Network,
		Address: "/tmp/x/s",
	})
	if err != nil {
		t.Fatalf("AnnounceLine: %v", err)
	}

	if _, err := testProto.ReadAnnounce(
		strings.NewReader(valid+"later diagnostic noise\n"), "c",
	); err != nil {
		t.Errorf("valid first line rejected: %v", err)
	}

	if _, err := testProto.ReadAnnounce(
		strings.NewReader("boot log\n"+valid), "c",
	); err == nil {
		t.Error("polluted first line accepted despite later valid announce")
	}
}

// TestProtocol_ReadAnnounceEOFBeforeLine pins the child-exited-early
// path: EOF before any line is a rejected handshake, not a panic — and
// the io.EOF must survive dewey's refusal to wrap it (the
// ErrorWithStackf-not-Wrap pattern in ReadAnnounce).
func TestProtocol_ReadAnnounceEOFBeforeLine(t *testing.T) {
	if _, err := testProto.ReadAnnounce(strings.NewReader(""), "c"); err == nil {
		t.Fatal("expected error on EOF before announce, got nil")
	}
}

// TestProtocol_DelimiterInFieldRejectedAtEmit pins that a field carrying
// the delimiter can never be rendered into an ambiguous line.
func TestProtocol_DelimiterInFieldRejectedAtEmit(t *testing.T) {
	_, err := testProto.AnnounceLine("c", Handshake{
		Version:  "test/v1",
		Network:  testProto.Network,
		Address:  "/tmp/x/s",
		Metadata: "a|b",
	})
	if err == nil {
		t.Fatal("metadata containing '|' rendered without error")
	}
}

// TestProtocol_CookieFromEnv pins both sides of the launch guard against
// the protocol's own cookie env var.
func TestProtocol_CookieFromEnv(t *testing.T) {
	t.Setenv(testProto.CookieEnv, "abc123")
	cookie, err := testProto.CookieFromEnv()
	if err != nil {
		t.Fatalf("CookieFromEnv with env set: %v", err)
	}
	if cookie != "abc123" {
		t.Errorf("cookie = %q, want abc123", cookie)
	}

	t.Setenv(testProto.CookieEnv, "")
	if _, err := testProto.CookieFromEnv(); err == nil {
		t.Fatal("expected error with unset cookie env, got nil")
	}
}

// TestProtocol_ListenAnnounceDial is the integration pin: bind a
// rendezvous socket under the protocol's prefix, render+parse the
// announce, dial it, and prove the dialed connection is the listener's
// by exchanging one message. cleanup must unlink the socket directory.
func TestProtocol_ListenAnnounceDial(t *testing.T) {
	ln, sock, cleanup, err := testProto.ListenRendezvous()
	if err != nil {
		t.Fatalf("ListenRendezvous: %v", err)
	}
	defer cleanup()

	if len(sock) > sunPathMax {
		t.Errorf("rendezvous path %q exceeds sun_path bound", sock)
	}
	if !strings.Contains(sock, testProto.SocketPrefix) {
		t.Errorf("rendezvous path %q does not use prefix %q",
			sock, testProto.SocketPrefix)
	}

	cookie, err := testProto.NewCookie()
	if err != nil {
		t.Fatalf("NewCookie: %v", err)
	}
	line, err := testProto.AnnounceLine(cookie, Handshake{
		Version: "test/v1",
		Network: testProto.Network,
		Address: sock,
	})
	if err != nil {
		t.Fatalf("AnnounceLine: %v", err)
	}
	h, err := testProto.ParseAnnounceLine(line, cookie)
	if err != nil {
		t.Fatalf("ParseAnnounceLine: %v", err)
	}

	accepted := make(chan error, 1)
	go func() {
		conn, aerr := ln.Accept()
		if aerr != nil {
			accepted <- aerr
			return
		}
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 16)
		n, rerr := conn.Read(buf)
		if rerr != nil {
			accepted <- rerr
			return
		}
		if string(buf[:n]) != "ping" {
			accepted <- fmt.Errorf("got %q, want ping", string(buf[:n]))
			return
		}
		accepted <- nil
	}()

	conn, err := testProto.DialAnnounced(h)
	if err != nil {
		t.Fatalf("DialAnnounced: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write over dialed conn: %v", err)
	}
	if err := <-accepted; err != nil {
		t.Fatalf("accept side: %v", err)
	}

	// cleanup unlinks the socket AND its directory: a second dial must
	// fail and the directory must be gone.
	sockDir := strings.TrimSuffix(sock, "/s")
	cleanup()
	if _, err := testProto.DialAnnounced(h); err == nil {
		t.Error("dial succeeded after cleanup unlinked the socket")
	}
	if _, err := os.Stat(sockDir); !os.IsNotExist(err) {
		t.Errorf("socket dir %q survives cleanup (stat err: %v)", sockDir, err)
	}
}

// TestProtocol_DialAnnouncedRejectsForeignNetwork pins that only the
// protocol's own socket family is dialable — a foreign network in the
// announce is a protocol violation, not a silent downgrade.
func TestProtocol_DialAnnouncedRejectsForeignNetwork(t *testing.T) {
	_, err := testProto.DialAnnounced(Handshake{
		Version: "test/v1",
		Network: "tcp",
		Address: "127.0.0.1:1",
	})
	if err == nil {
		t.Fatal("expected rejection of foreign network, got nil")
	}
}
