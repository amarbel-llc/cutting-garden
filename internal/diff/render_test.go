package diff

import (
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestNewDiffRenderer_AlwaysForcesANSI asserts -color=always sets
// the ANSI profile regardless of what lipgloss auto-detected.
func TestNewDiffRenderer_AlwaysForcesANSI(t *testing.T) {
	r, err := newDiffRenderer(colorAlways, os.Stdout)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := r.ColorProfile(); got != termenv.ANSI {
		t.Errorf("expected ANSI profile, got %v", got)
	}
}

// TestNewDiffRenderer_NeverForcesAscii asserts -color=never sets the
// Ascii (no-color) profile.
func TestNewDiffRenderer_NeverForcesAscii(t *testing.T) {
	r, err := newDiffRenderer(colorNever, os.Stdout)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := r.ColorProfile(); got != termenv.Ascii {
		t.Errorf("expected Ascii profile, got %v", got)
	}
}

// TestNewDiffRenderer_AutoPreservesDetection asserts -color=auto
// leaves whatever profile lipgloss.NewRenderer auto-detected. Test
// only verifies that no error is returned; the exact profile depends
// on whether stdout is a TTY in the test process.
func TestNewDiffRenderer_AutoPreservesDetection(t *testing.T) {
	if _, err := newDiffRenderer(colorAuto, os.Stdout); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestNewDiffRenderer_InvalidValue_Errors asserts the defense-in-depth
// branch fires when -color isn't validated by Run (which it always
// is in production). Run's validation is the primary gate; this
// covers the renderer-side return path.
func TestNewDiffRenderer_InvalidValue_Errors(t *testing.T) {
	_, err := newDiffRenderer("bogus", os.Stdout)
	if err == nil {
		t.Fatal("expected error for bogus -color value, got nil")
	}
	if !strings.Contains(err.Error(), "auto, always, or never") {
		t.Errorf("expected expected-values list in error; got: %v", err)
	}
}

// ---------------------------------------------------------------------
// renderDiffLine: per-marker color selection
// ---------------------------------------------------------------------

// renderAscii returns a renderer at the Ascii (no-color) profile so
// renderDiffLine's output equals its input — useful for asserting the
// marker-dispatch logic without parsing escape codes.
func renderAscii(t *testing.T) *lipgloss.Renderer {
	t.Helper()
	r, err := newDiffRenderer(colorNever, os.Stdout)
	if err != nil {
		t.Fatalf("newDiffRenderer(never): %v", err)
	}
	return r
}

func TestRenderDiffLine_AsciiPreservesInput(t *testing.T) {
	r := renderAscii(t)
	cases := []string{
		"A  extra.txt\tfile",
		"D  missing.txt\tfile",
		"M  x\tmode 0644 -> 0755",
		"T  x\tfile -> symlink",
		"B  x\tblob blake2b256-x missing in source store",
	}
	for _, line := range cases {
		t.Run(line, func(t *testing.T) {
			if got := renderDiffLine(r, line); got != line {
				t.Errorf("ascii profile should pass through unchanged.\ngot:  %q\nwant: %q", got, line)
			}
		})
	}
}

func TestRenderDiffLine_EmptyAndUnknownMarkers_PassThrough(t *testing.T) {
	r := renderAscii(t)
	cases := []string{
		"",
		"X  unknown marker",
		"diff: 3 differences", // not a diff line — passes through
	}
	for _, line := range cases {
		t.Run(line, func(t *testing.T) {
			if got := renderDiffLine(r, line); got != line {
				t.Errorf("unexpected change: got %q, want %q", got, line)
			}
		})
	}
}

// TestRenderDiffLine_ANSIWrapsInSGR asserts the ANSI profile wraps
// each known-marker line in an SGR escape sequence. The payload
// (after the SGR prefix and before the reset) must contain the
// original line content unchanged.
func TestRenderDiffLine_ANSIWrapsInSGR(t *testing.T) {
	r, err := newDiffRenderer(colorAlways, os.Stdout)
	if err != nil {
		t.Fatalf("newDiffRenderer(always): %v", err)
	}
	cases := map[byte]string{
		'A': "A  extra.txt\tfile",
		'D': "D  missing.txt\tfile",
		'M': "M  x\tmode 0644 -> 0755",
		'T': "T  x\tfile -> symlink",
		'B': "B  x\tblob blake2b256-x missing in source store",
	}
	for marker, line := range cases {
		t.Run(string(marker), func(t *testing.T) {
			got := renderDiffLine(r, line)
			if !strings.Contains(got, "\x1b[") {
				t.Errorf("expected SGR escape in output, got %q", got)
			}
			if !strings.Contains(got, line) {
				t.Errorf("expected original line content in output, got %q", got)
			}
		})
	}
}
