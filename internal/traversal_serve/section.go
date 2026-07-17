package traversal_serve

import (
	"strings"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// SectionTOML extracts one named top-level section from the raw config
// file bytes, WRAPPER-STRIPPED per RFC 0013 §initialize: the returned
// text's keys are section-relative — `[fj]` scalars become top-level
// keys, `[[fj.roots]]` becomes `[[roots]]`, `[fj.sub.x]` becomes
// `[sub.x]` — so the plugin never sees its own section name, mirroring
// what the in-process config decoder hands a linked plugin. An absent
// section returns ("", nil).
//
// The extraction is line-based over TOML's header grammar (headers are
// single physical lines), with quote-aware scanning so quoted keys
// containing '.', ']', or '#' cannot confuse it. section MUST be a bare
// TOML key (PluginStanza.Validate enforces the same grammar); a
// top-level `[[section]]` array-of-tables cannot be represented
// wrapper-stripped and is rejected — a plugin wanting repeated tables
// uses a sub-array (`[[section.roots]]`).
func SectionTOML(raw []byte, section string) (string, error) {
	if !sectionNamePattern.MatchString(section) {
		return "", errors.BadRequestf(
			"config section %q is not a bare TOML key", section,
		)
	}

	var out strings.Builder
	found := false
	inSection := false

	for _, line := range strings.SplitAfter(string(raw), "\n") {
		if line == "" {
			continue
		}

		header, ok := parseHeaderLine(line)
		if !ok {
			if inSection {
				out.WriteString(line)
			}
			continue
		}

		if header.firstSegment != section {
			inSection = false
			continue
		}

		found = true
		inSection = true

		if header.rest == "" {
			if header.array {
				return "", errors.BadRequestf(
					"config section %q is a top-level array of tables"+
						" ([[%s]]), which cannot be passed"+
						" wrapper-stripped — use a sub-array"+
						" ([[%s.<key>]]) instead",
					section, section, section,
				)
			}
			// The bare [section] header itself: its keys become
			// top-level keys, so the header line is dropped.
			continue
		}

		openTok, closeTok := "[", "]"
		if header.array {
			openTok, closeTok = "[[", "]]"
		}
		out.WriteString(openTok + header.rest + closeTok + header.suffix)
	}

	if !found {
		return "", nil
	}

	return out.String(), nil
}

// tomlHeader is one parsed `[key.path]` / `[[key.path]]` line.
type tomlHeader struct {
	// array is true for an array-of-tables header ([[...]]).
	array bool
	// firstSegment is the first key-path segment, unquoted.
	firstSegment string
	// rest is the raw header content after the first segment's dot,
	// verbatim (quoting preserved); empty when the path is exactly one
	// segment.
	rest string
	// suffix is everything after the closing bracket(s) — trailing
	// whitespace, an inline comment, and the newline — verbatim.
	suffix string
}

// parseHeaderLine classifies one physical line as a TOML table header,
// returning ok=false for anything else (key/value lines, comments,
// blanks, and malformed headers — the latter are the TOML parser's
// problem, not the extractor's).
func parseHeaderLine(line string) (tomlHeader, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, "[") {
		return tomlHeader{}, false
	}

	var h tomlHeader
	content := trimmed[1:]
	if strings.HasPrefix(content, "[") {
		h.array = true
		content = content[1:]
	}

	closeIdx := scanUnquoted(content, ']')
	if closeIdx < 0 {
		return tomlHeader{}, false
	}
	inner := content[:closeIdx]
	after := content[closeIdx+1:]
	if h.array {
		if !strings.HasPrefix(after, "]") {
			return tomlHeader{}, false
		}
		after = after[1:]
	}
	h.suffix = after

	dotIdx := scanUnquoted(inner, '.')
	var first string
	if dotIdx < 0 {
		first, h.rest = inner, ""
	} else {
		first, h.rest = inner[:dotIdx], strings.TrimLeft(
			inner[dotIdx+1:], " \t",
		)
	}

	first = strings.TrimSpace(first)
	first = strings.TrimSuffix(strings.TrimPrefix(first, `"`), `"`)
	h.firstSegment = first

	return h, true
}

// scanUnquoted returns the index of the first occurrence of target in s
// that lies outside single- or double-quoted runs, or -1. Escapes
// inside basic strings are honored for `\"`.
func scanUnquoted(s string, target byte) int {
	var inBasic, inLiteral, escaped bool
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case escaped:
			escaped = false
		case inBasic && c == '\\':
			escaped = true
		case inBasic && c == '"':
			inBasic = false
		case inLiteral && c == '\'':
			inLiteral = false
		case !inBasic && !inLiteral && c == '"':
			inBasic = true
		case !inBasic && !inLiteral && c == '\'':
			inLiteral = true
		case !inBasic && !inLiteral && c == target:
			return i
		}
	}
	return -1
}
