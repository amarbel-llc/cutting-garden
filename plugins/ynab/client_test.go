package ynab

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// snippet must cut on a rune boundary: a multi-byte character straddling the
// limit may not leave invalid UTF-8 in a diagnostic.
func TestSnippet_NeverSplitsARune(t *testing.T) {
	// "é" is two bytes; place it so byte 256 falls inside it.
	body := []byte(strings.Repeat("a", 255) + "é" + strings.Repeat("b", 40))
	got := snippet(body)
	if !utf8.ValidString(got) {
		t.Errorf("snippet is not valid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated snippet should end in an ellipsis: %q", got)
	}
	if got := snippet([]byte("  short  ")); got != "short" {
		t.Errorf("short snippet = %q, want trimmed", got)
	}
}
