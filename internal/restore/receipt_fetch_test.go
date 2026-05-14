package restore

import (
	"strings"
	"testing"

	// Blank-import the file plugin so its init() registers under "",
	// "file" restore schemes. Without it, ResolveRestore returns an
	// empty-registry error before resolveRestorePlugin can dispatch.
	_ "github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugin_file"
)

// TestResolveRestorePlugin_SchemelessDispatchesToFile asserts a
// schemeless dest (e.g. "out", "./tmp/dest") routes to the file
// plugin's "" registration. The single happy-path positional surface
// for the file backend.
func TestResolveRestorePlugin_SchemelessDispatchesToFile(t *testing.T) {
	cases := []string{"out", "./tmp/dest", "/abs/path"}
	for _, dest := range cases {
		t.Run(dest, func(t *testing.T) {
			u, plugin, err := resolveRestorePlugin(dest)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if u == nil {
				t.Fatal("nil URL")
			}
			if plugin == nil {
				t.Fatal("nil plugin")
			}
			if got := plugin.TypeTag(); !strings.HasSuffix(got, "-fs-v1") {
				t.Errorf("expected file plugin (TypeTag ending -fs-v1), got %q", got)
			}
		})
	}
}

// TestResolveRestorePlugin_FileScheme asserts an explicit "file:"
// scheme also routes to the file plugin.
func TestResolveRestorePlugin_FileScheme(t *testing.T) {
	u, plugin, err := resolveRestorePlugin("file:/tmp/dest")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.Scheme != "file" {
		t.Errorf("expected scheme=file, got %q", u.Scheme)
	}
	if plugin == nil {
		t.Fatal("nil plugin")
	}
}

// TestResolveRestorePlugin_UnknownSchemeErrors asserts an unregistered
// scheme surfaces a registry error.
func TestResolveRestorePlugin_UnknownSchemeErrors(t *testing.T) {
	_, _, err := resolveRestorePlugin("s3://bucket/key")
	if err == nil {
		t.Fatal("expected error for unknown scheme, got nil")
	}
}
