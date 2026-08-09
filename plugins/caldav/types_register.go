package caldav

import "code.linenisgreat.com/cutting-garden/pkgs/capture_plugin"

// Protocol-defined node type-strings for the caldav RFC 0011 binding.
const (
	// captureKind tags the receipt: cutting_garden-capture_receipt-caldav-v1
	// (the underscore prefix, #112 — caldav is a new family debuting on
	// the converged prefix at v1).
	captureKind = "caldav"
	// payloadType is the caldav payload node: a metadata node referencing
	// every stored .ics object plus a JCS body of capture metadata
	// (endpoint, per-resource {id, etag} freshness records, count).
	payloadType = "jcs-caldav-payload-v1"
	// pluginEnvType is the caldav plugin's identity-affecting environment
	// node (the captured component set).
	pluginEnvType = "jcs-caldav-environment-v1"
	// captureFormat is the invocation `format` value for caldav captures.
	captureFormat = "caldav-objects"
)

// init registers the caldav binding's node types into the build-time
// type-signature registry (RFC 0002 §Type Signatures mechanism (1)), so
// every reference into the caldav receipt tree carries an `@<sig>` type
// lock consumers verify. Media types follow
// application/vnd.cutting-garden.<thing>+<format>; the object leaves carry
// the iCalendar media type with no hyphence/jcs framing (they are the
// verbatim text/calendar body). The per-component object leaf tags
// (typeVTODO/typeVEVENT/typeVJOURNAL) are the SAME tags the traversal layer
// uses (traversal.go) — the unified tag grammar (FDR 0018), now three sibling
// tags moving in sync between traversal and the receipt.
func init() {
	capture_plugin.RegisterType(capture_plugin.TypeDef{
		TypeString:    capture_plugin.ReceiptType(captureKind),
		IANAMediaType: "application/vnd.cutting-garden.capture-receipt-caldav+hyphence",
	})
	capture_plugin.RegisterType(capture_plugin.TypeDef{
		TypeString:         payloadType,
		IANAMediaType:      "application/vnd.cutting-garden.caldav-payload+jcs",
		PayloadCardinality: "single",
	})
	capture_plugin.RegisterType(capture_plugin.TypeDef{
		TypeString:    pluginEnvType,
		IANAMediaType: "application/vnd.cutting-garden.caldav-environment+jcs",
	})
	for _, tag := range []string{typeVTODO, typeVEVENT, typeVJOURNAL} {
		capture_plugin.RegisterType(capture_plugin.TypeDef{
			TypeString:    tag,
			IANAMediaType: "text/calendar",
		})
	}
}
