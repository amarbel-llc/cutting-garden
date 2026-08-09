package organize

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/hyphence/go/hyphence"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The organize document (RFC 0015, redesigned 2026-08) is a single hyphence
// document — the exact bytes the end-user is presented and edits:
//
//	---
//	% generated: cg organize -group-by status caldav:https://…/cal/
//	- _base = @blake2b256-<digest of this doc with the _base line excised>
//	- _anchor = caldav:https://…/cal/
//	- _type = !caldav-object-v1            (spelling 2 only)
//	! organize-base-v1
//	---
//
//	# status=                              (spelling 2: dimension at depth 1)
//
//	## =NEEDS-ACTION
//
//	## =COMPLETED
//	- [task1.ics] Buy milk
//
// The `---`-fenced block is a hyphence envelope (hyphence.Boundary): a `%`
// provenance comment, the framework `-` fields (`_base` pin, `_anchor` for
// re-query + relative-id resolution, optional `_query`, and `_type` for the
// envelope type spelling), and the `! organize-base-v1` type. The body is a
// heading ladder of arbitrary depth; an object's effective terms are the union
// of its heading path (RFC 0015 laddering). Two spellings of the object type:
//
//   - SPELLING 1 (type as heading): a leading `# !<type>` scopes objects, then
//     `## <dim>=` → `### =<value>`; object boxes carry inline `!type`.
//   - SPELLING 2 (type in envelope): `- _type = !<type>` distributes the type to
//     every object; object boxes drop `!type`; the ladder is one level shallower.
//
// Object lines are espalier boxes `- [<id> !<type>] <desc>` with anchor-relative
// short ids; a bare heading term (`# !type`) drops the brackets a box carries.
// The parser accepts either spelling and any depth; render emits spelling 2 for a
// single-type node set (flatter), spelling 1 for a multi-type set.
const (
	envelopeType = "organize-base-v1"
	fieldBase    = "_base"
	fieldAnchor  = "_anchor"
	fieldQuery   = "_query"
	fieldType    = "_type"
)

// objectLine is one espalier box literal: the anchor-relative node id, its type
// tag (inline for spelling 1; empty for spelling 2, where the envelope carries
// it), the plugin-presented detail atoms (date/time/location; FDR 0023,
// cutting-garden#47), and the description trailer.
type objectLine struct {
	ID     string
	Type   string
	Fields []cgp.BoxAtom
	Desc   string
}

// section is one heading and the object lines directly beneath it (before any
// deeper heading). Depth is the `#` count; Term is the heading text — a type term
// `!<type>`, a dimension partial-term `<dim>=`, or a dependent value `=<value>`.
// Nesting is implicit in the flat order + Depth (a deeper section that follows is
// a child).
type section struct {
	Depth int
	Term  string
	Lines []objectLine
}

// document is a parsed or about-to-be-rendered organize document.
type document struct {
	// Provenance is the `%` comment (without the leading "% ").
	Provenance string
	// BaseDigest is the bare digest of the `- _base = @<digest>` pin; empty in the
	// canonical base form (the stored blob excises the pin).
	BaseDigest string
	// Anchor is `- _anchor`: the plugin URI apply re-resolves the live state from
	// and against which relative object ids resolve.
	Anchor string
	// Query is `- _query`: the trellis query that selected the nodes (optional).
	Query string
	// Type is `- _type` (spelling 2): the type distributed to every object; empty
	// under spelling 1, where the type is a `# !<type>` heading instead.
	Type string
	// Distributed carries any other bare `- <term>` envelope lines the user
	// hand-added (RFC 0015 distribution rule) — preserved verbatim on parse.
	Distributed []string
	// Ungrouped are objects above the first heading (spelling 2's type-less set).
	Ungrouped []objectLine
	// Sections is the heading ladder in render order.
	Sections []section
}

// --- term classification -----------------------------------------------------

func isValueTerm(t string) bool { return strings.HasPrefix(t, "=") }

// isDimTerm reports a dimension partial-term like `status=` (ends with `=`, does
// not start with one).
func isDimTerm(t string) bool {
	return strings.HasSuffix(t, "=") && !strings.HasPrefix(t, "=")
}

func dimName(t string) string   { return strings.TrimSuffix(t, "=") }
func valueName(t string) string { return strings.TrimPrefix(t, "=") }

// --- render ------------------------------------------------------------------

// render serializes the full document: the hyphence envelope (with the `_base`
// pin when set) and the heading-ladder body.
func render(doc document) string {
	var b strings.Builder
	b.WriteString(hyphence.Boundary + "\n")
	if doc.Provenance != "" {
		fmt.Fprintf(&b, "%% %s\n", doc.Provenance)
	}
	if doc.BaseDigest != "" {
		fmt.Fprintf(&b, "- %s = @%s\n", fieldBase, doc.BaseDigest)
	}
	if doc.Anchor != "" {
		fmt.Fprintf(&b, "- %s = %s\n", fieldAnchor, doc.Anchor)
	}
	if doc.Query != "" {
		fmt.Fprintf(&b, "- %s = %s\n", fieldQuery, doc.Query)
	}
	if doc.Type != "" {
		fmt.Fprintf(&b, "- %s = !%s\n", fieldType, doc.Type)
	}
	for _, d := range doc.Distributed {
		fmt.Fprintf(&b, "- %s\n", d)
	}
	fmt.Fprintf(&b, "! %s\n", envelopeType)
	b.WriteString(hyphence.Boundary + "\n")

	writeBody(&b, doc)
	return b.String()
}

// renderCanonical renders the base form (no `_base` pin): the exact bytes hashed
// and stored as the organize-base-v1 blob. The emitted document is this plus the
// `- _base` line, so the pin names a digest computed without it (RFC 0015 §250).
func renderCanonical(doc document) string {
	doc.BaseDigest = ""
	return render(doc)
}

// writeBody emits the heading-ladder body: the ungrouped lines, then each section
// as its heading, one blank line, and its object lines run together. Blank lines
// separate headings and groups of objects — never individual objects.
func writeBody(b *strings.Builder, doc document) {
	if len(doc.Ungrouped) > 0 {
		b.WriteByte('\n')
		for _, ln := range doc.Ungrouped {
			writeObjectLine(b, ln)
		}
	}
	for _, s := range doc.Sections {
		b.WriteByte('\n')
		fmt.Fprintf(b, "%s %s\n", strings.Repeat("#", s.Depth), s.Term)
		if len(s.Lines) > 0 {
			b.WriteByte('\n')
		}
		for _, ln := range s.Lines {
			writeObjectLine(b, ln)
		}
	}
}

// writeObjectLine renders one espalier box literal and its description trailer:
// `- [<id> !<type> <name>=<value>…] <desc>`. The detail atoms follow the id/type
// inside the box, each a ground `name=value` espalier field (cutting-garden#47).
func writeObjectLine(b *strings.Builder, ln objectLine) {
	b.WriteString("- [")
	b.WriteString(ln.ID)
	if ln.Type != "" {
		fmt.Fprintf(b, " !%s", ln.Type)
	}
	for _, f := range ln.Fields {
		fmt.Fprintf(b, " %s=%s", f.Name, f.Value)
	}
	b.WriteByte(']')
	if ln.Desc != "" {
		fmt.Fprintf(b, " %s", ln.Desc)
	}
	b.WriteByte('\n')
}

// --- parse -------------------------------------------------------------------

// parseDocument parses the emitted form or the canonical base body back into a
// document. The envelope is parsed by hyphence (robust `---` fence + prefix
// handling); the body (heading ladder) is organize's own line grammar.
func parseDocument(text string) (document, error) {
	fields, provenance, distributed, body, err := splitEnvelope(text)
	if err != nil {
		return document{}, err
	}

	var doc document
	doc.Provenance = provenance
	doc.Distributed = distributed
	doc.BaseDigest = strings.TrimPrefix(fields[fieldBase], "@")
	doc.Anchor = fields[fieldAnchor]
	doc.Query = fields[fieldQuery]
	doc.Type = strings.TrimPrefix(fields[fieldType], "!")

	if err := parseBody(&doc, body); err != nil {
		return document{}, err
	}
	return doc, nil
}

// splitEnvelope parses the hyphence `---` envelope, returning the `-` fields
// (keyed, `key = value`), any bare distributing `-` terms, the joined `%`
// provenance comments, and the raw body text.
func splitEnvelope(text string) (
	fields map[string]string, provenance string, distributed []string, body string, err error,
) {
	doc := &hyphence.Document{}
	var bodyBuf bytes.Buffer
	reader := hyphence.Reader{
		RequireMetadata: true,
		Metadata:        &hyphence.MetadataBuilder{Doc: doc},
		Blob:            &hyphence.BodyStreamer{W: &bodyBuf},
	}
	if _, err = reader.ReadFrom(strings.NewReader(text)); err != nil {
		return nil, "", nil, "", errors.Wrapf(err, "organize: parse hyphence envelope")
	}

	fields = map[string]string{}
	var comments []string
	for _, ml := range doc.Metadata {
		comments = append(comments, ml.LeadingComments...)
		switch ml.Prefix {
		case '-':
			if key, val, has := strings.Cut(ml.Value, "="); has {
				fields[strings.TrimSpace(key)] = strings.TrimSpace(val)
			} else {
				distributed = append(distributed, strings.TrimSpace(ml.Value))
			}
		case '!':
			// The envelope's own type (organize-base-v1); nothing to recover.
		}
	}
	comments = append(comments, doc.TrailingComments...)
	provenance = strings.Join(comments, "\n")
	return fields, provenance, distributed, bodyBuf.String(), nil
}

// parseBody walks the heading-ladder body, attaching object lines to their
// enclosing section (or the ungrouped set before any heading).
func parseBody(doc *document, body string) error {
	curSection := -1
	for i, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		switch {
		case strings.HasPrefix(line, "#"):
			depth := 0
			for depth < len(line) && line[depth] == '#' {
				depth++
			}
			term := strings.TrimSpace(line[depth:])
			doc.Sections = append(doc.Sections, section{Depth: depth, Term: term})
			curSection = len(doc.Sections) - 1

		case strings.HasPrefix(line, "-"):
			ln, err := parseObjectLine(strings.TrimSpace(line[1:]))
			if err != nil {
				return errors.BadRequestf("organize: body line %d: %s", i+1, err)
			}
			if curSection >= 0 {
				doc.Sections[curSection].Lines = append(doc.Sections[curSection].Lines, ln)
			} else {
				doc.Ungrouped = append(doc.Ungrouped, ln)
			}

		default:
			return errors.BadRequestf("organize: body line %d: unrecognized %q", i+1, line)
		}
	}
	return nil
}

// parseObjectLine parses one espalier box literal `[<id> !<type> …] <desc>`.
func parseObjectLine(rest string) (objectLine, error) {
	if !strings.HasPrefix(rest, "[") {
		return objectLine{}, fmt.Errorf("object line is not an espalier box: %q", rest)
	}
	end := strings.IndexByte(rest, ']')
	if end < 0 {
		return objectLine{}, fmt.Errorf("object line box is unterminated: %q", rest)
	}
	interior := strings.Fields(rest[1:end])
	if len(interior) == 0 {
		return objectLine{}, fmt.Errorf("object line box is empty: %q", rest)
	}
	ln := objectLine{ID: interior[0], Desc: strings.TrimSpace(rest[end+1:])}
	for _, tok := range interior[1:] {
		if strings.HasPrefix(tok, "!") {
			ln.Type = strings.TrimPrefix(tok, "!")
			continue
		}
		// A ground `name=value` espalier field: the plugin-presented detail
		// atoms (date/time/location; cutting-garden#47). Captured so they
		// round-trip; splitting on the FIRST '=' keeps any '=' in the value.
		if name, value, ok := strings.Cut(tok, "="); ok && name != "" {
			ln.Fields = append(ln.Fields, cgp.BoxAtom{Name: name, Value: value})
		}
	}
	return ln, nil
}

// --- derived views -----------------------------------------------------------

// objectLines returns every object line in the document — the ungrouped set
// followed by each section's lines, in document order.
func (doc document) objectLines() []objectLine {
	lines := make([]objectLine, 0, len(doc.Ungrouped))
	lines = append(lines, doc.Ungrouped...)
	for _, s := range doc.Sections {
		lines = append(lines, s.Lines...)
	}
	return lines
}

// groupedDimension returns the grouped facet dimension — the `<dim>=` heading
// term (RFC 0015: the dimension is the heading, not a separate field). Empty when
// the document has no dimension heading.
func (doc document) groupedDimension() string {
	for _, s := range doc.Sections {
		if isDimTerm(s.Term) {
			return dimName(s.Term)
		}
	}
	return ""
}

// assignments projects the document into a box-id → bucket-value map: each
// object's value for the grouped dimension, read from the deepest `=<value>`
// heading in its path (empty when the object sits only under the type/ungrouped).
// Keys are the box ids as written; the apply engine re-derives the same key from
// each live node's URI via relativeID. An object under two positions is a
// malformed edit and rejects loudly.
func (doc document) assignments() (map[string]string, error) {
	out := make(map[string]string)
	place := func(ln objectLine, value string) error {
		if prev, dup := out[ln.ID]; dup {
			return errors.BadRequestf(
				"organize: object %s appears twice (buckets %q and %q)", ln.ID, prev, value,
			)
		}
		out[ln.ID] = value
		return nil
	}

	for _, ln := range doc.Ungrouped {
		if err := place(ln, ""); err != nil {
			return nil, err
		}
	}

	// Walk the flat sections as a depth stack, so each object's value is the
	// value term on its enclosing heading path.
	var stack []section
	for _, s := range doc.Sections {
		for len(stack) > 0 && stack[len(stack)-1].Depth >= s.Depth {
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, s)
		value := ""
		for _, anc := range stack {
			if isValueTerm(anc.Term) {
				value = valueName(anc.Term)
			}
		}
		for _, ln := range s.Lines {
			if err := place(ln, value); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// --- id resolution -----------------------------------------------------------

// relativeID renders a node URI relative to the anchor when it sits under it (the
// short `task1.ics` form), else the full URI. Comparison is form-independent: it
// matches on host+path so an anchor spelled `caldav:https://host/cal/` shortens a
// node URI spelled `caldav://host/cal/x.ics` (a real caldav divergence — the
// plugin normalizes node URIs but not the anchor arg). Deterministic on
// (uri, anchor), which lets the apply engine re-derive a stored box id from a live
// node URI and match it.
func relativeID(uriStr, anchorStr string) string {
	u, err1 := url.Parse(uriStr)
	a, err2 := url.Parse(anchorStr)
	if err1 == nil && err2 == nil {
		up, ap := canonicalHostPath(u), canonicalHostPath(a)
		if ap != "" && strings.HasPrefix(up, ap) {
			return strings.TrimPrefix(up, ap)
		}
	}
	return uriStr
}

// canonicalHostPath projects a URL to a scheme-form-independent "host/path" key,
// handling the caldav opaque spelling (`caldav:https://host/path`) as well as the
// plain hierarchical form (`caldav://host/path`).
func canonicalHostPath(u *url.URL) string {
	if u.Opaque != "" {
		s := u.Opaque
		s = strings.TrimPrefix(s, "https://")
		s = strings.TrimPrefix(s, "http://")
		return s
	}
	return u.Host + u.Path
}
