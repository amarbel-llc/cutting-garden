package sdklayering

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const (
	modulePath     = "code.linenisgreat.com/cutting-garden"
	pkgsPrefix     = modulePath + "/pkgs/"
	internalPrefix = modulePath + "/internal/"
	pluginsPrefix  = modulePath + "/plugins/"
	// commandComponents is the stable composition layer (config, roots,
	// store resolution, receipts) and nodeView the presentation layer
	// split out of it; the edge between them runs one way only.
	commandComponents = internalPrefix + "command_components"
	nodeView          = internalPrefix + "node_view"
	// pluginAggregator legitimately blank-imports not-yet-migrated
	// in-tree plugins during the RFC 0009 §5 migration, so it is exempt
	// from the "no internal/ imports" rule below.
	pluginAggregator = modulePath + "/plugins/all"
)

// importEdges returns every "<importer> <imported>" edge for the packages
// matched by pattern, including test and external-test imports. The
// {{...}} are go-template actions, not just(1) interpolation — which is
// why this guard lives in a Go test, not a justfile recipe.
func importEdges(t *testing.T, pattern string) [][2]string {
	t.Helper()
	const tmpl = `{{$p := .ImportPath}}` +
		`{{range .Imports}}{{$p}} {{.}}` + "\n" + `{{end}}` +
		`{{range .TestImports}}{{$p}} {{.}}` + "\n" + `{{end}}` +
		`{{range .XTestImports}}{{$p}} {{.}}` + "\n" + `{{end}}`
	return goListEdges(t, tmpl, pattern)
}

// productionImportEdges is importEdges restricted to each package's
// non-test imports — the edges that decide a package's build (and, under
// godyn, its invalidation cone).
func productionImportEdges(t *testing.T, pattern string) [][2]string {
	t.Helper()
	const tmpl = `{{$p := .ImportPath}}` +
		`{{range .Imports}}{{$p}} {{.}}` + "\n" + `{{end}}`
	return goListEdges(t, tmpl, pattern)
}

// testImportEdges is importEdges restricted to each package's TEST imports
// — the in-package (_test.go) and external-test (package foo_test) edges
// that decide what a test lane builds, and nothing the production build
// sees. Complements productionImportEdges.
func testImportEdges(t *testing.T, pattern string) [][2]string {
	t.Helper()
	const tmpl = `{{$p := .ImportPath}}` +
		`{{range .TestImports}}{{$p}} {{.}}` + "\n" + `{{end}}` +
		`{{range .XTestImports}}{{$p}} {{.}}` + "\n" + `{{end}}`
	return goListEdges(t, tmpl, pattern)
}

// productionDeps returns pkg's full TRANSITIVE production dependency
// closure — go list's .Deps, which is imports-of-imports with test files
// excluded. This is the set that decides what a godyn edit re-derives,
// and the only honest basis for a reachability guard: .Imports sees one
// hop and would miss a chain through an intermediate package.
func productionDeps(t *testing.T, pkg string) []string {
	t.Helper()
	const tmpl = `{{range .Deps}}{{.}}` + "\n" + `{{end}}`
	return goListLines(t, tmpl, pkg)
}

func goListEdges(t *testing.T, tmpl, pattern string) [][2]string {
	t.Helper()
	var edges [][2]string
	for _, line := range goListLines(t, tmpl, pattern) {
		if f := strings.Fields(line); len(f) == 2 {
			edges = append(edges, [2]string{f[0], f[1]})
		}
	}
	return edges
}

func goListLines(t *testing.T, tmpl, pattern string) []string {
	t.Helper()
	// A per-package godyn test run has no go toolchain or module tree;
	// `just debug-test-layering` (go test through the godyn-go escape
	// hatch) is where this guard runs.
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain on PATH")
	}

	out, err := exec.Command("go", "list", "-f", tmpl, pattern).Output()
	if err != nil {
		t.Fatalf("go list %s: %v", pattern, err)
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

// productionPath reconstructs a production-import chain from -> ... -> to
// so a reachability failure can NAME the intermediate that reintroduced
// the edge. Best effort, for the error message only — the assertion
// itself is productionDeps. It walks the internal/ package graph, which
// is where such a chain must live: a hop out to pkgs/ or plugins/ and
// back is already forbidden by the two clauses above. Returns nil when no
// path is found over that graph.
func productionPath(t *testing.T, from, to string) []string {
	t.Helper()
	imports := map[string][]string{}
	for _, e := range productionImportEdges(t, internalPrefix+"...") {
		imports[e[0]] = append(imports[e[0]], e[1])
	}

	cameFrom := map[string]string{from: ""}
	for queue := []string{from}; len(queue) > 0; queue = queue[1:] {
		for _, next := range imports[queue[0]] {
			if _, seen := cameFrom[next]; seen {
				continue
			}
			cameFrom[next] = queue[0]
			if next != to {
				queue = append(queue, next)
				continue
			}
			var path []string
			for at := next; at != ""; at = cameFrom[at] {
				path = append([]string{at}, path...)
			}
			return path
		}
	}
	return nil
}

// TestNoInversion_InternalDoesNotImportPkgs enforces the RFC 0009 §4
// no-inversion rule: no package under internal/ may import a pkgs/ facade.
// pkgs/ is the dagnabit-generated OUTWARD face of internal/, so an
// internal/ -> pkgs/ edge is inverted layering (internal depending on its
// own public veneer). Consumers live OUTSIDE internal/.
//
// Checking direct imports suffices: a transitive internal -> internal ->
// pkgs path is caught at the intermediate package, which directly imports
// pkgs/.
func TestNoInversion_InternalDoesNotImportPkgs(t *testing.T) {
	for _, e := range importEdges(t, internalPrefix+"...") {
		importer, imported := e[0], e[1]
		if strings.HasPrefix(imported, pkgsPrefix) {
			t.Errorf("no-inversion violation (RFC 0009 §4): %s imports %s\n"+
				"internal/ must not import pkgs/; move the consumer out of internal/ "+
				"(plugins/<scheme>/, examples/, or its own module)",
				importer, imported)
		}
	}
}

// TestMigratedPluginsConsumeTheFacade enforces the other half of RFC 0009
// §4: a plugin relocated under plugins/<scheme>/ must consume the pkgs/
// SDK, not internal/ — so an in-repo plugin is structurally identical to
// an out-of-tree one (which the Go internal rule would forbid from
// importing internal/ at all). The plugins/all aggregator is exempt: it
// blank-imports the still-internal plugins until each migrates.
func TestMigratedPluginsConsumeTheFacade(t *testing.T) {
	for _, e := range importEdges(t, modulePath+"/plugins/...") {
		importer, imported := e[0], e[1]
		if importer == pluginAggregator {
			continue
		}
		if strings.HasPrefix(imported, internalPrefix) {
			t.Errorf("layering violation (RFC 0009 §4): migrated plugin %s imports %s\n"+
				"a plugin under plugins/ must consume the pkgs/ facade, not internal/",
				importer, imported)
		}
	}
}

// TestInternalDoesNotImportPlugins is the invalidation-cone guard
// (docs/plans/2026-09-21-invalidation-cone-moves.md D7): no package under
// internal/ imports plugins/ in PRODUCTION code, so a plugin edit
// re-derives only plugins/<scheme>, plugins/all and the cmd/ mains under
// godyn. Plugins reach the framework through the SDK registries
// (cutting_garden_plugins.MustRegisterScheme / MustRegisterConfigSection),
// never the other way round. Test-only imports are a separate clause
// (Unit C of the same plan).
func TestInternalDoesNotImportPlugins(t *testing.T) {
	for _, e := range productionImportEdges(t, internalPrefix+"...") {
		importer, imported := e[0], e[1]
		if strings.HasPrefix(imported, pluginsPrefix) {
			t.Errorf("invalidation-cone violation: %s imports %s\n"+
				"internal/ must not import plugins/ in production code; register the "+
				"plugin's contribution through an SDK registry instead",
				importer, imported)
		}
	}
}

// testPluginImportAllowList names the internal/ packages whose TESTS may
// still import plugins/. Empty since the framework test lanes switched to
// in-package fakes (invalidation-cone B3); an entry here is a deliberate,
// justified exception, not a parking spot — a test that needs a REAL plugin
// linked belongs in zz-tests_bats against the built binary, the way
// health's capability report does.
var testPluginImportAllowList = map[string]bool{}

// TestInternalTestsDoNotImportPlugins is the test-lane half of the
// invalidation-cone guard (docs/plans/2026-09-21-invalidation-cone-moves.md
// D7). TestInternalDoesNotImportPlugins above covers production imports;
// this one covers _test.go imports, which godyn's per-package test lane
// derives from just the same — a framework package blank-importing
// plugins/file to populate a registry puts every plugin source in that
// lane's invalidation cone, so a plugin edit re-runs framework tests that
// have nothing to do with it. Register an in-package fake instead (see
// internal/command_components/traversal_registration_test.go for the
// pattern).
func TestInternalTestsDoNotImportPlugins(t *testing.T) {
	for _, e := range testImportEdges(t, internalPrefix+"...") {
		importer, imported := e[0], e[1]
		if !strings.HasPrefix(imported, pluginsPrefix) {
			continue
		}
		if testPluginImportAllowList[importer] {
			continue
		}
		t.Errorf("invalidation-cone violation: %s's tests import %s\n"+
			"an internal/ test must not import plugins/; register an in-package "+
			"fake plugin with the SDK registries instead, or move an assertion "+
			"that genuinely needs the real plugins linked to zz-tests_bats",
			importer, imported)
	}
}

// TestCommandComponentsDoesNotImportNodeView pins the direction of the
// command_components/node_view split
// (docs/plans/2026-09-21-invalidation-cone-moves.md D5): the presentation
// helpers moved OUT of the 9-importer composition hub so that organize/list
// rendering churn stops re-deriving capture, restore, diff, serve, failures
// and blob_writer under godyn. A command_components -> node_view edge would
// hand every one of those the presentation cone (and internal/trellis with
// it) straight back, silently — Go's own cycle check cannot catch it,
// because node_view deliberately imports nothing from command_components.
// A helper both layers need stays in command_components and node_view
// imports it, never the reverse.
//
// The check is TRANSITIVE (the whole .Deps closure), not direct: godyn
// invalidates on the closure, so command_components -> Y -> node_view
// re-derives those six packages exactly as a direct edge would, and a
// future Y is the likelier way the regression arrives. Note this does NOT
// mirror the "direct suffices" reasoning of
// TestNoInversion_InternalDoesNotImportPkgs above — that holds only
// because it sweeps every internal/ package, so an offending intermediate
// trips on its own direct edge. This clause names ONE package, so it has
// no such property and must walk the closure itself.
func TestCommandComponentsDoesNotImportNodeView(t *testing.T) {
	if !slices.Contains(productionDeps(t, commandComponents), nodeView) {
		return
	}
	chain := commandComponents + " -> ... -> " + nodeView
	if path := productionPath(t, commandComponents, nodeView); path != nil {
		chain = strings.Join(path, " -> ")
	}
	t.Errorf("invalidation-cone violation: %s reaches %s\n  %s\n"+
		"command_components is the STABLE composition layer; the "+
		"presentation helpers live in node_view and depend on it, not "+
		"the other way round (move the shared helper down, not the edge up)",
		commandComponents, nodeView, chain)
}
