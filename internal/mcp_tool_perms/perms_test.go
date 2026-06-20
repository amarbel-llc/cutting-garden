package mcp_tool_perms

import "testing"

func TestClassify_CUDToolsAreDestructive(t *testing.T) {
	for _, tool := range []string{ToolCreateNode, ToolUpdateNode, ToolDeleteNode} {
		class, ok := Classify(tool)
		if !ok {
			t.Errorf("%q should be classified", tool)
		}
		if class != ClassDestructive {
			t.Errorf("%q class = %q, want %q", tool, class, ClassDestructive)
		}
	}
}

func TestClassify_UnknownIsUnclassified(t *testing.T) {
	if _, ok := Classify("resources_read"); ok {
		t.Error("an unknown tool must be unclassified (ok=false)")
	}
	if _, ok := Classify(""); ok {
		t.Error("the empty tool name must be unclassified")
	}
}
