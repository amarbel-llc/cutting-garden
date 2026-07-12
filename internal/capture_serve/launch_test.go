package capture_serve_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/amarbel-llc/cutting-garden/internal/capture_serve"
	testpeer "github.com/amarbel-llc/cutting-garden/internal/capture_serve_testpeer"
)

// testPeerModeEnv re-execs THIS test binary as the launched plugin: when
// set, TestMain runs a peer behavior instead of the test suite. The
// "serve" mode is the real packaged peer (capture_serve_testpeer.Main,
// the same code cutting-garden-test-capture-serve ships); the failure
// modes exercise Launch's bring-up guards.
const testPeerModeEnv = "CG_CAPTURE_SERVE_TESTPEER"

func TestMain(m *testing.M) {
	switch mode := os.Getenv(testPeerModeEnv); mode {
	case "":
		os.Exit(m.Run())
	case "serve":
		os.Exit(testpeer.Main())
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

// TestLaunch_TwoProcessByteIdentity is the spawned-process form of the
// conformance bar: Run launches the re-exec'd test peer as a real child
// process, the whole handshake and blob protocol cross a process
// boundary, and the resulting tree must equal the in-process reference —
// digests and stored-blob counts both.
func TestLaunch_TwoProcessByteIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	refStore := testpeer.NewMemStore()
	wantDigest, _, err := testpeer.EmitFixedReceipt(ctx, refStore)
	if err != nil {
		t.Fatalf("reference emit: %v", err)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv(testPeerModeEnv, "serve")

	gotStore := testpeer.NewMemStore()
	result, err := capture_serve.Run(ctx, gotStore, capture_serve.BatchParams{
		Target: "test://fixture",
		Captures: []capture_serve.CaptureSpec{
			{Name: "cg", Format: "fixed"},
		},
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
	if got, want := len(gotStore.Snapshot()), len(refStore.Snapshot()); got != want {
		t.Errorf("stored %d blobs, want %d", got, want)
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
	_, err = capture_serve.Launch(ctx, self)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected launch failure, got session")
	}
	if elapsed > 5*time.Second {
		t.Errorf("bring-up failure took %s; must be fast for the v2→v1 fallback", elapsed)
	}

	// Through Run, the same failure classifies as a fallback signal.
	_, err = capture_serve.Run(ctx, testpeer.NewMemStore(),
		capture_serve.BatchParams{
			Captures: []capture_serve.CaptureSpec{{Name: "cg", Format: "fixed"}},
		}, self)
	if err == nil {
		t.Fatal("expected Run bring-up failure, got nil")
	}
	if !capture_serve.IsFallbackSignal(err) {
		t.Error("a bring-up failure through Run must satisfy IsFallbackSignal")
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
	_, err = capture_serve.Launch(ctx, self)
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

// TestLaunch_NoBatchTeardownReapsChild pins that abandoning a session
// right after bring-up (no batch, no shutdown notification) still reaps
// the child promptly instead of hanging. The child's exit code is
// deliberately NOT asserted: conn-close and stdin-EOF race, and a
// close-without-shutdown is cancellation, which may exit nonzero. The
// deterministic stdin-EOF-exits-0 form (no connection at all) is the
// bats bring-up smoke's job.
func TestLaunch_NoBatchTeardownReapsChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv(testPeerModeEnv, "serve")

	sess, err := capture_serve.Launch(ctx, self)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	start := time.Now()
	_ = sess.Close()
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("teardown took %s; child was not reaped promptly", elapsed)
	}
}
