package organize

import "testing"

// TestResolveEditorCommand pins the editor precedence: $VISUAL wins over $EDITOR,
// which wins over the vi fallback.
func TestResolveEditorCommand(t *testing.T) {
	t.Run("VISUAL wins", func(t *testing.T) {
		t.Setenv("VISUAL", "code -w")
		t.Setenv("EDITOR", "nano")
		if got := resolveEditorCommand(); got != "code -w" {
			t.Errorf("resolveEditorCommand() = %q, want %q", got, "code -w")
		}
	})

	t.Run("EDITOR when VISUAL unset", func(t *testing.T) {
		t.Setenv("VISUAL", "")
		t.Setenv("EDITOR", "nano")
		if got := resolveEditorCommand(); got != "nano" {
			t.Errorf("resolveEditorCommand() = %q, want %q", got, "nano")
		}
	})

	t.Run("vi fallback when neither set", func(t *testing.T) {
		t.Setenv("VISUAL", "")
		t.Setenv("EDITOR", "")
		if got := resolveEditorCommand(); got != "vi" {
			t.Errorf("resolveEditorCommand() = %q, want %q", got, "vi")
		}
	})
}
