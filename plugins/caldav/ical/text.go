package ical

import "strings"

// RFC 5545 §3.3.11 TEXT escaping. The escaping is WIRE-FORMAT ONLY: parsed
// struct fields (Summary, Description, Location, each Categories element) hold
// the unescaped value, and the serializers re-escape on write — so a stored
// `SUMMARY:Plan\, then do` is the Go string "Plan, then do" everywhere above
// this package (the 2026-09-02 UAT ruling: commas never require escaping in
// organize trailers or anywhere else consumer-facing). Escaped `\n` decodes to
// a REAL newline in the struct; single-line consumers (organize's trailer and
// box atoms) collapse newlines at their presentation layer, not here — see
// internal/organize collapseToSingleLine.
//
// Non-TEXT properties (UID, DTSTAMP, STATUS, PRIORITY, DUE/DTSTART/DTEND,
// RRULE, GEO, SEQUENCE, …) are never escaped. VALARM subcomponent properties
// (including the alarm DESCRIPTION) stay an opaque verbatim pass-through —
// they round-trip byte-identically and nothing above the ical layer consumes
// them as text.

// escapeText encodes a Go string as an RFC 5545 TEXT property value:
// `\` → `\\`, `;` → `\;`, `,` → `\,`, newline → `\n`. CRLF / bare CR are
// normalized to LF first so a value injected through a JSON patch cannot smuggle
// a raw CR into the serialized line.
func escapeText(s string) string {
	if !strings.ContainsAny(s, "\\;,\n\r") {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			b.WriteString(`\\`)
		case ';':
			b.WriteString(`\;`)
		case ',':
			b.WriteString(`\,`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// unescapeText decodes an RFC 5545 TEXT property value: `\\` → `\`, `\;` → `;`,
// `\,` → `,`, `\n` / `\N` → newline. An escape sequence outside that set (or a
// trailing lone backslash) is kept verbatim — lenient toward the malformed
// producers real CalDAV data comes from, and it keeps unescape(x) lossless for
// any x escapeText produces.
func unescapeText(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 == len(s) {
			b.WriteByte(c)
			continue
		}
		i++
		switch s[i] {
		case '\\', ';', ',':
			b.WriteByte(s[i])
		case 'n', 'N':
			b.WriteByte('\n')
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// parseCategories splits a CATEGORIES property value into its category list.
// The wire value is a comma-SEPARATED list where `\,` is a literal comma INSIDE
// one category, so the split is escape-aware: only unescaped commas separate,
// then each element is trimmed and unescaped — `planning\, misc` is ONE
// category "planning, misc", never two.
func parseCategories(value string) []string {
	var cats []string
	start := 0
	escaped := false
	for i := 0; i < len(value); i++ {
		switch {
		case escaped:
			escaped = false
		case value[i] == '\\':
			escaped = true
		case value[i] == ',':
			cats = append(cats, unescapeText(strings.TrimSpace(value[start:i])))
			start = i + 1
		}
	}
	return append(cats, unescapeText(strings.TrimSpace(value[start:])))
}

// formatCategories serializes a category list to the CATEGORIES wire value:
// each element TEXT-escaped (so a literal comma inside a category becomes
// `\,`), then comma-joined.
func formatCategories(cats []string) string {
	escaped := make([]string, len(cats))
	for i, c := range cats {
		escaped[i] = escapeText(c)
	}
	return strings.Join(escaped, ",")
}

// writeTextProp emits a TEXT-typed property with RFC 5545 §3.3.11 escaping
// applied to the value — the write-side twin of the parsers' unescapeText.
// Only SUMMARY / DESCRIPTION / LOCATION go through this; date, integer, and
// enum properties use writeIcalProp directly (their values never escape).
func writeTextProp(b *strings.Builder, name, value string) {
	writeIcalProp(b, name, escapeText(value))
}
