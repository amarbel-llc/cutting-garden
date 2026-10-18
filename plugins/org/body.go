package org

import (
	"regexp"
	"slices"
	"strings"

	"code.linenisgreat.com/cutting-garden/plugins/org/orgtext"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Body edits (FDR 0026 D7, option 1): the `body` patch key replaces a
// headline's own text — what read_node returns as `body` — and nothing it
// did not show. Drawers in the body (:LOGBOOK: and the like) are kept byte
// for byte, gathered above the text where Orgzly writes them; the planning
// line, the :PROPERTIES: drawer and the children are untouched. The text is
// written with the indentation the old text had, and must read back
// exactly as sent: a line that would become a headline, a drawer, or a
// file setting is refused.

const fieldPatchBody = "body"

var bodyDrawerRe = regexp.MustCompile(`^[ \t]*:([A-Za-z0-9_-]+):[ \t]*$`)

// normalizeBody is the form read_node returns a body in: lines without
// "\r", blank edges trimmed, common indentation removed.
func normalizeBody(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	indent := commonIndent(lines)
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			lines[i] = ""
		} else {
			lines[i] = l[len(indent):]
		}
	}
	return strings.Join(lines, "\n")
}

// commonIndent is the shortest leading whitespace of the non-blank lines.
func commonIndent(lines []string) string {
	indent, found := "", false
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		lead := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		if !found || len(lead) < len(indent) {
			indent, found = lead, true
		}
	}
	return indent
}

// findHeadlineByID returns the first headline holding id, the one its
// ?id= URI addresses.
func findHeadlineByID(hs []*orgtext.Headline, id string) *orgtext.Headline {
	for _, h := range hs {
		if h.ID() == id {
			return h
		}
		if found := findHeadlineByID(h.Children, id); found != nil {
			return found
		}
	}
	return nil
}

func countHeadlines(hs []*orgtext.Headline) int {
	n := len(hs)
	for _, h := range hs {
		n += countHeadlines(h.Children)
	}
	return n
}

// spliceBody replaces the own text of the headline holding id with text,
// keeping the body's drawers and trailing blank lines, and checks the
// result reads back as sent.
func spliceBody(src []byte, kws orgtext.Keywords, id, text, nl string) ([]byte, error) {
	before := orgtext.Parse(src, orgtext.Options{Keywords: kws})
	h := findHeadlineByID(before.Headlines, id)
	if h == nil {
		return nil, errors.ErrorWithStackf("org plugin: headline %s vanished while editing its body", id)
	}

	region := strings.ReplaceAll(string(src[h.BodySpan.Start:h.BodySpan.End]), "\r\n", "\n")
	lines := strings.Split(strings.TrimSuffix(region, "\n"), "\n")
	if region == "" {
		lines = nil
	}

	var drawers, textLines []string
	for i := 0; i < len(lines); i++ {
		if m := bodyDrawerRe.FindStringSubmatch(lines[i]); m != nil && !strings.EqualFold(m[1], "END") {
			if end := slices.IndexFunc(lines[i+1:], func(l string) bool {
				return strings.EqualFold(strings.TrimSpace(l), ":END:")
			}); end >= 0 {
				drawers = append(drawers, lines[i:i+1+end+1]...)
				i += end + 1
				continue
			}
		}
		textLines = append(textLines, lines[i])
	}
	trailingBlank := 0
	for i := len(textLines) - 1; i >= 0 && strings.TrimSpace(textLines[i]) == ""; i-- {
		trailingBlank++
	}
	indent := commonIndent(textLines)

	var b strings.Builder
	for _, l := range drawers {
		b.WriteString(l + nl)
	}
	want := normalizeBody(text)
	if want != "" {
		for _, l := range strings.Split(want, "\n") {
			if l != "" {
				l = indent + l
			}
			b.WriteString(l + nl)
		}
	}
	for range trailingBlank {
		b.WriteString(nl)
	}

	out := append([]byte(nil), src[:h.BodySpan.Start]...)
	out = append(out, b.String()...)
	out = append(out, src[h.BodySpan.End:]...)

	after := orgtext.Parse(out, orgtext.Options{Keywords: kws})
	if countHeadlines(after.Headlines) != countHeadlines(before.Headlines) {
		return nil, errors.BadRequestf(
			"org plugin: the body would create a headline: a line beginning with `*` and a space " +
				"starts one in org; indent it or reword it")
	}
	if !slices.Equal(after.FileTags, before.FileTags) ||
		!slices.Equal(after.Keywords.Active, before.Keywords.Active) ||
		!slices.Equal(after.Keywords.Done, before.Keywords.Done) {
		return nil, errors.BadRequestf(
			"org plugin: the body would change the file's settings (#+TODO or #+FILETAGS); " +
				"a body cannot hold those lines")
	}
	if got := findHeadlineByID(after.Headlines, id); got == nil || got.Body != want {
		return nil, errors.BadRequestf(
			"org plugin: the body would not read back as written; a body cannot hold a drawer " +
				"(`:NAME:` … `:END:`) or a line org reads as planning or properties")
	}
	return out, nil
}
