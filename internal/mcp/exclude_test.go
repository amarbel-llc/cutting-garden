package mcp

import (
	"context"
	"net/url"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// excludeSchemeFake is a minimal RootProvider fake registered under a
// distinct scheme, used to exercise the -exclude-scheme filter
// (cutting-garden#148) against both the no-arg aggregation path
// (mcpRoots/AggregateRoots) and the explicit-arg resolution path
// (resolveRoots) without depending on any real plugin — internal/mcp
// deliberately imports none of plugins/* ("plugin-bare", mcp.go's package
// doc).
type excludeSchemeFake struct{ scheme string }

func (f excludeSchemeFake) Schemes() []string { return []string{f.scheme} }
func (f excludeSchemeFake) TypeTag() string   { return "cutting_garden-test-" + f.scheme + "-v1" }

func (f excludeSchemeFake) Types() []cutting_garden_plugins.NodeType {
	return []cutting_garden_plugins.NodeType{{Tag: f.scheme + "-leaf-v1"}}
}

func (f excludeSchemeFake) ListRoots(
	context.Context, *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	return nil, nil
}

func (f excludeSchemeFake) Roots(context.Context) ([]*url.URL, error) {
	return []*url.URL{{Scheme: f.scheme, Path: "/root"}}, nil
}

var (
	_ cutting_garden_plugins.RootProvider = excludeSchemeFake{}

	excludeSchemeFakeA = excludeSchemeFake{scheme: "mcpxa"}
	excludeSchemeFakeB = excludeSchemeFake{scheme: "mcpxb"}
)

func init() {
	cutting_garden_plugins.MustRegisterScheme(excludeSchemeFakeA)
	cutting_garden_plugins.MustRegisterScheme(excludeSchemeFakeB)
}

// TestExcludeSchemesFlag_Repeatable pins the flag.Value contract that
// makes -exclude-scheme=a -exclude-scheme=b ACCUMULATE (each occurrence
// calls Set once) rather than the last one winning, as a plain StringVar
// would.
func TestExcludeSchemesFlag_Repeatable(t *testing.T) {
	var values []string
	flag := excludeSchemesFlag{values: &values}

	if err := flag.Set("file"); err != nil {
		t.Fatalf("Set(file): %v", err)
	}
	if err := flag.Set("web"); err != nil {
		t.Fatalf("Set(web): %v", err)
	}
	if len(values) != 2 || values[0] != "file" || values[1] != "web" {
		t.Errorf("values = %v, want [file web]", values)
	}
	if got := flag.String(); got != "file,web" {
		t.Errorf("String() = %q, want %q", got, "file,web")
	}
}

func TestExcludeSchemesFlag_RejectsEmptyValue(t *testing.T) {
	var values []string
	flag := excludeSchemesFlag{values: &values}
	if err := flag.Set("   "); err == nil {
		t.Fatal("Set(whitespace-only) must error")
	}
	if len(values) != 0 {
		t.Errorf("values = %v after rejected Set, want unchanged", values)
	}
}

func TestFilterExcludedSchemes_DropsMatchingScheme(t *testing.T) {
	roots := []*url.URL{
		{Scheme: "mcpxa", Path: "/a"},
		{Scheme: "mcpxb", Path: "/b"},
	}
	out := filterExcludedSchemes(roots, excludedSchemeSet([]string{"mcpxa"}))
	if len(out) != 1 || out[0].Scheme != "mcpxb" {
		t.Errorf("filtered = %+v, want only the mcpxb root", out)
	}
}

func TestFilterExcludedSchemes_EmptySetIsNoOp(t *testing.T) {
	roots := []*url.URL{{Scheme: "mcpxa"}}
	out := filterExcludedSchemes(roots, excludedSchemeSet(nil))
	if len(out) != 1 || out[0].Scheme != "mcpxa" {
		t.Errorf("filtered = %+v, want unmodified", out)
	}
}

// TestMCPRoots_AggregationDropsExcludedScheme drives the no-arg branch:
// AggregateRoots's full result is filtered post-hoc, silently dropping the
// excluded root while leaving every other plugin's roots untouched.
func TestMCPRoots_AggregationDropsExcludedScheme(t *testing.T) {
	roots, err := mcpRoots(context.Background(), nil, []string{"mcpxa"})
	if err != nil {
		t.Fatalf("mcpRoots: %v", err)
	}
	var sawA, sawB bool
	for _, r := range roots {
		switch r.Scheme {
		case "mcpxa":
			sawA = true
		case "mcpxb":
			sawB = true
		}
	}
	if sawA {
		t.Errorf("mcpxa root present despite -exclude-scheme=mcpxa: %+v", roots)
	}
	if !sawB {
		t.Errorf("mcpxb root missing; exclusion filtered too much: %+v", roots)
	}
}

// TestResolveRoots_ExcludedSchemeIsUsageError drives the explicit-arg
// branch's defensive rejection: unlike aggregation's silent drop, an
// explicit argument naming an excluded scheme errors rather than
// disappearing quietly.
func TestResolveRoots_ExcludedSchemeIsUsageError(t *testing.T) {
	_, err := resolveRoots(
		[]string{"mcpxa://h/"}, excludedSchemeSet([]string{"mcpxa"}),
	)
	if err == nil {
		t.Fatal("resolveRoots with an excluded scheme argument must error")
	}
}

func TestResolveRoots_NonExcludedSchemeUnaffected(t *testing.T) {
	roots, err := resolveRoots(
		[]string{"mcpxb://h/"}, excludedSchemeSet([]string{"mcpxa"}),
	)
	if err != nil {
		t.Fatalf("resolveRoots: %v", err)
	}
	if len(roots) != 1 || roots[0].Scheme != "mcpxb" {
		t.Errorf("roots = %+v, want [mcpxb://h/]", roots)
	}
}

// TestRun_ExcludedSchemeExplicitArgIsUsageError drives the flag through
// real CLI dispatch (flag parsing included), exercising the same
// pre-server validation path TestRun_UnknownSchemeIsUsageError and
// TestRun_NonListableSchemeIsUsageError already cover for the unflagged
// cases.
func TestRun_ExcludedSchemeExplicitArgIsUsageError(t *testing.T) {
	if code := driveMCP(t, "-exclude-scheme=mcpxa", "mcpxa://h/"); code != 64 {
		t.Fatalf("exit = %d, want 64 (explicit arg names an excluded scheme)", code)
	}
}
