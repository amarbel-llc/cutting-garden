package capture

import (
	"bytes"
	"os"
	"strings"
	"sync"

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

// reporterLineWriter is an io.Writer that turns line-oriented stderr
// chatter (madder's blob-store SFTP dial / host-key / remote-config
// lines, prefixed "# (blob_store: ...) ") into Reporter.Log lines so
// the -progress viewport's tail shows them instead of raw stderr
// writes fracturing the bubbletea render. Same splitting semantics as
// the git plugin's progressLogWriter: each '\r'- or '\n'-delimited
// segment is trimmed and, if non-empty, flushed to log; a trailing
// partial segment stays buffered for the next Write.
//
// Write is mutex-guarded: the blob store may print from its own
// goroutines, and while the Reporter contract allows concurrent use,
// the internal buffer needs serializing.
type reporterLineWriter struct {
	mu  sync.Mutex
	log func(string)
	buf []byte
}

func (w *reporterLineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexAny(w.buf, "\r\n")
		if i < 0 {
			break
		}
		segment := strings.TrimSpace(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
		if segment != "" {
			w.log(segment)
		}
	}
	return len(p), nil
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
