// Package claude_hooks implements the Claude Code hook protocol for the
// cutting-garden clown plugin. The plugin's hooks/hooks.json registers a
// PreToolUse hook scoped to cutting-garden's own MCP tools; the handler
// script execs `cutting-garden hook`, which routes stdin/stdout through
// Run.
//
// cutting-garden's MCP server (internal/mcp) now exposes the CUD write
// tools (create_node/put_node/patch_node/delete_node, FDR 0020). Run classifies a
// PreToolUse event for one of them through mcp_tool_perms — the SAME
// classifier that sets the tools' MCP annotations, so the hint a client
// sees and the decision here cannot drift (the #102 parity ask): a
// destructive tool emits `ask`, a read tool `allow`, and an unrecognized
// tool falls through to Claude Code's normal permission flow. Keeping the
// decision in Go — rather than a hooks.json matcher regex — follows
// dodder's internal/bravo/claude_hooks and spinclass's internal/hooks: it
// is unit-testable and has room to grow.
package claude_hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"code.linenisgreat.com/cutting-garden/internal/mcp_tool_perms"
)

// hookInput carries the subset of Claude Code's hook-event payload the
// decision table consumes; unused protocol fields (session_id, tool_input,
// cwd) are deliberately not decoded.
type hookInput struct {
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
}

// toolNamePrefix is the namespace Claude Code gives a plugin's MCP tools:
// the plugin "cutting-garden" registers an MCP server also named
// "cutting-garden", so its tools appear as
// mcp__plugin_cutting-garden_cutting-garden__<tool>. Stripping it yields
// the bare tool name a future decision table classifies.
//
// The hyphen in the plugin name is PRESERVED in clown's tool namespace —
// confirmed against clown's live naming scheme, where a hyphenated plugin
// keeps its hyphens (e.g. `mcp__plugin_clown-builtin-jobs_jobs__chat_send`).
// With cutting-garden's plugin name and stdio-server name both
// "cutting-garden" (plugin.json / clown.json), the prefix below is correct.
// The hooks.json matcher is still written hyphen/underscore-tolerant as a
// belt, and the MCP-level destructive annotation is an independent gate, so
// even a prefix miss degrades safely (no decision -> normal prompting). See
// cutting-garden#102.
const toolNamePrefix = "mcp__plugin_cutting-garden_cutting-garden__"

// Run decodes one Claude Code hook event from reader and writes a
// permission decision to writer when one applies: a non-PreToolUse event is
// ignored, a PreToolUse event whose tool is not one of cutting-garden's
// (prefix miss) or is unclassified falls through, and a recognized CUD tool
// is decided by its class — destructive => ask, read => allow.
func Run(reader io.Reader, writer io.Writer) error {
	var input hookInput

	if err := json.NewDecoder(reader).Decode(&input); err != nil {
		return fmt.Errorf("decoding hook input: %w", err)
	}

	if input.HookEventName != "PreToolUse" {
		return nil
	}

	tool, ok := strings.CutPrefix(input.ToolName, toolNamePrefix)
	if !ok {
		return nil
	}

	// Classify the bare tool name through the shared classifier (the same
	// one that sets the MCP tool annotations), so the annotation a client
	// sees and the decision here cannot drift. An unrecognized tool gets no
	// decision — fall through to Claude Code's normal permission flow rather
	// than inventing one.
	class, known := mcp_tool_perms.Classify(tool)
	if !known {
		return nil
	}
	switch class {
	case mcp_tool_perms.ClassDestructive:
		return writeDecision(writer, "ask",
			fmt.Sprintf("%s mutates live state; confirm before applying", tool))
	case mcp_tool_perms.ClassRead:
		return writeDecision(writer, "allow",
			fmt.Sprintf("%s is read-only", tool))
	default:
		return nil
	}
}

// writeDecision emits a single PreToolUse permission decision in Claude
// Code's hookSpecificOutput shape. It is the contract a future CUD decision
// table will write through; the unit test locks its output so that wiring
// inherits a verified format.
func writeDecision(writer io.Writer, decision, reason string) error {
	output := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       decision,
			"permissionDecisionReason": reason,
		},
	}

	return json.NewEncoder(writer).Encode(output)
}
