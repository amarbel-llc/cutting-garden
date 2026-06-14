// Package claude_hooks implements the Claude Code hook protocol for the
// cutting-garden clown plugin. The plugin's hooks/hooks.json registers a
// PreToolUse hook scoped to cutting-garden's own MCP tools; the handler
// script execs `cutting-garden hook`, which routes stdin/stdout through
// Run.
//
// cutting-garden's MCP server (internal/mcp) is read-only resource
// discovery and exposes NO tools (FDR 0015), so today there is nothing to
// auto-approve: every event falls through to Claude Code's normal
// permission flow and Run is a deliberate no-op scaffold. It is wired now
// so that when create/update/delete tools land, the decision table is a
// localized edit here (read-only => allow, destructive => ask) rather than
// new plumbing. Keeping the decision in Go — rather than a hooks.json
// matcher regex — follows dodder's internal/bravo/claude_hooks and
// spinclass's internal/hooks: it is unit-testable and has room to grow.
// The full parity (a shared classifier feeding both the MCP tool
// annotations and this table) is tracked in cutting-garden#102.
package claude_hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
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
// NB: the separator treatment of the hyphenated plugin name is assumed
// here (hyphen preserved, matching how clown surfaces other hyphenated
// names) and MUST be confirmed against a live clown session when the first
// MCP tool ships — until then this path is never exercised because no
// tools exist. The hooks.json matcher is written hyphen/underscore-tolerant
// so the handler still fires regardless of the real separator; a prefix
// miss here merely yields no decision (safe fall-through to normal
// prompting). See cutting-garden#102.
const toolNamePrefix = "mcp__plugin_cutting-garden_cutting-garden__"

// Run decodes one Claude Code hook event from reader and writes a
// permission decision to writer when one applies. cutting-garden's MCP
// surface is entirely read-only resource discovery with no tools today, so
// no event yields a decision: non-PreToolUse events are ignored, and a
// PreToolUse event for a cutting-garden tool (none exist) falls through.
// When write tools arrive, classify the stripped tool name below and emit
// via writeDecision.
func Run(reader io.Reader, writer io.Writer) error {
	var input hookInput

	if err := json.NewDecoder(reader).Decode(&input); err != nil {
		return fmt.Errorf("decoding hook input: %w", err)
	}

	if input.HookEventName != "PreToolUse" {
		return nil
	}

	if _, ok := strings.CutPrefix(input.ToolName, toolNamePrefix); !ok {
		return nil
	}

	// No tools to classify yet — fall through to Claude Code's normal
	// permission flow. CUD tools get their allow/ask table here, emitting
	// through writeDecision. See cutting-garden#102.
	return nil
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
