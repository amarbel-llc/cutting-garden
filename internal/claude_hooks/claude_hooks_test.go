package claude_hooks

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/mcp_tool_perms"
)

func TestRun_MalformedJSON_Errors(t *testing.T) {
	var out bytes.Buffer
	if err := Run(strings.NewReader("{not json"), &out); err == nil {
		t.Fatal("expected a decode error, got nil")
	}
	if out.Len() != 0 {
		t.Fatalf("expected no output on error, got %q", out.String())
	}
}

func TestRun_NonPreToolUse_NoOutput(t *testing.T) {
	var out bytes.Buffer
	in := `{"hook_event_name":"PostToolUse","tool_name":"` + toolNamePrefix + `anything"}`
	if err := Run(strings.NewReader(in), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no decision for a non-PreToolUse event, got %q", out.String())
	}
}

func TestRun_ForeignTool_NoOutput(t *testing.T) {
	var out bytes.Buffer
	in := `{"hook_event_name":"PreToolUse","tool_name":"mcp__plugin_madder_madder__blobs_get"}`
	if err := Run(strings.NewReader(in), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no decision for a non-cutting-garden tool, got %q", out.String())
	}
}

// A correctly namespaced cutting-garden tool that is not one of the
// classified CUD tools falls through with no decision — the hook never
// invents one.
func TestRun_UnclassifiedTool_NoDecision(t *testing.T) {
	var out bytes.Buffer
	in := `{"hook_event_name":"PreToolUse","tool_name":"` + toolNamePrefix + `some_other_tool"}`
	if err := Run(strings.NewReader(in), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no decision for an unclassified tool, got %q", out.String())
	}
}

// Each CUD write tool is destructive, so its PreToolUse event is gated with
// `ask` — the #102 enforcement.
func TestRun_CUDTools_Ask(t *testing.T) {
	for _, tool := range []string{
		mcp_tool_perms.ToolCreateNode,
		mcp_tool_perms.ToolUpdateNode,
		mcp_tool_perms.ToolDeleteNode,
	} {
		var out bytes.Buffer
		in := `{"hook_event_name":"PreToolUse","tool_name":"` + toolNamePrefix + tool + `"}`
		if err := Run(strings.NewReader(in), &out); err != nil {
			t.Fatalf("Run(%s): %v", tool, err)
		}
		var got struct {
			HookSpecificOutput struct {
				PermissionDecision string `json:"permissionDecision"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal %s: %v (out=%q)", tool, err, out.String())
		}
		if got.HookSpecificOutput.PermissionDecision != "ask" {
			t.Errorf("%s decision = %q, want ask", tool, got.HookSpecificOutput.PermissionDecision)
		}
	}
}

// writeDecision is the contract a future CUD decision table writes through;
// lock its shape now so that wiring inherits a verified format.
func TestWriteDecision_Shape(t *testing.T) {
	var out bytes.Buffer
	if err := writeDecision(&out, "allow", "read-only"); err != nil {
		t.Fatalf("writeDecision: %v", err)
	}

	var got struct {
		HookSpecificOutput struct {
			HookEventName            string `json:"hookEventName"`
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("hookEventName = %q, want PreToolUse", got.HookSpecificOutput.HookEventName)
	}
	if got.HookSpecificOutput.PermissionDecision != "allow" {
		t.Errorf("permissionDecision = %q, want allow", got.HookSpecificOutput.PermissionDecision)
	}
	if got.HookSpecificOutput.PermissionDecisionReason != "read-only" {
		t.Errorf("permissionDecisionReason = %q, want read-only", got.HookSpecificOutput.PermissionDecisionReason)
	}
}
