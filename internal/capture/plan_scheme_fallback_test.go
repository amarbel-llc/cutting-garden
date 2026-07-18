package capture

import (
	"net/url"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// schemeOnlyProtocolCapture is a ProtocolCapturePlugin registered ONLY via
// MustRegisterScheme — no MustRegisterCapture, no CaptureRoot, no
// ValidateSource — the shape RFC 0005 was written to make classifiable:
// today's classifyArg (pre-RFC-0005) could never resolve this plugin
// because captureRoot.plugin was typed to the full EntryV1 CapturePlugin
// interface, which this fake deliberately does not implement.
type schemeOnlyProtocolCapture struct{}

func (schemeOnlyProtocolCapture) Schemes() []string { return []string{"schemeonlyprotocolcapture"} }
func (schemeOnlyProtocolCapture) TypeTag() string {
	return "cutting_garden-schemeonlyprotocolcapture-v1"
}

func (schemeOnlyProtocolCapture) CaptureProtocol(
	req cutting_garden_plugins.ProtocolCaptureRequest,
) (cutting_garden_plugins.ProtocolCaptureResult, error) {
	return cutting_garden_plugins.ProtocolCaptureResult{
		ReceiptDigest: "sha256-fake",
		ObjectCount:   1,
	}, nil
}

func init() {
	cutting_garden_plugins.MustRegisterScheme(schemeOnlyProtocolCapture{})
}

// TestClassifyArg_FallsBackToSchemeRegistry_ProtocolOnly asserts a
// scheme-only-registered ProtocolCapturePlugin (no EntryV1 CapturePlugin
// implementation at all) is classifiable via the RFC 0005 §Resolution
// scheme-registry fallback, and that the missing ValidateSource is
// treated as "nothing to validate" (RFC 0005 §Source validation) rather
// than a classify failure.
func TestClassifyArg_FallsBackToSchemeRegistry_ProtocolOnly(t *testing.T) {
	got := classifyArg("schemeonlyprotocolcapture://endpoint/path")
	if got.kind != argKindCapture {
		t.Fatalf("kind = %d, want argKindCapture (err=%v)", got.kind, got.err)
	}
	if got.plugin == nil {
		t.Fatal("nil plugin")
	}
	if _, ok := got.plugin.(cutting_garden_plugins.ProtocolCapturePlugin); !ok {
		t.Errorf("resolved plugin %T does not implement ProtocolCapturePlugin", got.plugin)
	}
	if got.sourceURL == nil || got.sourceURL.Scheme != "schemeonlyprotocolcapture" {
		t.Errorf("sourceURL = %+v, want scheme schemeonlyprotocolcapture", got.sourceURL)
	}
}

// TestResolveCapturePlugin_ProtocolOnlyScheme is the narrower unit-level
// pin on resolveCapturePlugin itself: given a scheme registered only via
// MustRegisterScheme with a value implementing ONLY ProtocolCapturePlugin,
// resolution must succeed (miss the typed capture registry, hit the
// scheme-registry fallback, and recognize the protocol capability).
func TestResolveCapturePlugin_ProtocolOnlyScheme(t *testing.T) {
	plugin, err := resolveCapturePlugin("schemeonlyprotocolcapture")
	if err != nil {
		t.Fatalf("resolveCapturePlugin: %v", err)
	}
	if _, ok := plugin.(cutting_garden_plugins.ProtocolCapturePlugin); !ok {
		t.Errorf("resolved plugin %T does not implement ProtocolCapturePlugin", plugin)
	}
}

// TestResolveCapturePlugin_UnknownSchemeErrors pins the miss case: a
// scheme registered nowhere (neither typed capture registry nor base
// scheme registry) still errors.
func TestResolveCapturePlugin_UnknownSchemeErrors(t *testing.T) {
	if _, err := resolveCapturePlugin("totally-unregistered-scheme"); err == nil {
		t.Fatal("expected error for unregistered scheme, got nil")
	}
}

// TestValidateCaptureSource_SkipsWhenPluginHasNoValidator pins RFC 0005
// §Source validation: a plugin implementing neither SourceValidator nor
// CapturePlugin is not an error — validation is simply skipped.
func TestValidateCaptureSource_SkipsWhenPluginHasNoValidator(t *testing.T) {
	if err := validateCaptureSource(
		schemeOnlyProtocolCapture{}, &url.URL{Scheme: "schemeonlyprotocolcapture"}, "raw",
	); err != nil {
		t.Errorf("expected nil (validation skipped), got %v", err)
	}
}
