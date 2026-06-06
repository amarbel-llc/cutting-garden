package capture

import (
	"os"
	"strings"

	"github.com/mattn/go-isatty"
)

// progressActive reports whether the live viewport should run. Mirrors the
// -color auto/always/never contract but keys on stderr (where the TUI
// renders) rather than stdout. The auto branch additionally honors NO_COLOR
// because the viewport is styled output.
func progressActive(mode string, stderr *os.File) bool {
	switch mode {
	case progressNever:
		return false
	case progressAlways:
		return true
	default: // auto
		if os.Getenv("NO_COLOR") != "" {
			return false
		}
		fd := stderr.Fd()
		return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
	}
}

// captureLabel builds a short human title for the viewport header. It picks
// the first non-flag positional arg ("capture ./src"); with no positional
// args (the implicit-"." capture) it returns just "capture".
func captureLabel(args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return "capture " + arg
	}
	return "capture"
}
