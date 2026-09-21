package sdklayering

import (
	"os/exec"
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

func goListEdges(t *testing.T, tmpl, pattern string) [][2]string {
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

	var edges [][2]string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			edges = append(edges, [2]string{f[0], f[1]})
		}
	}
	return edges
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
func TestCommandComponentsDoesNotImportNodeView(t *testing.T) {
	for _, e := range productionImportEdges(t, commandComponents) {
		if e[1] == nodeView {
			t.Errorf("invalidation-cone violation: %s imports %s\n"+
				"command_components is the STABLE composition layer; the "+
				"presentation helpers live in node_view and depend on it, not "+
				"the other way round (move the shared helper down, not the edge up)",
				e[0], e[1])
		}
	}
}
