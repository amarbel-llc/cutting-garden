package capture_serve

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// testPeerModeEnv re-execs THIS test binary as the launched plugin: when
// set, TestMain runs a peer behavior instead of the test suite. The
// two-process launch tests exercise the real spawn → announce → dial →
// serve path without a separately built binary (the packaged
// cutting-garden-test-capture-serve + bats lane covers that form).
const testPeerModeEnv = "CG_CAPTURE_SERVE_TESTPEER"

func TestMain(m *testing.M) {
	switch mode := os.Getenv(testPeerModeEnv); mode {
	case "":
		os.Exit(m.Run())
	case "serve":
		os.Exit(testPeerServe())
	case "exit2":
		// A binary without the capture-serve subcommand: immediate
		// nonzero exit, nothing on stdout.
		fmt.Fprintln(os.Stderr, "unknown subcommand capture-serve")
		os.Exit(2)
	case "pollute":
		// A misbehaving plugin: stdout noise instead of the announce,
		// then a hang — Launch must reject and kill it.
		fmt.Println("starting capture-serve, please wait...")
		time.Sleep(time.Minute)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown %s mode %q\n", testPeerModeEnv, mode)
		os.Exit(2)
	}
}

// testPeerServe is the launched plugin side: the full out-of-tree
// bring-up sequence (cookie guard → rendezvous listen → announce →
// accept → Serve) around the fixed-receipt batch fixture.
func testPeerServe() int {
	cookie, err := CookieFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ln, sock, cleanup, err := ListenRendezvous()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer cleanup()

	line, err := AnnounceLine(cookie, Handshake{
		Version: SchemaV2,
		Network: HandshakeNetwork,
		Address: sock,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if _, err := os.Stdout.WriteString(line); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	conn, err := ln.AcceptUnix()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := Serve(context.Background(), conn, testServeConfig()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// TestLaunch_TwoProcessByteIdentity is the spawned-process form of the
// conformance bar: Run launches the re-exec'd test peer as a real child
// process, the whole handshake and blob protocol cross a process
// boundary, and the resulting tree must equal the in-process reference —
// digests and stored bytes both.
func TestLaunch_TwoProcessByteIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	refStore := newFakeStore()
	wantDigest, _, err := emitFixedReceipt(ctx, refStore)
	if err != nil {
		t.Fatalf("reference emit: %v", err)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv(testPeerModeEnv, "serve")

	gotStore := newFakeStore()
	result, err := Run(ctx, gotStore, BatchParams{
		Target:   "test://fixture",
		Captures: []CaptureSpec{{Name: "cg", Format: "fixed"}},
	}, self)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(result.Captures) != 1 || result.Captures[0].Receipt == nil {
		t.Fatalf("unexpected batch result: %+v", result)
	}
	if got := result.Captures[0].Receipt.ID; got != wantDigest {
		t.Errorf("receipt id = %s, want %s", got, wantDigest)
	}
	if len(gotStore.blobs) != len(refStore.blobs) {
		t.Errorf("stored %d blobs, want %d",
			len(gotStore.blobs), len(refStore.blobs))
	}
}

// TestLaunch_MissingSubcommandFailsFast pins the fallback latency
// promise: a child that exits without announcing fails the launch well
// inside the announce deadline, so every capture briefly attempting v2
// against an old chrest costs milliseconds, not seconds.
func TestLaunch_MissingSubcommandFailsFast(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv(testPeerModeEnv, "exit2")

	start := time.Now()
	_, err = Launch(ctx, self)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected launch failure, got session")
	}
	if elapsed > 5*time.Second {
		t.Errorf("bring-up failure took %s; must be fast for the v2→v1 fallback", elapsed)
	}
}

// TestLaunch_PollutedStdoutRejectedAndKilled pins the pollution guard on
// the live path: a child that prints noise instead of the announce is
// rejected — and reaped, not leaked, even though it would happily hang.
func TestLaunch_PollutedStdoutRejectedAndKilled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv(testPeerModeEnv, "pollute")

	start := time.Now()
	_, err = Launch(ctx, self)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected launch rejection on polluted stdout, got session")
	}
	// The child sleeps a minute; returning quickly proves Launch killed
	// and reaped it rather than waiting it out.
	if elapsed > 10*time.Second {
		t.Errorf("polluted launch took %s; child was not killed promptly", elapsed)
	}
}
