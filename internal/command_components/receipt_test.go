package command_components

import (
	"net/url"
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"

	// Blank-import the file plugin so its init() registers under
	// "", "file" restore schemes. Without it,
	// cutting_garden_plugins.ResolveRestore returns an empty-registry
	// error before ResolveRestorePlugin can dispatch.
	_ "github.com/amarbel-llc/cutting-garden/plugins/file"
	"github.com/amarbel-llc/madder/go/pkgs/ids"
	"github.com/amarbel-llc/madder/go/pkgs/markl"
)

// ---------------------------------------------------------------------
// ResolveRestorePlugin
// ---------------------------------------------------------------------

// TestResolveRestorePlugin_SchemelessDispatchesToFile asserts a
// schemeless dest (e.g. "out", "./tmp/dest") routes to the file
// plugin's "" registration. The single happy-path positional surface
// for the file backend.
func TestResolveRestorePlugin_SchemelessDispatchesToFile(t *testing.T) {
	cases := []string{"out", "./tmp/dest", "/abs/path"}
	for _, dest := range cases {
		t.Run(dest, func(t *testing.T) {
			u, plugin, err := ResolveRestorePlugin(dest)
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
	u, plugin, err := ResolveRestorePlugin("file:/tmp/dest")
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

// TestResolveRestorePlugin_UnknownSchemeErrors asserts an
// unregistered scheme surfaces a registry error.
func TestResolveRestorePlugin_UnknownSchemeErrors(t *testing.T) {
	_, _, err := ResolveRestorePlugin("s3://bucket/key")
	if err == nil {
		t.Fatal("expected error for unknown scheme, got nil")
	}
}

// ---------------------------------------------------------------------
// CheckReceiptTypeTag
// ---------------------------------------------------------------------

// stubPlugin satisfies cutting_garden_plugins.Plugin with a
// configurable TypeTag. Used to exercise the cross-scheme guard
// without registering a second real plugin.
type stubPlugin struct {
	tag string
}

func (s stubPlugin) Schemes() []string { return []string{"stub"} }
func (s stubPlugin) TypeTag() string   { return s.tag }

func TestCheckReceiptTypeTag_AcceptsMatchingTag(t *testing.T) {
	var rid markl.Id
	tt := capture_receipt.TypeStructV1
	plugin := stubPlugin{tag: tt.StringSansOp()}
	destURL, _ := url.Parse("stub://target")

	if err := CheckReceiptTypeTag(&rid, tt, plugin, destURL, "restore"); err != nil {
		t.Errorf("expected accept on matching tag, got error: %v", err)
	}
}

func TestCheckReceiptTypeTag_RefusesCrossSchemeRestore(t *testing.T) {
	var rid markl.Id
	receiptTT := capture_receipt.TypeStructV1
	plugin := stubPlugin{tag: "cutting_garden-capture_receipt-s3-v1"}
	destURL, _ := url.Parse("stub://bucket/key")

	err := CheckReceiptTypeTag(&rid, receiptTT, plugin, destURL, "restore")
	if err == nil {
		t.Fatal("expected refusal on cross-scheme, got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"type-tag",
		"cross-scheme restore is not supported",
		"cutting-garden#18",
		receiptTT.StringSansOp(),
		plugin.tag,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic missing %q; got: %s", want, msg)
		}
	}
}

func TestCheckReceiptTypeTag_RefusesCrossSchemeDiff(t *testing.T) {
	// Phase 4 step 3 will call CheckReceiptTypeTag with operation="diff".
	// Pin the diagnostic shape for that path too.
	var rid markl.Id
	receiptTT := capture_receipt.TypeStructV1
	plugin := stubPlugin{tag: "cutting_garden-capture_receipt-s3-v1"}
	destURL, _ := url.Parse("stub://bucket/key")

	err := CheckReceiptTypeTag(&rid, receiptTT, plugin, destURL, "diff")
	if err == nil {
		t.Fatal("expected refusal on cross-scheme, got nil")
	}
	if !strings.Contains(err.Error(), "cross-scheme diff is not supported") {
		t.Errorf("expected diff-specific phrasing; got: %v", err)
	}
}

func TestCheckReceiptTypeTag_RefusesUnknownReceiptTag(t *testing.T) {
	// A forged receipt could carry an arbitrary type-tag; the guard
	// refuses anything the resolved plugin does not declare.
	var rid markl.Id
	forged := ids.MustTypeStruct("bogus-type-v1")
	var plugin cutting_garden_plugins.Plugin = stubPlugin{tag: capture_receipt.TypeTagV1}
	destURL, _ := url.Parse("out")

	if err := CheckReceiptTypeTag(&rid, forged, plugin, destURL, "restore"); err == nil {
		t.Fatal("expected refusal on unknown receipt tag, got nil")
	}
}
