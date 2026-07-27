package traversal_serve

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// testPeerModeEnv re-execs THIS test binary as the launched plugin:
// when set, TestMain runs a peer behavior instead of the test suite.
// The "serve" mode is a real RFC 0013 bring-up around Serve over the
// in-package fakeFullPlugin; the failure modes exercise Launch's
// bring-up guards.
const testPeerModeEnv = "CG_TRAVERSAL_SERVE_TESTPEER"

// testPeerConfigOutEnv, when set in serve mode, names a file the
// plugin's ConfigApply writes the received config_toml to verbatim —
// the deterministic passthrough probe (ConfigApply runs before the
// initialize response, so the file exists by the time Launch returns).
const testPeerConfigOutEnv = "CG_TRAVERSAL_SERVE_TESTPEER_CONFIG_OUT"

// testPeerMadderEnvOutEnv names a file the serve-mode peer writes its
// INHERITED MADDER_XDG_USER_LOCATION_ONLY value to at startup, so a test
// can assert Launch set it in the spawned env (cutting-garden#131).
const testPeerMadderEnvOutEnv = "CG_TRAVERSAL_SERVE_TESTPEER_MADDER_ENV_OUT"

func TestMain(m *testing.M) {
	switch mode := os.Getenv(testPeerModeEnv); mode {
	case "":
		os.Exit(m.Run())
	case "serve":
		os.Exit(runTestPeerServe())
	case "exit2":
		// A binary without the traversal-serve subcommand: immediate
		// nonzero exit, nothing on stdout.
		fmt.Fprintln(os.Stderr, "unknown subcommand traversal-serve")
		os.Exit(2)
	case "pollute":
		// A misbehaving plugin: stdout noise instead of the announce,
		// then a hang — Launch must reject and kill it.
		fmt.Println("starting traversal-serve, please wait...")
		time.Sleep(time.Minute)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown %s mode %q\n", testPeerModeEnv, mode)
		os.Exit(2)
	}
}

// runTestPeerServe is the whole plugin process: the RFC 0013 bring-up
// sequence (cookie guard → rendezvous listen → announce on stdout →
// accept) around Serve, with the stdin-EOF lifecycle signal — the same
// structure the Task 7 testpeer packages for out-of-process lanes.
func runTestPeerServe() int {
	// Record the inherited MADDER_XDG_USER_LOCATION_ONLY before anything
	// else, so a test can prove Launch injected it into the spawned env
	// (cutting-garden#131). Done at startup, before the blocking Serve.
	if out := os.Getenv(testPeerMadderEnvOutEnv); out != "" {
		_ = os.WriteFile(
			out, []byte(os.Getenv("MADDER_XDG_USER_LOCATION_ONLY")), 0o600,
		)
	}

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
		Version: SchemaV1,
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

	// stdin EOF is a shutdown signal (RFC 0013 §Launch and
	// rendezvous), armed BEFORE accept so a host that dies (or a smoke
	// test that never dials) unblocks the accept via the listener close
	// instead of hanging the peer forever.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		cancel()
	}()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	conn, err := ln.AcceptUnix()
	if err != nil {
		if ctx.Err() != nil {
			return 0
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	cfg := ServeConfig{
		Plugin: &fakeFullPlugin{},
		Info:   PluginInfo{Name: "fake-mem", Version: "0.0.1"},
	}
	if out := os.Getenv(testPeerConfigOutEnv); out != "" {
		cfg.ConfigApply = func(configTOML string) error {
			return os.WriteFile(out, []byte(configTOML), 0o600)
		}
	}

	if err := Serve(ctx, conn, cfg); err != nil {
		if ctx.Err() != nil {
			return 0
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func launchSelf(
	t *testing.T, ctx context.Context, mode, configTOML string,
) (*Session, error) {
	t.Helper()

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv(testPeerModeEnv, mode)

	return Launch(ctx, []string{self}, configTOML)
}

// TestLaunchServeSessionRoundTrip is the happy path across a real
// process boundary: bring-up completes, the session carries the fake
// plugin's initialize declaration, a nodes.list call round-trips, and
// Close reaps the child promptly with a clean (nil) exit.
func TestLaunchServeSessionRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sess, err := launchSelf(t, ctx, "serve", "")
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	if sess.Init.Schema != SchemaV1 {
		t.Errorf("Init.Schema = %q, want %q", sess.Init.Schema, SchemaV1)
	}
	if sess.Init.Plugin.Name != "fake-mem" {
		t.Errorf("Init.Plugin.Name = %q, want fake-mem", sess.Init.Plugin.Name)
	}
	if !slices.Equal(sess.Init.Schemes, []string{"mem"}) {
		t.Errorf("Init.Schemes = %v, want [mem]", sess.Init.Schemes)
	}
	wantCaps := []string{
		CapRoots, CapLeafRead, CapFacetCounts,
		CapFacetVersion, CapFacetLabels, CapMutate, CapContainerCreate,
		CapFilteredList, CapBulkMutate,
	}
	gotCaps := slices.Clone(sess.Init.Capabilities)
	slices.Sort(gotCaps)
	slices.Sort(wantCaps)
	if !slices.Equal(gotCaps, wantCaps) {
		t.Errorf("Init.Capabilities = %v, want %v",
			sess.Init.Capabilities, wantCaps)
	}

	var result NodesListResult
	err = sess.Call(ctx, MethodNodesList, NodesListParams{URI: fakeRootURI}, &result)
	if err != nil {
		t.Fatalf("nodes.list: %v", err)
	}
	if len(result.Nodes) != 2 {
		t.Fatalf("nodes = %+v, want 2", result.Nodes)
	}
	if result.Nodes[0].URI != fakeLeafA || result.Nodes[1].URI != fakeLeafB {
		t.Errorf("nodes = %+v, want [%s %s]", result.Nodes, fakeLeafA, fakeLeafB)
	}

	start := time.Now()
	if err := sess.Close(); err != nil {
		t.Errorf("Close = %v, want nil after graceful shutdown", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("teardown took %s; child was not reaped promptly", elapsed)
	}
}

// TestLaunchMissingSubcommandFailsFast pins the bring-up latency
// promise: a child that exits without announcing fails the launch well
// inside the announce deadline. RFC 0013 has no fallback protocol —
// this failure is simply "plugin unavailable", but it must still be
// fast so a misconfigured stanza does not stall every operation.
func TestLaunchMissingSubcommandFailsFast(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	_, err := launchSelf(t, ctx, "exit2", "")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected launch failure, got session")
	}
	if elapsed > 5*time.Second {
		t.Errorf("bring-up failure took %s; must be well under the %s deadline",
			elapsed, announceTimeout)
	}
}

// TestLaunchPollutedStdoutRejectedAndKilled pins the pollution guard on
// the live path: a child that prints noise instead of the announce is
// rejected — and reaped, not leaked, even though it would happily hang.
func TestLaunchPollutedStdoutRejectedAndKilled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	_, err := launchSelf(t, ctx, "pollute", "")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected launch rejection on polluted stdout, got session")
	}
	if !strings.Contains(err.Error(), "announce") {
		t.Errorf("error %q does not mention the announce handshake", err)
	}
	// The child sleeps a minute; returning quickly proves Launch killed
	// and reaped it rather than waiting it out.
	if elapsed > 10*time.Second {
		t.Errorf("polluted launch took %s; child was not killed promptly", elapsed)
	}
}

// TestLaunchConfigTOMLPassthrough pins that Launch's configTOML reaches
// the plugin's ConfigApply verbatim before initialize resolves: the
// serve-mode peer writes what it received to a file, which must exist
// with the exact bytes by the time Launch returns.
func TestLaunchConfigTOMLPassthrough(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	configOut := filepath.Join(t.TempDir(), "config-received.toml")
	t.Setenv(testPeerConfigOutEnv, configOut)

	const configTOML = "[fj]\ntoken_env = \"FJ_TOKEN\"\n"

	sess, err := launchSelf(t, ctx, "serve", configTOML)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer func() { _ = sess.Close() }()

	received, err := os.ReadFile(configOut)
	if err != nil {
		t.Fatalf("read ConfigApply output: %v", err)
	}
	if string(received) != configTOML {
		t.Errorf("ConfigApply received %q, want %q", received, configTOML)
	}
}

// TestLaunchInjectsMadderXDGUserLocationOnly pins the EXDEV fix
// (cutting-garden#131): Launch must set MADDER_XDG_USER_LOCATION_ONLY=1
// in the spawned plugin's environment, so the plugin's madder resolves
// its blob-store cache from its OWN HOME/XDG rather than walking up an
// inherited working directory into the host's `.madder` (which, on a
// bind-mounted host, makes every plugin blob write EXDEV silently). The
// serve-mode peer records its inherited value; it must be "1".
func TestLaunchInjectsMadderXDGUserLocationOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Ensure the test process itself does NOT carry the var, so the
	// value the child sees comes from Launch's append alone (glibc
	// getenv returns the first occurrence; os.Environ() would win over
	// the appended one). The var's correct default IS unset.
	os.Unsetenv("MADDER_XDG_USER_LOCATION_ONLY")

	envOut := filepath.Join(t.TempDir(), "madder-env.txt")
	t.Setenv(testPeerMadderEnvOutEnv, envOut)

	sess, err := launchSelf(t, ctx, "serve", "")
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer func() { _ = sess.Close() }()

	got, err := os.ReadFile(envOut)
	if err != nil {
		t.Fatalf("read madder-env output: %v", err)
	}
	if string(got) != "1" {
		t.Errorf(
			"spawned MADDER_XDG_USER_LOCATION_ONLY = %q, want \"1\"", got,
		)
	}
}

// TestLaunchCloseAfterChildDeath pins Close against an already-dead
// child: it must reap promptly (no shutdownGrace stall, no hang on a
// broken stream) and stay idempotent — a second Close returns the same
// recorded result instead of double-reaping.
func TestLaunchCloseAfterChildDeath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sess, err := launchSelf(t, ctx, "serve", "")
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	if err := sess.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill child: %v", err)
	}

	start := time.Now()
	first := sess.Close()
	if elapsed := time.Since(start); elapsed > shutdownGrace {
		t.Errorf("Close on a dead child took %s; must not wait out the grace",
			elapsed)
	}
	if first == nil {
		t.Error("Close = nil, want the SIGKILLed child's exit error")
	}

	second := sess.Close()
	if second != first {
		t.Errorf("second Close = %v, want the recorded first result %v",
			second, first)
	}
}
