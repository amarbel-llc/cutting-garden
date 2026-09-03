package list

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/command"
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/traversal_serve_testpeer"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// testpeerMainEnv re-execs this test binary as the RFC 0013 test peer:
// the wire plugin's Launch spawns os.Args[0], the child inherits the
// environment, and TestMain diverts to the peer before any test runs.
const testpeerMainEnv = "CG_LIST_TESTPEER_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(testpeerMainEnv) == "1" {
		os.Exit(traversal_serve_testpeer.Main())
	}
	os.Exit(m.Run())
}

// TestRun_WirePluginDirectURI is the regression fj-cg's live conformance
// run found (#140): `list <uri>` in a fresh process must load config and
// register [[traversal_plugins]] schemes BEFORE resolving. Without the
// Run-level config load this failed with `unknown scheme` while the
// no-arg root listing worked.
func TestRun_WirePluginDirectURI(t *testing.T) {
	t.Setenv(testpeerMainEnv, "1")

	xdg := t.TempDir()
	configDir := filepath.Join(xdg, "cutting-garden")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	config := "[[traversal_plugins]]\n" +
		"name = \"cgtest\"\n" +
		"command = [\"" + os.Args[0] + "\"]\n" +
		"schemes = [\"cgtest\"]\n"
	if err := os.WriteFile(
		filepath.Join(configDir, "config.toml"), []byte(config), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdg)

	var buf bytes.Buffer
	u := command.MakeUtility("cg-test", nil)
	u.AddCmd("list", newWithOutput(&buf))
	code := u.Run([]string{
		"cg-test", "list", traversal_serve_testpeer.RootBox,
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, buf.String())
	}

	out := buf.String()
	for _, child := range []string{"alpha", "beta", "nested"} {
		if !strings.Contains(out, child) {
			t.Errorf("listing lacks child %q:\n%s", child, out)
		}
	}
}

// listFake is a capture plugin that also implements RootLister, so the
// list command can be exercised without a live CalDAV server. It claims
// the "listtest" scheme and returns two canned container nodes.
type listFake struct{}

func (listFake) Schemes() []string                      { return []string{"listtest"} }
func (listFake) TypeTag() string                        { return "cutting_garden-test-v1" }
func (listFake) ValidateSource(*url.URL, string) error  { return nil }
func (listFake) ValidateDiffDir(*url.URL, string) error { return nil }
func (listFake) CaptureRoot(cutting_garden_plugins.CaptureRootRequest) cutting_garden_plugins.CaptureRootResult {
	return cutting_garden_plugins.CaptureRootResult{}
}

func (listFake) Types() []cutting_garden_plugins.NodeType {
	return []cutting_garden_plugins.NodeType{
		{Tag: "test-container-v1", Container: true},
		{Tag: "test-object-v1", Container: false},
	}
}

func (listFake) ListRoots(
	_ context.Context,
	node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	if node == nil {
		return nil, errors.ErrorWithStackf("listtest: nil node")
	}
	mk := func(path, name string) cutting_garden_plugins.Node {
		return cutting_garden_plugins.Node{
			URI:  &url.URL{Scheme: "listtest", Host: node.Host, Path: path},
			Name: name,
			Type: "test-container-v1",
		}
	}
	return []cutting_garden_plugins.Node{
		mk("/work", "Work"),
		mk("/personal", "Personal"),
	}, nil
}

// facetFake extends listFake with the facet capabilities (FacetCounter +
// FacetDescriber), declaring one FacetDate dimension, to exercise the
// runFacets filter-Validate gate (cutting-garden#161 applied to the CLI
// --facets path, #230). It claims its own scheme so the plain listFake
// keeps exercising the no-facets paths.
type facetFake struct{ listFake }

func (facetFake) Schemes() []string { return []string{"facettest"} }

func (facetFake) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	return []cutting_garden_plugins.NodeTypeFacets{{
		Tag: "test-object-v1",
		Dimensions: []cutting_garden_plugins.FacetDimension{
			{Key: "date_x", Kind: cutting_garden_plugins.FacetDate},
		},
	}}
}

func (facetFake) FacetCounts(
	_ context.Context, _ *url.URL, _ cutting_garden_plugins.FacetFilter,
) (cutting_garden_plugins.FacetResult, bool, error) {
	return cutting_garden_plugins.FacetResult{
		Summary:  cutting_garden_plugins.FacetSummary{"date_x": {"2026-08-15": 1}},
		Complete: true,
	}, true, nil
}

// captureOnlyFake claims a scheme but is NOT a RootLister, to exercise
// the "does not support listing" path.
type captureOnlyFake struct{}

func (captureOnlyFake) Schemes() []string                     { return []string{"noroots"} }
func (captureOnlyFake) TypeTag() string                       { return "cutting_garden-test-v1" }
func (captureOnlyFake) ValidateSource(*url.URL, string) error { return nil }
func (captureOnlyFake) CaptureRoot(cutting_garden_plugins.CaptureRootRequest) cutting_garden_plugins.CaptureRootResult {
	return cutting_garden_plugins.CaptureRootResult{}
}

// taggedListFake extends listFake with a UnifiedDescriber declaring a
// categories FieldTag dimension over its own scheme, and a ListRoots that
// populates the stored tag list — the `list -format json` tags fixture
// (design G12, native tags slice 2). No EnrichedLister: the fetch falls back
// to ListRoots, whose Fields already carry what the codec presents.
type taggedListFake struct{ listFake }

func (taggedListFake) Schemes() []string { return []string{"taggedlist"} }

func (taggedListFake) DescribeUnified() []cutting_garden_plugins.NodeTypeUnifiedFields {
	return []cutting_garden_plugins.NodeTypeUnifiedFields{{
		Tag:    "test-object-v1",
		Codecs: []cutting_garden_plugins.Codec{tagListCodec{}},
	}}
}

func (taggedListFake) ListRoots(
	_ context.Context, node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	return []cutting_garden_plugins.Node{
		{
			URI:    &url.URL{Scheme: "taggedlist", Host: node.Host, Path: "/tagged"},
			Name:   "tagged",
			Type:   "test-object-v1",
			Fields: map[string]any{"categories": []string{"work", "errand"}},
		},
		{
			URI:  &url.URL{Scheme: "taggedlist", Host: node.Host, Path: "/plain"},
			Name: "plain",
			Type: "test-object-v1",
		},
	}, nil
}

// tagListCodec presents a stored []string categories field verbatim as the
// designated tag set.
type tagListCodec struct{}

func (tagListCodec) Fields() []cutting_garden_plugins.UnifiedField {
	return []cutting_garden_plugins.UnifiedField{{
		Key: "categories", Kind: cutting_garden_plugins.FieldTag,
		Groupable: true, MultiValued: true, Interpreter: "naive",
	}}
}

func (tagListCodec) Format(stored map[string]any) (map[string][]string, error) {
	if ts, ok := stored["categories"].([]string); ok && len(ts) > 0 {
		return map[string][]string{"categories": ts}, nil
	}
	return map[string][]string{}, nil
}

func (tagListCodec) Parse(map[string][]string, map[string]any) (map[string]any, error) {
	return nil, nil
}

func init() {
	cutting_garden_plugins.MustRegisterCapture(listFake{})
	cutting_garden_plugins.MustRegisterCapture(facetFake{})
	cutting_garden_plugins.MustRegisterCapture(captureOnlyFake{})
	cutting_garden_plugins.MustRegisterCapture(taggedListFake{})
}

// TestRunFacets_FilterValidateGate pins the runFacets Validate addition
// (cutting-garden#161 applied to the CLI --facets path, #230): a malformed
// date-bucket filter value is rejected loudly BEFORE FacetCounts runs, and a
// well-shaped one passes through to the summary.
func TestRunFacets_FilterValidateGate(t *testing.T) {
	ctx := errors.MakeContextDefault()

	bad := newWithOutput(io.Discard)
	bad.Filter = "date_x=bogus"
	err := bad.runFacets(ctx, "facettest://h/dav/")
	if err == nil || !strings.Contains(err.Error(), "not a date bucket") {
		t.Fatalf("malformed date filter: want a date-bucket rejection, got %v", err)
	}

	good := newWithOutput(io.Discard)
	good.Filter = "date_x=2026"
	if err := good.runFacets(ctx, "facettest://h/dav/"); err != nil {
		t.Fatalf("valid date filter: want nil, got %v", err)
	}
}

// driveList dispatches the list subcommand through a fresh Utility (flag
// parsing included) with output routed to out, returning the exit code.
// Config is isolated to an empty XDG home: Run loads config on every
// path (the #140 wire-plugin fix), and tests must never read the
// developer's real one. A test that needs a config fixture builds its
// own utility (see TestRun_WirePluginDirectURI).
func driveList(t *testing.T, out io.Writer, args ...string) int {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	u := command.MakeUtility("cg-test", nil)
	u.AddCmd("list", newWithOutput(out))
	return u.Run(append([]string{"cg-test", "list"}, args...))
}

func TestRun_TextTable(t *testing.T) {
	var buf bytes.Buffer
	if code := driveList(t, &buf, "listtest://h/dav/"); code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, buf.String())
	}
	out := buf.String()
	for _, want := range []string{"URI", "NAME", "TYPE", "Work", "Personal", "test-container-v1", "listtest://h/work"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
}

func TestRun_JSONRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if code := driveList(t, &buf, "-format", "json", "listtest://h/dav/"); code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, buf.String())
	}

	var got []nodeView
	dec := json.NewDecoder(&buf)
	for dec.More() {
		var n nodeView
		if err := dec.Decode(&n); err != nil {
			t.Fatalf("decode NDJSON: %v", err)
		}
		got = append(got, n)
	}
	if len(got) != 2 {
		t.Fatalf("got %d nodes, want 2: %+v", len(got), got)
	}
	for _, n := range got {
		if n.Type != "test-container-v1" || n.URI == "" || n.Name == "" {
			t.Errorf("node = %+v", n)
		}
	}
}

// TestRun_JSONCarriesTags pins the G12 CLI half (native tags slice 2): the
// JSON node view of a tag-declaring plugin carries a top-level `tags` array —
// the designated FieldTag field's values in the resolved interpreter's
// SortKey order — while an untagged node omits the key, and the text table
// stays tag-free (espalier/mesa are slice 4).
func TestRun_JSONCarriesTags(t *testing.T) {
	var buf bytes.Buffer
	if code := driveList(t, &buf, "-format", "json", "taggedlist://h/"); code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, buf.String())
	}

	byName := map[string]nodeView{}
	dec := json.NewDecoder(&buf)
	for dec.More() {
		var n nodeView
		if err := dec.Decode(&n); err != nil {
			t.Fatalf("decode NDJSON: %v", err)
		}
		byName[n.Name] = n
	}
	if got, want := byName["tagged"].Tags, []string{"errand", "work"}; len(got) != 2 ||
		got[0] != want[0] || got[1] != want[1] {
		t.Errorf("tagged node tags = %v, want SortKey order %v", got, want)
	}
	if got := byName["plain"].Tags; len(got) != 0 {
		t.Errorf("untagged node tags = %v, want omitted", got)
	}

	// The text table is untouched by the tag enrichment.
	buf.Reset()
	if code := driveList(t, &buf, "taggedlist://h/"); code != 0 {
		t.Fatalf("text exit = %d, want 0; output:\n%s", code, buf.String())
	}
	if strings.Contains(buf.String(), "errand") {
		t.Errorf("text table leaked tags:\n%s", buf.String())
	}
}

func TestRun_UnknownSchemeIsTrouble(t *testing.T) {
	var buf bytes.Buffer
	if code := driveList(t, &buf, "bogus://x"); code != 2 {
		t.Fatalf("exit = %d, want 2 (unknown scheme)", code)
	}
}

func TestRun_SchemeNotListableIsTrouble(t *testing.T) {
	var buf bytes.Buffer
	if code := driveList(t, &buf, "noroots://x"); code != 2 {
		t.Fatalf("exit = %d, want 2 (scheme has no RootLister)", code)
	}
	if buf.Len() != 0 {
		t.Errorf("non-listable run wrote output: %q", buf.String())
	}
}

// TestRun_NoArgListsConfiguredRoots pins the RFC 0007 contract change: a
// no-argument `list` no longer errors — it lists the aggregated roots. With
// an isolated (absent) config and no RootProvider fakes registered here,
// the listing is empty, but the invocation succeeds (exit 0).
func TestRun_NoArgListsConfiguredRoots(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no config file → empty config
	var buf bytes.Buffer
	if code := driveList(t, &buf); code != 0 {
		t.Fatalf("exit = %d, want 0 (no-arg lists roots); output:\n%s",
			code, buf.String())
	}
}

func TestRun_TrailingArgIsUsageError(t *testing.T) {
	var buf bytes.Buffer
	if code := driveList(t, &buf, "listtest://h/", "extra"); code != 64 {
		t.Fatalf("exit = %d, want 64 (EX_USAGE)", code)
	}
}

func TestRun_BadFormatIsUsageError(t *testing.T) {
	var buf bytes.Buffer
	if code := driveList(t, &buf, "-format", "yaml", "listtest://h/"); code != 64 {
		t.Fatalf("exit = %d, want 64 (EX_USAGE)", code)
	}
}
