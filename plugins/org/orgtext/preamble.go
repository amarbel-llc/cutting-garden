package orgtext

import "strings"

// Preamble is a file's text before its first headline, split in two. The
// file's own settings — #+TODO, #+SEQ_TODO, #+TYP_TODO and #+FILETAGS lines
// outside a block — change how the rest of the file reads, so they are kept
// apart and survive an edit of the text. Everything else (comments, #+TITLE
// and other keywords, free text, blank lines) is Body.
type Preamble struct {
	// Settings are the setting lines, in file order, without line endings.
	Settings []string
	// Body is the remaining lines joined by "\n" with the last line's
	// terminator dropped, so "a\n" is the lines "a" and "". Line endings
	// read as "\n".
	Body string
}

// Preamble splits the text before the first headline.
func (f *File) Preamble() Preamble {
	lines := splitLines(f.src)
	inBlock := blockLines(lines)
	var p Preamble
	var rest []string
	for i, l := range lines {
		if l.start >= f.PreambleSpan.End {
			break
		}
		if !inBlock[i] && isFileSetting(l.text) {
			p.Settings = append(p.Settings, l.text)
			continue
		}
		rest = append(rest, l.text)
	}
	p.Body = strings.Join(rest, "\n")
	return p
}

func isFileSetting(text string) bool {
	m := settingRe.FindStringSubmatch(text)
	if m == nil {
		return false
	}
	switch strings.ToUpper(m[1]) {
	case "TODO", "SEQ_TODO", "TYP_TODO", "FILETAGS":
		return true
	}
	return false
}

// Render writes the preamble back as file text: the settings, then the body,
// every line ended by nl. An empty body adds no line. It is the inverse of
// Preamble for a body Preamble produced.
func (p Preamble) Render(nl string) string {
	var b strings.Builder
	for _, l := range p.Settings {
		b.WriteString(l + nl)
	}
	if p.Body != "" {
		for _, l := range strings.Split(strings.ReplaceAll(p.Body, "\r\n", "\n"), "\n") {
			b.WriteString(l + nl)
		}
	}
	return b.String()
}

// LineEnding is the file's line ending: "\r\n" when its first line ends so,
// otherwise "\n".
func LineEnding(src []byte) string {
	for i, c := range src {
		if c == '\n' {
			if i > 0 && src[i-1] == '\r' {
				return "\r\n"
			}
			return "\n"
		}
	}
	return "\n"
}
