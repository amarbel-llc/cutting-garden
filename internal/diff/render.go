package diff

import (
	"os"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// newDiffRenderer builds a lipgloss.Renderer keyed off the -color
// flag value (FDR §Flags). "auto" lets lipgloss/termenv auto-detect
// from stdout (TTY check, NO_COLOR env, COLORTERM, etc.); "always"
// forces ANSI 16-color (enough for the diff-marker palette);
// "never" forces Ascii so styles render their input unchanged.
//
// Run validates the -color value before calling, so the default
// arm is defense-in-depth — it shouldn't fire in practice.
func newDiffRenderer(mode string, stdout *os.File) (*lipgloss.Renderer, error) {
	r := lipgloss.NewRenderer(stdout)
	switch mode {
	case colorAlways:
		r.SetColorProfile(termenv.ANSI)
	case colorNever:
		r.SetColorProfile(termenv.Ascii)
	case "", colorAuto:
		// NewRenderer already auto-detected the profile from stdout.
	default:
		return nil, errors.ErrorWithStackf(
			"invalid -color value %q; expected auto, always, or never",
			mode,
		)
	}
	return r, nil
}

// renderDiffLine paints a per-marker color over the entire line.
// Returns the line unchanged when its leading character isn't one
// of A/D/M/T/B (defensive — keeps the function total).
//
// Color scheme is fixed (FDR §Flags):
//
//	A → green       (added on disk)
//	D → red         (deleted on disk)
//	M → yellow      (modified)
//	T → magenta     (type-changed)
//	B → bright red  (receipt blob missing in source store)
//
// TabWidth(lipgloss.NoTabConversion) preserves the literal tab
// between the path and the per-attribute detail; lipgloss otherwise
// substitutes spaces.
func renderDiffLine(r *lipgloss.Renderer, line string) string {
	if len(line) == 0 {
		return line
	}
	var color lipgloss.Color
	switch line[0] {
	case 'A':
		color = lipgloss.Color("2")
	case 'D':
		color = lipgloss.Color("1")
	case 'M':
		color = lipgloss.Color("3")
	case 'T':
		color = lipgloss.Color("5")
	case 'B':
		color = lipgloss.Color("9")
	default:
		return line
	}
	return r.NewStyle().
		Foreground(color).
		TabWidth(lipgloss.NoTabConversion).
		Render(line)
}
