package capture_serve

import (
	"fmt"
	"strings"
	"testing"
)

// TestHandshake_AnnounceRoundTrip pins the line shape end-to-end: a fresh
// cookie, a rendered announce, and a parse that recovers every field.
func TestHandshake_AnnounceRoundTrip(t *testing.T) {
	cookie, err := NewCookie()
	if err != nil {
		t.Fatalf("NewCookie: %v", err)
	}
	in := Handshake{
		Version:  SchemaV2,
		Network:  HandshakeNetwork,
		Address:  "/tmp/cg-serve-x/s",
		Metadata: "pid=123",
	}
	line, err := AnnounceLine(cookie, in)
	if err != nil {
		t.Fatalf("AnnounceLine: %v", err)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Errorf("announce line is not newline-terminated: %q", line)
	}
	if strings.Count(line, "|") != announceFields-1 {
		t.Errorf("announce line has wrong delimiter count: %q", line)
	}

	out, err := ParseAnnounceLine(line, cookie)
	if err != nil {
		t.Fatalf("ParseAnnounceLine: %v", err)
	}
	if out != in {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", out, in)
	}
}

// TestHandshake_CookieMismatchRejected pins the magic-cookie guard.
func TestHandshake_CookieMismatchRejected(t *testing.T) {
	line, err := AnnounceLine("expected-cookie", Handshake{
		Version: SchemaV2,
		Network: HandshakeNetwork,
		Address: "/tmp/x/s",
	})
	if err != nil {
		t.Fatalf("AnnounceLine: %v", err)
	}
	if _, err := ParseAnnounceLine(line, "different-cookie"); err == nil {
		t.Fatal("expected cookie-mismatch rejection, got nil")
	}
}

// TestHandshake_PollutionRejected pins that stdout noise — a log line, a
// wrong subprotocol, an empty line — never parses as an announce. This is
// the guard the fast-and-unambiguous v2→v1 fallback rests on.
func TestHandshake_PollutionRejected(t *testing.T) {
	cases := map[string]string{
		"log line":          "starting capture-serve on /tmp/x\n",
		"empty":             "\n",
		"too few fields":    "cookie|v2|unixpacket\n",
		"too many fields":   "c|v|n|a|m|extra|capture-plugin\n",
		"wrong subprotocol": "c|" + SchemaV2 + "|unixpacket|/tmp/x/s||sftp\n",
		"empty address":     "c|" + SchemaV2 + "|unixpacket|||capture-plugin\n",
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseAnnounceLine(line, "c"); err == nil {
				t.Fatalf("polluted line parsed as announce: %q", line)
			}
		})
	}
}

// TestHandshake_ReadAnnounceTakesFirstLine pins ReadAnnounce semantics:
// the FIRST line decides — a valid announce parses even with trailing
// noise after it, and a polluted first line rejects even when a valid
// announce follows.
func TestHandshake_ReadAnnounceTakesFirstLine(t *testing.T) {
	valid, err := AnnounceLine("c", Handshake{
		Version: SchemaV2,
		Network: HandshakeNetwork,
		Address: "/tmp/x/s",
	})
	if err != nil {
		t.Fatalf("AnnounceLine: %v", err)
	}

	if _, err := ReadAnnounce(
		strings.NewReader(valid+"later diagnostic noise\n"), "c",
	); err != nil {
		t.Errorf("valid first line rejected: %v", err)
	}

	if _, err := ReadAnnounce(
		strings.NewReader("boot log\n"+valid), "c",
	); err == nil {
		t.Error("polluted first line accepted despite later valid announce")
	}
}

// TestHandshake_DelimiterInFieldRejectedAtEmit pins that a field carrying
// the delimiter can never be rendered into an ambiguous line.
func TestHandshake_DelimiterInFieldRejectedAtEmit(t *testing.T) {
	_, err := AnnounceLine("c", Handshake{
		Version:  SchemaV2,
		Network:  HandshakeNetwork,
		Address:  "/tmp/x/s",
		Metadata: "a|b",
	})
	if err == nil {
		t.Fatal("metadata containing '|' rendered without error")
	}
}

// TestHandshake_CookieFromEnv pins both sides of the launch guard.
func TestHandshake_CookieFromEnv(t *testing.T) {
	t.Setenv(CookieEnv, "abc123")
	cookie, err := CookieFromEnv()
	if err != nil {
		t.Fatalf("CookieFromEnv with env set: %v", err)
	}
	if cookie != "abc123" {
		t.Errorf("cookie = %q, want abc123", cookie)
	}

	t.Setenv(CookieEnv, "")
	if _, err := CookieFromEnv(); err == nil {
		t.Fatal("expected error with unset cookie env, got nil")
	}
}

// TestHandshake_ListenAnnounceDial is the integration pin: bind a
// rendezvous socket, render+parse the announce, dial it, and prove the
// dialed connection is the listener's by exchanging one datagram.
func TestHandshake_ListenAnnounceDial(t *testing.T) {
	ln, sock, cleanup, err := ListenRendezvous()
	if err != nil {
		t.Fatalf("ListenRendezvous: %v", err)
	}
	defer cleanup()

	if len(sock) > sunPathMax {
		t.Errorf("rendezvous path %q exceeds sun_path bound", sock)
	}

	cookie, err := NewCookie()
	if err != nil {
		t.Fatalf("NewCookie: %v", err)
	}
	line, err := AnnounceLine(cookie, Handshake{
		Version: SchemaV2,
		Network: HandshakeNetwork,
		Address: sock,
	})
	if err != nil {
		t.Fatalf("AnnounceLine: %v", err)
	}
	h, err := ParseAnnounceLine(line, cookie)
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
		defer conn.Close()
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

	conn, err := DialAnnounced(h)
	if err != nil {
		t.Fatalf("DialAnnounced: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write over dialed conn: %v", err)
	}
	if err := <-accepted; err != nil {
		t.Fatalf("accept side: %v", err)
	}

	// cleanup unlinks the socket: a second dial must fail.
	cleanup()
	if _, err := DialAnnounced(h); err == nil {
		t.Error("dial succeeded after cleanup unlinked the socket")
	}
}

// TestHandshake_DialAnnouncedRejectsForeignNetwork pins that only the
// SEQPACKET rendezvous family is dialable — a stream or tcp announce is a
// protocol violation, not a silent downgrade.
func TestHandshake_DialAnnouncedRejectsForeignNetwork(t *testing.T) {
	_, err := DialAnnounced(Handshake{
		Version: SchemaV2,
		Network: "tcp",
		Address: "127.0.0.1:1",
	})
	if err == nil {
		t.Fatal("expected rejection of non-unixpacket network, got nil")
	}
}
