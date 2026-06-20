// Package mcp_tool_perms is the single source of truth for how
// cutting-garden's MCP tools classify for permission purposes. One
// classifier feeds two consumers (cutting-garden#102, FDR 0020):
//
//   - the `mcp` server's tool annotations (a destructive tool advertises
//     ToolAnnotations.DestructiveHint), and
//   - the clown PreToolUse hook decision table (internal/claude_hooks),
//     which maps a destructive tool to `ask` and a read tool to `allow`.
//
// Keeping the mapping here (rather than duplicated at each site) means the
// annotation a client sees and the gate the hook applies can never drift.
// It is deliberately a leaf package — it imports neither internal/mcp nor
// internal/claude_hooks, so both can depend on it without a cycle.
package mcp_tool_perms

// Class is a tool's permission classification.
type Class string

const (
	// ClassRead is a tool that does not mutate live state (safe to allow).
	ClassRead Class = "read"
	// ClassDestructive is a tool that mutates live state (gate with ask).
	ClassDestructive Class = "destructive"
)

// Tool names — the base tool names the `mcp` server registers, without the
// clown `mcp__plugin_<plugin>__` prefix the hook strips before classifying.
// Shared so the registration and the classification agree.
const (
	ToolCreateNode        = "create_node"
	ToolUpdateNode        = "update_node"
	ToolDeleteNode        = "delete_node"
	ToolDescribeNodeTypes = "describe_node_types"
)

// Classify returns the permission class of a tool by its base name (no
// plugin prefix), and whether the name is known. The three CUD write tools
// are destructive; the schema-discovery tool is read-only; an unknown name
// is unclassified (ok=false) so the caller applies its own default — the
// hook falls through to normal prompting rather than inventing a decision.
func Classify(toolName string) (Class, bool) {
	switch toolName {
	case ToolCreateNode, ToolUpdateNode, ToolDeleteNode:
		return ClassDestructive, true
	case ToolDescribeNodeTypes:
		return ClassRead, true
	default:
		return "", false
	}
}
