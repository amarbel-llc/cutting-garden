package mcp_tool_perms

import "testing"

func TestClassify_WriteToolIsDestructive(t *testing.T) {
	class, ok := Classify(ToolMutateNodes)
	if !ok {
		t.Fatalf("%q should be classified", ToolMutateNodes)
	}
	if class != ClassDestructive {
		t.Errorf("%q class = %q, want %q", ToolMutateNodes, class, ClassDestructive)
	}
}

// The singular write tools are gone (cutting-garden#315); a stale client
// calling one must not be handed a decision for a tool that no longer exists.
func TestClassify_RemovedWriteToolsAreUnclassified(t *testing.T) {
	for _, tool := range []string{"create_node", "put_node", "patch_node", "delete_node", "bulk_mutate"} {
		if _, ok := Classify(tool); ok {
			t.Errorf("%q should be unclassified", tool)
		}
	}
}

func TestClassify_ReadToolsAreRead(t *testing.T) {
	for _, tool := range []string{ToolDescribeNodeTypes, ToolReadNode, ToolListNodes, ToolReadFacets} {
		class, ok := Classify(tool)
		if !ok {
			t.Fatalf("%q should be classified", tool)
		}
		if class != ClassRead {
			t.Errorf("%q class = %q, want %q", tool, class, ClassRead)
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
