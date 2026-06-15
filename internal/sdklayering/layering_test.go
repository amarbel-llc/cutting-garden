package sdklayering

import (
	"os/exec"
	"strings"
	"testing"
)

const (
	modulePath     = "github.com/amarbel-llc/cutting-garden"
	pkgsPrefix     = modulePath + "/pkgs/"
	internalPrefix = modulePath + "/internal/"
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
