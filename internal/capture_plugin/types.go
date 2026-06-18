package capture_plugin

// Protocol-defined node type-strings (RFC 0002 §Protocol-Defined Node
// Types). The `jcs-` prefix marks nodes that carry a JCS-canonical JSON
// body; metadata-only nodes have no prefix.
const (
	// TypeIdentity is the identity subtree root (metadata-only).
	TypeIdentity = "cutting_garden-capture-identity-v1"
	// TypeInvocation carries the resolved request parameters (body).
	TypeInvocation = "jcs-cutting_garden-capture-invocation-v1"
	// TypeEnvironment groups host/binary/plugin (metadata-only).
	TypeEnvironment = "cutting_garden-capture-environment-v1"
	// TypeHost carries os/kernel/arch/libc (body).
	TypeHost = "jcs-cutting_garden-capture-environment-host-v1"
	// TypeBinary carries the plugin binary's name/version/digest (body).
	TypeBinary = "jcs-cutting_garden-capture-environment-binary-v1"
	// TypeOutcome carries per-run datetime + stripped residue (body).
	TypeOutcome = "jcs-cutting_garden-capture-outcome-v1"
)

// frozenHyphenKinds are the protocol-receipt kinds shipped before the
// #112 prefix convergence. Their receipts are immutable, so their
// type-strings keep the legacy hyphen-separated `capture-receipt` prefix
// forever — a kind in this set MUST render byte-identically to what it
// emitted before #112. New kinds (and the next version of these) use the
// converged underscore prefix; see ReceiptType.
var frozenHyphenKinds = map[string]bool{
	"git": true,
	"web": true,
}

// ReceiptType returns the receipt type-string for a capture kind, e.g.
// ReceiptType("caldav") == "cutting_garden-capture_receipt-caldav-v1".
// The kind names what is captured (caldav, web, git), not the plugin
// binary.
//
// Per #112 the canonical prefix is the underscore-bound `capture_receipt`
// (the underscore binds `capture`+`receipt` into one compound noun, which
// binds tighter than the hyphen segment separators). The pre-#112 kinds
// in frozenHyphenKinds keep the legacy hyphen `capture-receipt` prefix so
// their already-written, immutable receipts keep dispatching unchanged;
// every other (new) kind gets the converged underscore form.
func ReceiptType(kind string) string {
	if frozenHyphenKinds[kind] {
		return "cutting_garden-capture-receipt-" + kind + "-v1"
	}
	return "cutting_garden-capture_receipt-" + kind + "-v1"
}

// init registers the protocol-defined node types into the build-time
// type-signature registry (RFC 0002 §Type Signatures). Bindings register
// their own receipt/payload/leaf types. Media types follow the
// application/vnd.cutting-garden.<thing>+<body-format> convention.
func init() {
	RegisterType(TypeDef{
		TypeString:    TypeIdentity,
		IANAMediaType: "application/vnd.cutting-garden.capture-identity+hyphence",
	})
	RegisterType(TypeDef{
		TypeString:    TypeInvocation,
		IANAMediaType: "application/vnd.cutting-garden.capture-invocation+jcs",
	})
	RegisterType(TypeDef{
		TypeString:    TypeEnvironment,
		IANAMediaType: "application/vnd.cutting-garden.capture-environment+hyphence",
	})
	RegisterType(TypeDef{
		TypeString:    TypeHost,
		IANAMediaType: "application/vnd.cutting-garden.capture-environment-host+jcs",
	})
	RegisterType(TypeDef{
		TypeString:    TypeBinary,
		IANAMediaType: "application/vnd.cutting-garden.capture-environment-binary+jcs",
	})
	RegisterType(TypeDef{
		TypeString:    TypeOutcome,
		IANAMediaType: "application/vnd.cutting-garden.capture-outcome+jcs",
	})
}
