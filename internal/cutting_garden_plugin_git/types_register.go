package cutting_garden_plugin_git

import "github.com/amarbel-llc/cutting-garden/internal/capture_plugin"

// gitObjectTypes is the closed set of git object kinds the plugin stores
// as payload leaves. Each maps to a registered leaf type so its
// references carry type-signature locks.
var gitObjectTypes = []string{"commit", "tree", "blob", "tag"}

// init registers the git binding's node types into the build-time
// type-signature registry (RFC 0002 §Type Signatures, mechanism (1)).
// Media types follow application/vnd.cutting-garden.<thing>+<format>;
// the raw git object leaves carry a git-object media type with no
// hyphence/jcs framing.
func init() {
	capture_plugin.RegisterType(capture_plugin.TypeDef{
		TypeString:    capture_plugin.ReceiptType(captureKind),
		IANAMediaType: "application/vnd.cutting-garden.capture-receipt-git+hyphence",
	})
	capture_plugin.RegisterType(capture_plugin.TypeDef{
		TypeString:         payloadType,
		IANAMediaType:      "application/vnd.cutting-garden.git-capture-payload+jcs",
		PayloadCardinality: "single",
	})
	capture_plugin.RegisterType(capture_plugin.TypeDef{
		TypeString:    pluginEnvType,
		IANAMediaType: "application/vnd.cutting-garden.git-capture-environment+jcs",
	})
	for _, gt := range gitObjectTypes {
		capture_plugin.RegisterType(capture_plugin.TypeDef{
			TypeString:    objectTypeString(gt),
			IANAMediaType: "application/vnd.cutting-garden.git-object-" + gt,
		})
	}
}
