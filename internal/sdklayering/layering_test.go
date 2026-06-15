package sdklayering

import (
	"os/exec"
	"strings"
	"testing"
)

const (
	modulePath = "github.com/amarbel-llc/cutting-garden"
	pkgsPrefix = modulePath + "/pkgs/"
)

// TestNoInversion_InternalDoesNotImportPkgs enforces the RFC 0009 §4
// no-inversion rule: no package under internal/ may import a pkgs/ facade.
// pkgs/ is the dagnabit-generated OUTWARD face of internal/, so an
// internal/ -> pkgs/ edge is inverted layering (internal depending on its
// own public veneer). Plugins consume the SDK from OUTSIDE internal/
// (plugins/<scheme>/, examples/, or an external module), never from within
// it; this is what keeps the published surface honest as the in-tree
// plugins migrate out (RFC 0009 §5).
//
// Checking direct imports of every internal/ package suffices: a
// transitive internal -> internal -> pkgs path is caught at the
// intermediate package, which directly imports pkgs/.
func TestNoInversion_InternalDoesNotImportPkgs(t *testing.T) {
	// go list emits, per internal package, one "<importer> <imported>"
	// line per direct import. The {{...}} are go-template actions, not
	// just(1) interpolation — this is why the guard lives in a Go test and
	// not a justfile recipe.
	const tmpl = `{{$p := .ImportPath}}{{range .Imports}}{{$p}} {{.}}` + "\n" + `{{end}}`

	out, err := exec.Command(
		"go", "list", "-f", tmpl, modulePath+"/internal/...",
	).Output()
	if err != nil {
		t.Fatalf("go list internal/...: %v", err)
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		importer, imported := fields[0], fields[1]
		if strings.HasPrefix(imported, pkgsPrefix) {
			t.Errorf("no-inversion violation (RFC 0009 §4): %s imports %s\n"+
				"internal/ must not import pkgs/; move the consumer out of internal/ "+
				"(plugins/<scheme>/, examples/, or its own module)",
				importer, imported)
		}
	}
}
