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

// ReceiptType returns the receipt type-string for a capture kind, e.g.
// ReceiptType("git") == "cutting_garden-capture-receipt-git-v1". The
// kind names what is captured (fs, web, git), not the plugin binary.
func ReceiptType(kind string) string {
	return "cutting_garden-capture-receipt-" + kind + "-v1"
}
