package traversal_conformance_test

// The driver's own verification (the plan's §Verification): Run against
// the REAL spawned cgtest testpeer — this test binary re-exec'd into
// testpeer.Main via mainModeEnv, the same trick
// traversal_serve_testpeer's end-to-end uses, so no nix-built binary is
// needed — must pass every case; and the known-wrong self-test proves
// the driver can FAIL a non-conformant expectation (a driver that
// cannot fail ratifies nothing).
//
// The test lives in the _test external package so the import boundary
// stays honest: it consumes traversal_conformance exactly as the
// conformance binary does, plus the testpeer for the peer side.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/internal/traversal_conformance"
	testpeer "code.linenisgreat.com/cutting-garden/internal/traversal_serve_testpeer"
)

// mainModeEnv re-execs THIS test binary as the packaged test peer: when
// set, TestMain runs testpeer.Main() instead of the test suite.
const mainModeEnv = "CG_TRAVERSAL_CONFORMANCE_PEER_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(mainModeEnv) == "1" {
		os.Exit(testpeer.Main())
	}

	os.Exit(m.Run())
}

// selfExecutable resolves the running test binary for re-exec.
func selfExecutable(t *testing.T) string {
	t.Helper()

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	return self
}

// testpeerManifest is the in-tree cgtest parameterization, built
// against the testpeer's plugin-defined body formats
// (internal/traversal_serve_testpeer): CreateNode takes raw bytes;
// PatchNode takes a JSON object whose keys ALL merge into the
// structured view and are ALL reported applied — so ExpectApplied is
// exactly the patch body's keys, an unrecognized-only body is
// unconstructible (empty Body = the manifest's declared SKIP), and a
// non-JSON-object body is a BadRequest → -32602.
func testpeerManifest(t *testing.T) *traversal_conformance.Manifest {
	t.Helper()

	return &traversal_conformance.Manifest{
		Command:           []string{selfExecutable(t)},
		Schemes:           []string{testpeer.Scheme},
		WritableContainer: testpeer.RootBox,
		Create: traversal_conformance.CreateSpec{
			Type: testpeer.LeafType,
			Body: "probe body\n",
		},
		PatchRecognized: traversal_conformance.PatchRecognizedSpec{
			Body:          `{"note":"patched","rank":2}`,
			ExpectApplied: []string{"note", "rank"},
		},
		PatchUnrecognizedOnly: traversal_conformance.PatchSpec{Body: ""},
		PatchWrongTyped: traversal_conformance.PatchSpec{
			Body: "not json",
		},
		FacetContainer: &traversal_conformance.FacetContainerSpec{
			URI:    testpeer.RootBox,
			Filter: "state=open",
		},
		// IssueBox is the testpeer's container-with-body: it holds a comment
		// AND carries a title/state, and declares a uri_template — the
		// RFC 0018 §7 / cutting-garden#168 fixture.
		ContainerBody: &traversal_conformance.ContainerBodySpec{
			URI: testpeer.IssueBox,
		},
		// The testpeer advertises bulk-mutate (RFC 0017 / cutting-garden#196):
		// the case creates a probe leaf under RootBox and isolates a
		// missing-node delete as a failure.
		BulkMutate: &traversal_conformance.BulkMutateSpec{
			Container:  testpeer.RootBox,
			CreateType: testpeer.LeafType,
			CreateBody: "bulk probe body\n",
		},
	}
}

// TestRunPassesConformantTestpeer drives the full slice-1 case list
// against the spawned testpeer: every point must be ok (the
// unrecognized-only point via its declared SKIP), and the whole run
// must report passed.
func TestRunPassesConformantTestpeer(t *testing.T) {
	ctx, cancel := context.WithTimeout(
		context.Background(), 120*time.Second,
	)
	defer cancel()

	t.Setenv(mainModeEnv, "1")

	var out bytes.Buffer
	passed, err := traversal_conformance.Run(ctx, testpeerManifest(t), &out)
	if err != nil {
		t.Fatalf("Run: %v\noutput:\n%s", err, out.String())
	}

	if !passed {
		t.Fatalf("passed = false against the conformant testpeer:\n%s",
			out.String())
	}

	for _, want := range []string{
		"TAP version 14",
		"ok 1 - initialize: schema echo, schemes, capabilities",
		"ok 2 - error code: unknown method is -32601",
		"ok 3 - error code: malformed nodes.list uri is -32602",
		"ok 4 - node.create: probe node",
		"ok 5 - node.patch: recognized fields reported applied",
		"ok 6 - node.patch: unrecognized-only body reports applied" +
			" present-empty # SKIP peer tolerates every patch key",
		"ok 7 - node.patch: wrong-typed body is -32602",
		"ok 8 - node.delete: probe cleanup",
		"ok 9 - facets.counts: by_container raw invariants",
		"ok 10 - facets.counts: descend targets reachable",
		"ok 11 - leaf.read: container returns its own body beside children",
		"ok 12 - uri template: container resolves to its body-declaring type",
		"ok 13 - nodes.list: filter pushdown returns a sound subset",
		"ok 14 - node.bulk_mutate: best-effort applies and isolates a" +
			" failure, atomic and malformed refused",
		"1..14",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}

	if strings.Contains(out.String(), "not ok") {
		t.Errorf("output contains a not-ok point:\n%s", out.String())
	}
}

// TestRunFailsMismatchedExpectApplied is the "driver must be able to
// fail" acceptance requirement (the plan's known-wrong self-test): a
// manifest whose applied expectation deliberately mismatches the peer's
// report must fail the recognized-patch point and the whole run — a
// driver that passes everything ratifies nothing.
func TestRunFailsMismatchedExpectApplied(t *testing.T) {
	ctx, cancel := context.WithTimeout(
		context.Background(), 120*time.Second,
	)
	defer cancel()

	t.Setenv(mainModeEnv, "1")

	manifest := testpeerManifest(t)
	manifest.PatchRecognized.ExpectApplied = []string{"nope"}

	var out bytes.Buffer
	passed, err := traversal_conformance.Run(ctx, manifest, &out)
	if err != nil {
		t.Fatalf("Run: %v\noutput:\n%s", err, out.String())
	}

	if passed {
		t.Fatalf("passed = true with a deliberately wrong expectation:\n%s",
			out.String())
	}

	want := "not ok 5 - node.patch: recognized fields reported applied"
	if !strings.Contains(out.String(), want) {
		t.Errorf("output missing %q:\n%s", want, out.String())
	}

	// The probe cleanup must have run despite the failure (the
	// delete-even-on-failure obligation).
	if !strings.Contains(
		out.String(), "ok 8 - node.delete: probe cleanup",
	) {
		t.Errorf("probe cleanup did not run after a failed point:\n%s",
			out.String())
	}
}

// TestRunBailsOutWhenPeerCannotLaunch pins the driver-trouble path: a
// Command that is not a launchable peer (here a bare path that produces
// no announce) fails LaunchWithoutInitialize, so Run emits a well-formed
// TAP bailout — "Bail out!" plus a plan line, not a duplicate or
// contradictory plan — reports passed == false, AND returns a non-nil
// err (this is driver trouble, not a peer failing a case; the two are
// distinguished by err). The reviewer flagged the bailout TAP shape as
// otherwise untested.
func TestRunBailsOutWhenPeerCannotLaunch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	manifest := &traversal_conformance.Manifest{
		// A path guaranteed not to exist: spawn fails immediately, the
		// cleanest launch failure. (Deliberately NOT the re-exec'd test
		// binary — without mainModeEnv set it would run the whole test
		// suite as a subprocess, a fork bomb, rather than announcing.)
		Command: []string{
			filepath.Join(t.TempDir(), "no-such-peer-binary"),
		},
		Schemes: []string{testpeer.Scheme},
	}

	var out bytes.Buffer
	passed, err := traversal_conformance.Run(ctx, manifest, &out)
	if passed {
		t.Errorf("passed = true on a peer that could not launch:\n%s",
			out.String())
	}
	if err == nil {
		t.Error("err = nil; a launch failure is driver trouble, not a" +
			" peer conformance failure")
	}
	if !strings.Contains(out.String(), "Bail out!") {
		t.Errorf("output missing a TAP bailout:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "1..") {
		t.Errorf("bailout output missing a plan line:\n%s", out.String())
	}
}

// TestLoadManifestDecodesTOML pins the manifest schema's TOML surface:
// every field of the plan's §Peer manifest decodes, including the
// nested tables and the optional facet_container.
func TestLoadManifestDecodesTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer.toml")

	const text = `
command = ["/bin/peer", "traversal-serve"]
config_toml = "[peer]\nkey = \"value\"\n"
schemes = ["cgtest"]
writable_container = "cgtest://fixture/box"

[create]
type = "cgtest-obj-v1"
body = "probe body\n"

[patch_recognized]
body = '{"note":"n"}'
expect_applied = ["note"]

[patch_unrecognized_only]
body = '{"bogus":true}'

[patch_wrong_typed]
body = "not json"

[facet_container]
uri = "cgtest://fixture/box"
filter = "state=open"

[container_body]
uri = "cgtest://fixture/box/issue-1"
`
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	manifest, err := traversal_conformance.LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}

	if got := manifest.Command; len(got) != 2 ||
		got[0] != "/bin/peer" || got[1] != "traversal-serve" {
		t.Errorf("Command = %v", got)
	}
	if got := manifest.ConfigTOML; got != "[peer]\nkey = \"value\"\n" {
		t.Errorf("ConfigTOML = %q", got)
	}
	if got := manifest.Schemes; len(got) != 1 || got[0] != "cgtest" {
		t.Errorf("Schemes = %v", got)
	}
	if got := manifest.WritableContainer; got != "cgtest://fixture/box" {
		t.Errorf("WritableContainer = %q", got)
	}
	if manifest.Create.Type != "cgtest-obj-v1" ||
		manifest.Create.Body != "probe body\n" {
		t.Errorf("Create = %+v", manifest.Create)
	}
	if manifest.PatchRecognized.Body != `{"note":"n"}` {
		t.Errorf("PatchRecognized.Body = %q", manifest.PatchRecognized.Body)
	}
	if got := manifest.PatchRecognized.ExpectApplied; len(got) != 1 ||
		got[0] != "note" {
		t.Errorf("ExpectApplied = %v", got)
	}
	if manifest.PatchUnrecognizedOnly.Body != `{"bogus":true}` {
		t.Errorf("PatchUnrecognizedOnly.Body = %q",
			manifest.PatchUnrecognizedOnly.Body)
	}
	if manifest.PatchWrongTyped.Body != "not json" {
		t.Errorf("PatchWrongTyped.Body = %q", manifest.PatchWrongTyped.Body)
	}
	if manifest.FacetContainer == nil {
		t.Fatal("FacetContainer = nil")
	}
	if manifest.FacetContainer.URI != "cgtest://fixture/box" ||
		manifest.FacetContainer.Filter != "state=open" {
		t.Errorf("FacetContainer = %+v", manifest.FacetContainer)
	}
	if manifest.ContainerBody == nil ||
		manifest.ContainerBody.URI != "cgtest://fixture/box/issue-1" {
		t.Errorf("ContainerBody = %+v", manifest.ContainerBody)
	}
}

// TestLoadManifestRejectsUnknownKey pins the typo guard: a misspelled
// key must fail the load rather than silently narrowing the case list
// (a conformance tool must not hand out false ratification).
func TestLoadManifestRejectsUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "typo.toml")

	const text = `
command = ["/bin/peer"]
schemes = ["cgtest"]
writeable_container = "cgtest://fixture/box"
`
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	_, err := traversal_conformance.LoadManifest(path)
	if err == nil {
		t.Fatal("LoadManifest accepted an unknown key")
	}
	if !strings.Contains(err.Error(), "writeable_container") {
		t.Errorf("error %q does not name the unknown key", err)
	}
}
