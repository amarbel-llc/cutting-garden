package traversal_serve_testpeer

// The RFC 0013 §Conformance end-to-end: the fixed cgtest tree served by
// the REAL spawned test peer (this test binary re-exec'd into Main via
// mainModeEnv) through traversal_serve.NewWirePlugin must be deeply
// equal — nodes, types, facet declarations, summaries, tokens, labels,
// leaf content — to the same plugin linked in-process. Wire-side
// mutations then round-trip create → patch → put → delete against the
// subprocess's own memory (the linked instance stays untouched — the
// two paths deliberately do NOT share state).
//
// This test lives in the testpeer package (not traversal_serve_test)
// because the traversal_serve test binary already owns a TestMain
// (launch_test.go) and a package gets exactly one; here the package
// imports traversal_serve without a cycle and TestMain is free.
//
// A comparison through internal/list or internal/mcp is deliberately
// NOT made here: mcp's Resources and its registry-free resolve seam are
// unexported, and list resolves through the global scheme registry
// (MustRegisterScheme panics on duplicates across tests) — that layer
// is Task 8's bats lane (`list`/`mcp` against the packaged binary).

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/traversal_serve"
)

// mainModeEnv re-execs THIS test binary as the packaged test peer: when
// set, TestMain runs Main() instead of the test suite — the same trick
// capture_serve's end-to-end uses, so no nix-built binary is needed.
const mainModeEnv = "CG_TRAVERSAL_TESTPEER_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(mainModeEnv) == "1" {
		os.Exit(Main())
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

// envWithout copies the process environment minus the named variables.
func envWithout(names ...string) []string {
	var out []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if slices.Contains(names, name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// jsonNormalize round-trips any JSON-marshalable value (including
// json.RawMessage) through encoding/json's generic shape, so the linked
// plugin's Go maps and the wire's raw bytes compare structurally.
func jsonNormalize(t *testing.T, value any) any {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal for normalization: %v", err)
	}

	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal for normalization: %v", err)
	}
	return out
}

// requireNodesEqual asserts two child listings are indistinguishable:
// same length, same order, same URI/Name/Type/Facets per node. nil and
// empty are the same empty listing (the wire always materializes a
// slice; a linked plugin may return nil).
func requireNodesEqual(
	t *testing.T, uri string,
	linked, wire []cutting_garden_plugins.Node,
) {
	t.Helper()

	if len(linked) != len(wire) {
		t.Fatalf("%s: linked %d children, wire %d children:\n%+v\n%+v",
			uri, len(linked), len(wire), linked, wire)
	}

	for i := range linked {
		if linked[i].URIString() != wire[i].URIString() {
			t.Errorf("%s child[%d]: uri %q vs %q",
				uri, i, linked[i].URIString(), wire[i].URIString())
		}
		if linked[i].Name != wire[i].Name {
			t.Errorf("%s child[%d]: name %q vs %q",
				uri, i, linked[i].Name, wire[i].Name)
		}
		if linked[i].Type != wire[i].Type {
			t.Errorf("%s child[%d]: type %q vs %q",
				uri, i, linked[i].Type, wire[i].Type)
		}
		if !reflect.DeepEqual(linked[i].Facets, wire[i].Facets) {
			t.Errorf("%s child[%d]: facets\nlinked: %+v\nwire:   %+v",
				uri, i, linked[i].Facets, wire[i].Facets)
		}
	}
}

// TestWireIndistinguishableFromLinked is the conformance bar: every
// declaration and every read over every URI of the fixed tree, compared
// between the linked Plugin and the WirePlugin driving the spawned
// peer; then the wire-side mutation flow.
func TestWireIndistinguishableFromLinked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	t.Setenv(mainModeEnv, "1")

	linked := Plugin
	wire := traversal_serve.NewWirePlugin(traversal_serve.PluginSpec{
		Name:    "cgtest-e2e",
		Command: []string{selfExecutable(t)},
		Schemes: []string{Scheme},
	})
	closed := false
	defer func() {
		if !closed {
			_ = wire.Close()
		}
	}()

	// --- declarations -------------------------------------------------

	if got, want := wire.TypeTag(), linked.TypeTag(); got != want {
		t.Errorf("TypeTag: wire %q, linked %q", got, want)
	}

	if got, want := wire.Types(), linked.Types(); !reflect.DeepEqual(got, want) {
		t.Errorf("Types:\nwire:   %+v\nlinked: %+v", got, want)
	}

	if got, want := wire.DescribeFacets(), linked.DescribeFacets(); !reflect.DeepEqual(got, want) {
		t.Errorf("DescribeFacets:\nwire:   %+v\nlinked: %+v", got, want)
	}

	// Bodies compare JSON-normalized: the wire decodes Example into the
	// generic JSON shape, which is the declared contract of the field.
	if got, want := jsonNormalize(t, wire.DescribeBodies()),
		jsonNormalize(t, linked.DescribeBodies()); !reflect.DeepEqual(got, want) {
		t.Errorf("DescribeBodies:\nwire:   %+v\nlinked: %+v", got, want)
	}

	// --- roots + full-tree walk ---------------------------------------

	linkedRoots, err := linked.Roots(ctx)
	if err != nil {
		t.Fatalf("linked Roots: %v", err)
	}
	wireRoots, err := wire.Roots(ctx)
	if err != nil {
		t.Fatalf("wire Roots: %v", err)
	}
	if len(linkedRoots) != len(wireRoots) {
		t.Fatalf("roots: linked %v, wire %v", linkedRoots, wireRoots)
	}

	queue := make([]string, 0, len(linkedRoots))
	for i := range linkedRoots {
		if linkedRoots[i].String() != wireRoots[i].String() {
			t.Errorf("root[%d]: linked %s, wire %s",
				i, linkedRoots[i], wireRoots[i])
		}
		queue = append(queue, linkedRoots[i].String())
	}

	visited := 0
	for len(queue) > 0 {
		uriStr := queue[0]
		queue = queue[1:]
		visited++
		uri := mustParseURL(t, uriStr)

		linkedNodes, err := linked.ListRoots(ctx, uri)
		if err != nil {
			t.Fatalf("linked ListRoots(%s): %v", uriStr, err)
		}
		wireNodes, err := wire.ListRoots(ctx, uri)
		if err != nil {
			t.Fatalf("wire ListRoots(%s): %v", uriStr, err)
		}
		requireNodesEqual(t, uriStr, linkedNodes, wireNodes)

		linkedContent, linkedOK, err := linked.ReadLeaf(ctx, uri)
		if err != nil {
			t.Fatalf("linked ReadLeaf(%s): %v", uriStr, err)
		}
		wireContent, wireOK, err := wire.ReadLeaf(ctx, uri)
		if err != nil {
			t.Fatalf("wire ReadLeaf(%s): %v", uriStr, err)
		}
		if linkedOK != wireOK {
			t.Errorf("ReadLeaf(%s): ok linked %t, wire %t",
				uriStr, linkedOK, wireOK)
		}
		if linkedOK && wireOK {
			if !reflect.DeepEqual(
				jsonNormalize(t, linkedContent.Structured),
				jsonNormalize(t, wireContent.Structured),
			) {
				t.Errorf("ReadLeaf(%s): structured differs:\n%s\nvs\n%s",
					uriStr,
					mustJSON(t, linkedContent.Structured),
					mustJSON(t, wireContent.Structured))
			}
			if !slices.Equal(linkedContent.Raw, wireContent.Raw) {
				t.Errorf("ReadLeaf(%s): raw %q vs %q",
					uriStr, linkedContent.Raw, wireContent.Raw)
			}
			if linkedContent.RawMimeType != wireContent.RawMimeType {
				t.Errorf("ReadLeaf(%s): mime %q vs %q",
					uriStr, linkedContent.RawMimeType, wireContent.RawMimeType)
			}
		}

		for _, filter := range []cutting_garden_plugins.FacetFilter{
			nil,
			{{Dimension: "state", Value: "open"}},
		} {
			// EnrichedLister filter pushdown (cutting-garden#193): a filtered
			// listing must be identical linked vs wire — WirePlugin.ListEnriched
			// pushes the filter over nodes.list, mirroring the linked plugin's
			// own ListEnriched.
			linkedEnriched, linkedEnrichedOK, err := linked.ListEnriched(
				ctx, uri, filter,
			)
			if err != nil {
				t.Fatalf("linked ListEnriched(%s, %v): %v", uriStr, filter, err)
			}
			wireEnriched, wireEnrichedOK, err := wire.ListEnriched(
				ctx, uri, filter,
			)
			if err != nil {
				t.Fatalf("wire ListEnriched(%s, %v): %v", uriStr, filter, err)
			}
			if linkedEnrichedOK != wireEnrichedOK {
				t.Errorf("ListEnriched(%s, %v): ok linked %t, wire %t",
					uriStr, filter, linkedEnrichedOK, wireEnrichedOK)
			}
			if linkedEnrichedOK && wireEnrichedOK {
				requireNodesEqual(
					t, uriStr+" enriched", linkedEnriched, wireEnriched,
				)
			}

			linkedCounts, linkedOK, err := linked.FacetCounts(ctx, uri, filter)
			if err != nil {
				t.Fatalf("linked FacetCounts(%s, %v): %v", uriStr, filter, err)
			}
			wireCounts, wireOK, err := wire.FacetCounts(ctx, uri, filter)
			if err != nil {
				t.Fatalf("wire FacetCounts(%s, %v): %v", uriStr, filter, err)
			}
			if linkedOK != wireOK {
				t.Errorf("FacetCounts(%s, %v): ok linked %t, wire %t",
					uriStr, filter, linkedOK, wireOK)
			}
			if linkedOK && wireOK {
				if !reflect.DeepEqual(linkedCounts.Summary, wireCounts.Summary) {
					t.Errorf("FacetCounts(%s, %v): summary\nlinked: %+v\nwire:   %+v",
						uriStr, filter, linkedCounts.Summary, wireCounts.Summary)
				}
				if linkedCounts.Complete != wireCounts.Complete {
					t.Errorf("FacetCounts(%s, %v): complete linked %t, wire %t",
						uriStr, filter, linkedCounts.Complete, wireCounts.Complete)
				}
				// The §13 per-container breakdown must survive the wire
				// intact (cutting-garden#173) — including its ABSENCE: a
				// node with no breakdown must be nil on both sides, not
				// nil linked and empty wire.
				if !reflect.DeepEqual(
					linkedCounts.ByContainer, wireCounts.ByContainer,
				) {
					t.Errorf("FacetCounts(%s, %v): byContainer\nlinked: %+v\nwire:   %+v",
						uriStr, filter,
						linkedCounts.ByContainer, wireCounts.ByContainer)
				}
				if linkedCounts.ByContainerTruncated !=
					wireCounts.ByContainerTruncated {
					t.Errorf("FacetCounts(%s, %v): truncated linked %t, wire %t",
						uriStr, filter,
						linkedCounts.ByContainerTruncated,
						wireCounts.ByContainerTruncated)
				}
			}
		}

		linkedToken, linkedOK, err := linked.FacetVersion(ctx, uri)
		if err != nil {
			t.Fatalf("linked FacetVersion(%s): %v", uriStr, err)
		}
		wireToken, wireOK, err := wire.FacetVersion(ctx, uri)
		if err != nil {
			t.Fatalf("wire FacetVersion(%s): %v", uriStr, err)
		}
		if linkedOK != wireOK || linkedToken != wireToken {
			t.Errorf("FacetVersion(%s): linked %q %t, wire %q %t",
				uriStr, linkedToken, linkedOK, wireToken, wireOK)
		}

		// Every child is enqueued — containers descend, leaves get the
		// leaf-side comparisons (empty listing, ReadLeaf ok, decline
		// paths) on their own visit.
		for _, node := range linkedNodes {
			queue = append(queue, node.URIString())
		}
	}

	// 3 containers (box, nested, issue-1) + 4 leaves (alpha, beta, gamma,
	// issue-1's comment).
	if visited != 7 {
		t.Errorf("walk visited %d URIs, want 7", visited)
	}

	linkedLabels, err := linked.ResolveFacetLabels(
		ctx, "feed", []string{"f1", "f2", "zz"},
	)
	if err != nil {
		t.Fatalf("linked ResolveFacetLabels: %v", err)
	}
	wireLabels, err := wire.ResolveFacetLabels(
		ctx, "feed", []string{"f1", "f2", "zz"},
	)
	if err != nil {
		t.Fatalf("wire ResolveFacetLabels: %v", err)
	}
	if !reflect.DeepEqual(linkedLabels, wireLabels) {
		t.Errorf("ResolveFacetLabels: linked %+v, wire %+v",
			linkedLabels, wireLabels)
	}

	// --- wire-side mutation flow --------------------------------------
	// The subprocess has ITS OWN tree instance, so mutation assertions
	// are wire-side only; the linked instance must stay untouched.

	box := mustParseURL(t, RootBox)
	delta := mustParseURL(t, RootBox+"/delta")

	preToken, _, err := wire.FacetVersion(ctx, box)
	if err != nil {
		t.Fatalf("wire pre-mutation FacetVersion: %v", err)
	}

	if err := wire.CreateNode(
		ctx, delta, strings.NewReader("delta body"), LeafType,
	); err != nil {
		t.Fatalf("wire CreateNode: %v", err)
	}

	children, err := wire.ListRoots(ctx, box)
	if err != nil {
		t.Fatalf("wire ListRoots after create: %v", err)
	}
	if len(children) != 5 ||
		children[4].URIString() != delta.String() ||
		children[4].Type != LeafType {
		t.Fatalf("wire children after create = %+v, want delta appended",
			children)
	}

	postToken, _, err := wire.FacetVersion(ctx, box)
	if err != nil {
		t.Fatalf("wire post-mutation FacetVersion: %v", err)
	}
	if postToken == preToken {
		t.Errorf("facet token unchanged across a mutation: %q", postToken)
	}

	// A caller-fault error from the PLUGIN must reach the host as
	// -32602, not -32603 (cutting-garden#185). This asserts it over a
	// REAL socket against the spawned peer, so it pins the byte on the
	// wire rather than the in-process classification: the peer's
	// PatchNode rejects a non-JSON body as a bad request, and that
	// verdict has to survive serialization. Before the fix every plugin
	// error arrived as -32603 "the plugin failed", which invites a retry
	// that then fails identically forever.
	malformed, err := wire.PatchNode(ctx, delta, strings.NewReader("not json"))
	if err == nil {
		t.Fatalf("malformed patch body must error; got applied = %#v", malformed)
	}
	if code, ok := traversal_serve.CodeOf(err); !ok ||
		code != traversal_serve.CodeInvalidParams {
		t.Errorf("malformed patch body: CodeOf = %d, %t, want %d, true"+
			" — a caller mistake must not be reported as a plugin failure",
			code, ok, traversal_serve.CodeInvalidParams)
	}

	applied, err := wire.PatchNode(
		ctx, delta, strings.NewReader(`{"note":"patched"}`),
	)
	if err != nil {
		t.Fatalf("wire PatchNode: %v", err)
	}
	// Indistinguishability extends to the applied report: the linked peer
	// answers exactly this in testpeer_test (cutting-garden#182), so a
	// consumer still cannot tell which side of the wire it is talking to.
	if !slices.Equal(applied, []string{"note"}) {
		t.Errorf("wire applied = %#v, want [note]", applied)
	}

	content, ok, err := wire.ReadLeaf(ctx, delta)
	if err != nil || !ok {
		t.Fatalf("wire ReadLeaf(delta) = ok %t, err %v", ok, err)
	}
	structured, isMap := jsonNormalize(t, content.Structured).(map[string]any)
	if !isMap || structured["note"] != "patched" ||
		structured["title"] != "delta" {
		t.Errorf("patched structured = %+v", structured)
	}
	if string(content.Raw) != "delta body" {
		t.Errorf("raw after patch = %q, want untouched", content.Raw)
	}

	if err := wire.PutNode(
		ctx, delta, strings.NewReader("replaced"),
	); err != nil {
		t.Fatalf("wire PutNode: %v", err)
	}
	content, ok, err = wire.ReadLeaf(ctx, delta)
	if err != nil || !ok {
		t.Fatalf("wire ReadLeaf after put = ok %t, err %v", ok, err)
	}
	if string(content.Raw) != "replaced" {
		t.Errorf("raw after put = %q, want replaced", content.Raw)
	}

	if err := wire.DeleteNode(ctx, delta); err != nil {
		t.Fatalf("wire DeleteNode: %v", err)
	}
	children, err = wire.ListRoots(ctx, box)
	if err != nil {
		t.Fatalf("wire ListRoots after delete: %v", err)
	}
	if len(children) != 4 {
		t.Errorf("wire children after delete = %+v, want the original 4",
			children)
	}
	if _, ok, err := wire.ReadLeaf(ctx, delta); ok || err != nil {
		t.Errorf("wire ReadLeaf(deleted) = ok %t, err %v; want false, nil",
			ok, err)
	}

	// Separate memory: the linked instance never saw the mutations.
	linkedChildren, err := linked.ListRoots(ctx, box)
	if err != nil {
		t.Fatalf("linked ListRoots after wire mutations: %v", err)
	}
	if len(linkedChildren) != 4 {
		t.Errorf("linked children = %+v; wire mutations leaked into the"+
			" linked instance", linkedChildren)
	}

	// Graceful teardown: shutdown notification + stdin EOF must yield a
	// clean (exit 0) child, surfacing as a nil Close.
	closed = true
	if err := wire.Close(); err != nil {
		t.Errorf("wire Close = %v, want nil after graceful shutdown", err)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}

// TestMainWithoutCookieExitsNonzeroStdoutSilent pins the RFC 0013
// §Launch cookie guard on the packaged peer: invoked without the cookie
// it must exit 1 with a stderr diagnostic and NOTHING on stdout.
func TestMainWithoutCookieExitsNonzeroStdoutSilent(t *testing.T) {
	cmd := exec.Command(selfExecutable(t))
	cmd.Env = append(
		envWithout(mainModeEnv, traversal_serve.CookieEnv),
		mainModeEnv+"=1",
	)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitErr, isExit := err.(*exec.ExitError)
	if !isExit || exitErr.ExitCode() != 1 {
		t.Fatalf("no-cookie run: err = %v, want exit code 1", err)
	}

	if stdout.String() != "" {
		t.Errorf("no-cookie stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), traversal_serve.CookieEnv) {
		t.Errorf("no-cookie stderr %q does not name %s",
			stderr.String(), traversal_serve.CookieEnv)
	}
}

// TestMainStdinEOFNeverDialedExitsZero pins the stdin-EOF lifecycle at
// the process level: spawn the peer, read its announce, NEVER dial, and
// close stdin — the armed-before-accept watcher must unblock the accept
// and the peer must exit 0 promptly. (launch_test.go cannot cover this:
// Launch always dials.)
func TestMainStdinEOFNeverDialedExitsZero(t *testing.T) {
	cmd := exec.Command(selfExecutable(t))
	cmd.Env = append(
		envWithout(mainModeEnv, traversal_serve.CookieEnv),
		mainModeEnv+"=1",
		traversal_serve.CookieEnv+"=e2e-never-dialed-cookie",
	)
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = cmd.Process.Kill() }()

	reader := bufio.NewReader(stdout)
	announce, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read announce: %v", err)
	}
	if !strings.HasPrefix(announce, "e2e-never-dialed-cookie|") {
		t.Fatalf("announce %q does not echo the cookie", announce)
	}

	if err := stdin.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}

	// Drain the remaining stdout BEFORE Wait (Wait closes the pipe):
	// the read blocks until the child exits, proving the exit is prompt
	// and that stdout carried the announce and nothing else.
	type outcome struct {
		rest    []byte
		readErr error
		waitErr error
	}
	done := make(chan outcome, 1)
	go func() {
		rest, readErr := io.ReadAll(reader)
		done <- outcome{rest: rest, readErr: readErr, waitErr: cmd.Wait()}
	}()

	start := time.Now()
	select {
	case got := <-done:
		if got.waitErr != nil {
			t.Errorf("never-dialed peer exit = %v, want 0", got.waitErr)
		}
		if got.readErr != nil {
			t.Errorf("drain stdout: %v", got.readErr)
		}
		if len(got.rest) != 0 {
			t.Errorf("stdout after announce = %q, want nothing", got.rest)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("never-dialed peer did not exit on stdin EOF")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("exit took %s; the stdin-EOF watcher is not prompt", elapsed)
	}
}
