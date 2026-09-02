package organize

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/hyphence/go/hyphence"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The organize document (RFC 0015, redesigned 2026-08) is a single hyphence
// document — the exact bytes the end-user is presented and edits:
//
//	---
//	% generated: `cg organize -group-by status= caldav:https://…/cal/`
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
// The parser accepts either spelling and any depth (normalized to the shallowest
// level present, with empty headings as context resets — see parseBody); render
// emits spelling 2 for a single-type node set (flatter), spelling 1 for a
// multi-type set, always at minimal depth (a tag grouping's buckets sit at `#`).
const (
	envelopeType = "organize-base-v1"
	fieldBase    = "_base"
	fieldAnchor  = "_anchor"
	fieldQuery   = "_query"
	fieldType    = "_type"
	// fieldGroupBy is the `- _group-by = <spelling>` envelope directive a TAG
	// grouping carries (native tags design G10) — the hoisted dialect's spec in
	// the SAME spelling as the --group-by flag: `(tags)` for the whole tag set,
	// a bare `project` for a namespace. A field grouping never emits it (its
	// spelling IS the `# <dim>=` / `# <dim>=(month)` heading).
	fieldGroupBy = "_group-by"
	// fieldTagAtoms / fieldTagStrip are the tag-atom render levers (native tags
	// design G1/G2/G3, slice 2): DATA-plane envelope fields, content-addressed
	// into `_base` when present, OMITTED at their defaults (existing documents
	// stay byte-identical). `[organize] tag_atoms` / `tag_strip` config may set
	// the defaults; the document's explicit field wins (effectiveTagAtoms /
	// effectiveTagStrip).
	fieldTagAtoms = "_tag-atoms"
	fieldTagStrip = "_tag-strip"
)

// The `_tag-atoms` lever's domain (design G1): where an object's tag set
// renders inside its box — leading (after id/`!type`, before the `name=value`
// atoms; the default), trailing (after the atoms), or none (no tag atoms at
// all). Position is presentation-only: the parser collects tags wherever they
// sit, so every spelling re-parses identically.
const (
	tagAtomsLeading  = "leading"
	tagAtomsTrailing = "trailing"
	tagAtomsNone     = "none"
)

// The `_tag-strip` lever's domain (design G2): whether a tag-grouped document
// strips from each appearance's box exactly the placement (Via) tag(s) that
// filed it under its bucket — placement (the default; the #229 rule: placement
// carries it, never also an atom) — or keeps every tag in every box (none).
// Field-grouped documents strip nothing either way (no tag placement exists).
const (
	tagStripPlacement = "placement"
	tagStripNone      = "none"
)

// parseTagAtomsField validates a `- _tag-atoms = <value>` envelope value;
// empty (absent) stays empty. An out-of-domain value is a loud bad request,
// never a silent default.
func parseTagAtomsField(raw string) (string, error) {
	switch raw {
	case "", tagAtomsLeading, tagAtomsTrailing, tagAtomsNone:
		return raw, nil
	}
	return "", errors.BadRequestf(
		"organize: `- %s = %s` is not one of %s, %s, %s",
		fieldTagAtoms, raw, tagAtomsLeading, tagAtomsTrailing, tagAtomsNone,
	)
}

// parseTagStripField validates a `- _tag-strip = <value>` envelope value;
// empty (absent) stays empty. An out-of-domain value is a loud bad request.
func parseTagStripField(raw string) (string, error) {
	switch raw {
	case "", tagStripPlacement, tagStripNone:
		return raw, nil
	}
	return "", errors.BadRequestf(
		"organize: `- %s = %s` is not one of %s, %s",
		fieldTagStrip, raw, tagStripPlacement, tagStripNone,
	)
}

// effectiveTagAtoms resolves the `_tag-atoms` lever: the document's explicit
// field wins over the `[organize] tag_atoms` config default, which wins over
// the built-in default (leading) — design G3. Both inputs are already
// domain-validated (parseTagAtomsField / cgconfig's OrganizeConfig.Validate).
func effectiveTagAtoms(docVal, configVal string) string {
	switch {
	case docVal != "":
		return docVal
	case configVal != "":
		return configVal
	}
	return tagAtomsLeading
}

// effectiveTagStrip is effectiveTagAtoms' `_tag-strip` sibling: doc field,
// then `[organize] tag_strip`, then the built-in default (placement).
func effectiveTagStrip(docVal, configVal string) string {
	switch {
	case docVal != "":
		return docVal
	case configVal != "":
		return configVal
	}
	return tagStripPlacement
}

// objectLine is one espalier box literal: the anchor-relative node id, its type
// tag (inline for spelling 1; empty for spelling 2, where the envelope carries
// it), any bare tag tokens, the plugin-presented detail atoms
// (date/time/location; FDR 0023, cutting-garden#47), and the description
// trailer. The box interior is a trellis.Literal (native tags design G13): the
// parse and the spelling both belong to trellis.
type objectLine struct {
	ID   string
	Type string
	// Tags are the bare / quoted identifier tokens a box carries (design G9:
	// bare is ALWAYS a tag; leading by default, after the atoms under
	// `_tag-atoms = trailing`). Since native tags slice 2 generate RENDERS them
	// from the type's designated tag field (tagRender), SortKey-ordered, with
	// the placement Via stripped under a tag grouping (`_tag-strip`). Apply
	// still does not WRITE them: an edited tag set (vs the pinned base) is
	// refused loudly (rejectEditedTagAtoms) until slice 2 Task 3's membership
	// write.
	Tags   []string
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
	// GroupBy is `- _group-by`: the TAG grouping's spelling (design G10), `(tags)`
	// (the whole tag set) or a bare namespace (`project`). Empty for a field
	// grouping, which carries its spelling in a `# <dim>=` heading instead. When
	// set, groupedSpec reads the kind and namespace from it alone; the tag
	// DIMENSION it applies to is resolved from the plugin at apply time
	// (resolveTagDimension). The hoisted body has no parent dimension heading —
	// its buckets are bare `# <value>` headings at minimal depth.
	GroupBy string
	// TagAtoms is `- _tag-atoms`: where tag atoms render in a box (leading /
	// trailing / none, design G1). Empty = absent (the effective value falls
	// back to config, then leading — effectiveTagAtoms); generate emits the
	// field only when the effective value is non-default (G3).
	TagAtoms string
	// TagStrip is `- _tag-strip`: whether a tag grouping strips the placement
	// (Via) tag(s) from each appearance's box (placement / none, design G2).
	// Empty = absent, resolving like TagAtoms (default placement).
	TagStrip string
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

// isDimTerm reports a dimension heading term — the field group-by spelling
// `status=` or `date_due=(month)` (design G10), read by the group-term parser.
func isDimTerm(t string) bool {
	gt, err := parseGroupTerm(t)
	return err == nil && gt.kind == groupTermField
}

// isTagGrouping reports whether the document uses the hoisted TAG-grouping
// dialect (RFC 0019 tags slice 3 B3): a `_group-by` directive present, its
// buckets bare `# <value>` headings with no parent dimension heading. A field
// grouping (GroupBy empty) keeps the `# <dim>=` heading + `## =<value>` buckets.
func (doc document) isTagGrouping() bool { return doc.GroupBy != "" }

// sectionValueReader returns the function that extracts a bucket VALUE from a
// heading term for this document's grouping dialect. A field grouping's buckets
// are `=<value>` terms (isValueTerm); a TAG grouping's buckets are the hoisted
// bare/quoted headings `<value>` — every non-type heading is a value, INCLUDING
// a namespace grouping's root heading (`# project`), which is a live bucket
// whose value is the bare namespace (design G10a): walkSectionValues'
// deepest-value-wins rule then places a line under a nested continuation
// (`## -client`) in the continuation bucket, and a line directly under the root
// (equivalently, after a `##` reset popping a continuation) in the root bucket.
// Either way the value is one trellis term (parseHeadingValue): a bare or quoted
// identifier, decoded. A type heading (`!<type>`) and, for a field grouping, a
// `<dim>=` heading are never buckets. A heading that is not a single plain term
// is a bad request. Shared by assignments and memberships so both dialects read
// a section's value identically.
func (doc document) sectionValueReader() func(term string) (value string, isValue bool, err error) {
	if doc.isTagGrouping() {
		return func(term string) (string, bool, error) {
			if strings.HasPrefix(term, "!") {
				return "", false, nil
			}
			value, exact, err := parseHeadingValue(term)
			if err != nil {
				return "", false, err
			}
			if exact {
				return "", false, errors.BadRequestf(
					"organize: tag bucket heading %q must be bare (no `=` prefix)", term,
				)
			}
			return value, true, nil
		}
	}
	return func(term string) (string, bool, error) {
		if !isValueTerm(term) {
			return "", false, nil
		}
		value, _, err := parseHeadingValue(term)
		if err != nil {
			return "", false, err
		}
		return value, true, nil
	}
}

// parseHeadingValue reads a bucket heading's VALUE as one trellis term: a bare
// identifier (`work`, `-client`, `NEEDS-ACTION`) or a quoted String (`"_ inbox"`,
// decoded), optionally carrying the field dialect's `=` exact prefix
// (`=COMPLETED`), which is reported, not part of the value. The writer half is
// trellis.QuoteIfNeeded — the ONE quoting rule box slots and headings share
// (design G9).
func parseHeadingValue(term string) (value string, exact bool, err error) {
	t, err := trellis.ParseTerm(term)
	if err != nil {
		return "", false, errors.BadRequestf("organize: heading %q: %v", term, err)
	}
	if t.Negate {
		return "", false, errors.BadRequestf("organize: heading %q: a bucket value cannot be negated", term)
	}
	if v, ok := plainIdent(t); ok {
		return v, t.Exact, nil
	}
	return "", false, errors.BadRequestf(
		"organize: heading %q: a bucket value is a bare or quoted identifier", term,
	)
}

// parseEnvelopeDigest reads the `- _base = @<digest>` pin as a trellis
// DigestTerm, returning the bare digest (`blake2b256-…`); empty stays empty.
func parseEnvelopeDigest(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	t, err := trellis.ParseTerm(raw)
	if err != nil {
		return "", errors.BadRequestf("organize: `- %s = %s`: %v", fieldBase, raw, err)
	}
	if d, ok := t.Basic.(trellis.DigestBasicTerm); ok && d.Sigil == nil && !t.Negate && !t.Exact {
		return d.Digest.Digest, nil
	}
	return "", errors.BadRequestf("organize: `- %s = %s` is not a `@<digest>` term", fieldBase, raw)
}

// parseEnvelopeType reads the `- _type = !<type>` field as a trellis TypeTerm,
// returning the bare type name; empty stays empty.
func parseEnvelopeType(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	t, err := trellis.ParseTerm(raw)
	if err != nil {
		return "", errors.BadRequestf("organize: `- %s = %s`: %v", fieldType, raw, err)
	}
	if ty, ok := t.Basic.(trellis.TypeBasicTerm); ok && ty.Sigil == nil && !t.Negate && !t.Exact {
		return ty.Type.Name, nil
	}
	return "", errors.BadRequestf("organize: `- %s = %s` is not a `!<type>` term", fieldType, raw)
}

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
	if doc.GroupBy != "" {
		fmt.Fprintf(&b, "- %s = %s\n", fieldGroupBy, doc.GroupBy)
	}
	if doc.TagAtoms != "" {
		fmt.Fprintf(&b, "- %s = %s\n", fieldTagAtoms, doc.TagAtoms)
	}
	if doc.TagStrip != "" {
		fmt.Fprintf(&b, "- %s = %s\n", fieldTagStrip, doc.TagStrip)
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
	trailingTags := doc.TagAtoms == tagAtomsTrailing
	if len(doc.Ungrouped) > 0 {
		b.WriteByte('\n')
		for _, ln := range doc.Ungrouped {
			writeObjectLine(b, ln, trailingTags)
		}
	}
	for _, s := range doc.Sections {
		b.WriteByte('\n')
		fmt.Fprintf(b, "%s %s\n", strings.Repeat("#", s.Depth), s.Term)
		if len(s.Lines) > 0 {
			b.WriteByte('\n')
		}
		for _, ln := range s.Lines {
			writeObjectLine(b, ln, trailingTags)
		}
	}
}

// writeObjectLine renders one espalier box literal and its description trailer:
// `- [<id> !<type> <tag>… <name>=<value>…] <desc>`. The interior is spelled by
// trellis.WriteLiteral (design G13): the id, the type, the tag terms, then the
// detail atoms, each a ground `name=value` espalier field (cutting-garden#47).
// trailingTags (the document's `_tag-atoms = trailing` lever, design G1) moves
// the tag terms after the atoms via trellis.WriteLiteralTrailingTags —
// presentation-only, since the parser collects tags wherever they sit.
func writeObjectLine(b *strings.Builder, ln objectLine, trailingTags bool) {
	lit := trellis.Literal{ID: ln.ID, Type: ln.Type, Tags: ln.Tags}
	for _, f := range ln.Fields {
		lit.Atoms = append(lit.Atoms, trellis.Atom{Name: f.Name, Value: f.Value})
	}
	b.WriteString("- [")
	if trailingTags {
		trellis.WriteLiteralTrailingTags(b, lit)
	} else {
		trellis.WriteLiteral(b, lit)
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
	if doc.BaseDigest, err = parseEnvelopeDigest(fields[fieldBase]); err != nil {
		return document{}, err
	}
	doc.Anchor = fields[fieldAnchor]
	doc.Query = fields[fieldQuery]
	if doc.Type, err = parseEnvelopeType(fields[fieldType]); err != nil {
		return document{}, err
	}
	doc.GroupBy = fields[fieldGroupBy]
	if doc.TagAtoms, err = parseTagAtomsField(fields[fieldTagAtoms]); err != nil {
		return document{}, err
	}
	if doc.TagStrip, err = parseTagStripField(fields[fieldTagStrip]); err != nil {
		return document{}, err
	}

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
// enclosing section (or the ungrouped set before any heading). Heading depth is
// STRUCTURE-ONLY (native tags design G10): the shallowest `#` level present in
// the body is the root, and deeper levels nest relative to it, so a hand-written
// document starting at `##` parses identically to one starting at `#`
// (rootHeadingDepth). The open heading path is kept as a stack of section
// indices; a non-empty heading at depth N pops every open heading at N or
// deeper and pushes itself, and object lines attach to the top of the stack.
//
// An EMPTY heading is a RESET: `##` (after normalization) pops the heading
// context at its depth and deeper WITHOUT pushing anything, so the object lines
// that follow attach to the nearest enclosing heading shallower than N (the
// ungrouped context if none) — exactly as if they had been written under that
// heading; a bare `#` empties the stack. Note "nearest open", not "N−1": a
// non-contiguous ladder `# a` / `### b` / `###` lands under `a`. A reset deeper
// than the current context pops nothing (a no-op); re-entering a bucket needs a
// new non-empty heading. Resets never
// become sections, so a parsed document renders without them (generate never
// emits one) and every derived view (assignments, memberships, groupedSpec,
// hasBuckets) reads the resolved placement with no reset awareness of its own.
// A spelling-1 `# !<type>` heading is just another stack frame: `##` under
// `# !type` / `## status=` / `### =A` lands lines under the type alone (its
// ungrouped set), and `#` pops past the type heading to the document's
// ungrouped set.
func parseBody(doc *document, body string) error {
	lines := strings.Split(body, "\n")
	root := rootHeadingDepth(lines)
	// open is the stack of open heading sections (indices into doc.Sections),
	// shallowest first; the top is where object lines attach.
	var open []int
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		switch {
		case strings.HasPrefix(line, "#"):
			rawDepth, term := splitHeading(line)
			depth := rawDepth - root + 1
			open = popTo(open, doc.Sections, depth)
			if term == "" {
				continue // a reset: the pop above IS the effect
			}
			doc.Sections = append(doc.Sections, section{Depth: depth, Term: term})
			open = append(open, len(doc.Sections)-1)

		case strings.HasPrefix(line, "-"):
			ln, err := parseObjectLine(strings.TrimSpace(line[1:]))
			if err != nil {
				return errors.BadRequestf("organize: body line %d: %s", i+1, err)
			}
			if len(open) > 0 {
				cur := open[len(open)-1]
				doc.Sections[cur].Lines = append(doc.Sections[cur].Lines, ln)
			} else {
				doc.Ungrouped = append(doc.Ungrouped, ln)
			}

		default:
			return errors.BadRequestf("organize: body line %d: unrecognized %q", i+1, line)
		}
	}
	return nil
}

// splitHeading splits a heading line into its `#` count and its (trimmed) text —
// empty for a reset heading.
func splitHeading(line string) (depth int, term string) {
	for depth < len(line) && line[depth] == '#' {
		depth++
	}
	return depth, strings.TrimSpace(line[depth:])
}

// rootHeadingDepth returns the shallowest `#` count among the body's NON-EMPTY
// heading lines — the level that normalizes to depth 1. Resets are excluded:
// every reset comparison is relative, so counting them would not change any
// placement, but it would let a `## work` / `#` document parse `work` at depth
// 2 and re-render one level deeper than minimal. 1 when the body has no
// non-empty heading at all.
func rootHeadingDepth(lines []string) int {
	root := 0
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "#") {
			continue
		}
		depth, term := splitHeading(line)
		if term == "" {
			continue
		}
		if root == 0 || depth < root {
			root = depth
		}
	}
	if root == 0 {
		return 1
	}
	return root
}

// popTo pops every open section (open holds indices into secs, shallowest first)
// whose depth is depth or deeper, returning the shortened stack — the one
// ladder-walking rule the parser (parseBody) and the value walker
// (walkSectionValues) share.
func popTo(open []int, secs []section, depth int) []int {
	for len(open) > 0 && secs[open[len(open)-1]].Depth >= depth {
		open = open[:len(open)-1]
	}
	return open
}

// parseObjectLine parses one espalier box literal `[<id> !<type> …] <desc>`:
// the leading group through trellis.ParseLiteralPrefix (design G13 — the ground
// subset of a trellis Group, read by the real lexer so quotes/escapes/nesting are
// never re-scanned here; a non-ground term is a loud bad request naming it), the
// remainder of the line as the description trailer.
func parseObjectLine(rest string) (objectLine, error) {
	lit, trailer, err := trellis.ParseLiteralPrefix(rest)
	if err != nil {
		return objectLine{}, err
	}
	ln := objectLine{ID: lit.ID, Type: lit.Type, Tags: lit.Tags, Desc: strings.TrimSpace(trailer)}
	for _, a := range lit.Atoms {
		ln.Fields = append(ln.Fields, cgp.BoxAtom{Name: a.Name, Value: a.Value})
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

// groupedDimension returns the grouped field dimension — the `<dim>=` heading
// term (RFC 0015: the dimension is the heading, not a separate field). Empty when
// the document has no dimension heading (or it does not parse).
func (doc document) groupedDimension() string {
	spec, err := doc.groupedSpec()
	if err != nil {
		return ""
	}
	return spec.Dim
}

// groupedSpec recovers the document's grouping in the one G10 spelling. A TAG
// grouping is read from the `_group-by` envelope directive (`(tags)` or a bare
// namespace) — its tag DIMENSION is left empty for apply to resolve from the
// plugin. A FIELD grouping is read from its dimension heading, `<dim>=` or, for
// a date grouping, `<dim>=(<granularity>)` (cutting-garden#230): the document
// itself carries the granularity so apply coarsens live values exactly as
// generate did, without consulting config (which may have changed in between).
// An unknown granularity spelling, or a retired `dim:granularity=` heading, is
// a loud bad request — never a silent exact-match degradation. The zero spec
// means no grouping at all.
func (doc document) groupedSpec() (groupSpec, error) {
	if doc.isTagGrouping() {
		return parseGroupByEncoding(doc.GroupBy)
	}

	for _, s := range doc.Sections {
		// Bucket values (`=COMPLETED`) and type headings (`!type`) are never the
		// dimension heading; anything else must be a group term.
		if strings.HasPrefix(s.Term, "=") || strings.HasPrefix(s.Term, "!") {
			continue
		}
		gt, err := parseGroupTerm(s.Term)
		if err != nil {
			return groupSpec{}, errors.BadRequestf("organize: dimension heading %q: %w", s.Term, err)
		}
		if gt.kind != groupTermField {
			continue
		}
		g, err := gt.granularity()
		if err != nil {
			return groupSpec{}, errors.BadRequestf("organize: dimension heading %q: %w", s.Term, err)
		}
		return groupSpec{Dim: gt.name, Granularity: g}, nil
	}
	return groupSpec{}, nil
}

// hasBuckets reports whether the document has any bucket heading — a section
// other than a type heading or a field dimension heading.
func (doc document) hasBuckets() bool {
	for _, s := range doc.Sections {
		if strings.HasPrefix(s.Term, "!") || isDimTerm(s.Term) {
			continue
		}
		return true
	}
	return false
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

	if err := doc.walkSectionValues(place); err != nil {
		return nil, err
	}
	return out, nil
}

// walkSectionValues walks the flat sections as a depth stack, calling place for
// every object line with the bucket VALUE on its enclosing heading path (the
// deepest value heading wins). Shared by assignments and memberships.
func (doc document) walkSectionValues(place func(objectLine, string) error) error {
	readValue := doc.sectionValueReader()
	var stack []int // indices into doc.Sections, the open heading path
	for i, s := range doc.Sections {
		stack = popTo(stack, doc.Sections, s.Depth)
		stack = append(stack, i)
		value := ""
		for _, idx := range stack {
			v, ok, err := readValue(doc.Sections[idx].Term)
			if err != nil {
				return err
			}
			if ok {
				value = v
			}
		}
		for _, ln := range s.Lines {
			if err := place(ln, value); err != nil {
				return err
			}
		}
	}
	return nil
}

// memberships is the cardinality-aware sibling of assignments: it projects the
// document into a box-id → bucket-value SET, mirroring assignments' traversal
// (ungrouped objects first, then the flat sections walked as a depth stack, each
// object's value the deepest `=<value>` term on its enclosing heading path) but
// collecting a []string per id instead of a single string. It is the foundation
// the N-way merge for a multi-valued dimension builds on.
//
// When multi is true (a multi-valued dimension like categories) an object may
// legally appear under several DISTINCT buckets — all are accumulated in document
// order. When multi is false (a single-valued dimension) a second distinct bucket
// for one id is the same malformed-edit rejection assignments makes. In BOTH
// modes the SAME bucket value appearing twice for one id is a duplicated line and
// rejects loudly. An ungrouped object (value "") is recorded with an empty set.
func (doc document) memberships(multi bool) (map[string][]string, error) {
	out := make(map[string][]string)
	// placed is the occupancy ledger — every value placed for an id INCLUDING the
	// ungrouped "" — kept separate from out (the non-empty membership payload) so
	// the duplicate guard fires even for ids whose only membership is empty. Any id
	// placed more than once is an error, UNLESS multi AND every placement (prior and
	// current) is a distinct non-empty bucket.
	placed := make(map[string][]string)
	place := func(ln objectLine, value string) error {
		prior, seen := placed[ln.ID]
		if seen {
			priorHasEmpty := false
			priorHasValue := false
			for _, p := range prior {
				if p == "" {
					priorHasEmpty = true
				}
				if value != "" && p == value {
					priorHasValue = true
				}
			}
			switch {
			case priorHasValue:
				return errors.BadRequestf(
					"organize: object %s appears twice under bucket %q", ln.ID, value,
				)
			case value == "" || priorHasEmpty:
				return errors.BadRequestf(
					"organize: object %s appears both ungrouped and grouped, or twice", ln.ID,
				)
			case !multi:
				return errors.BadRequestf(
					"organize: object %s appears twice (buckets %q and %q)",
					ln.ID, prior[len(prior)-1], value,
				)
			}
		}
		placed[ln.ID] = append(prior, value)
		if value == "" {
			// An ungrouped object (no `=<value>` on its path) contributes no
			// membership; record the id present with an empty set. Mirrors how
			// assignments records the ungrouped case with value "".
			if _, ok := out[ln.ID]; !ok {
				out[ln.ID] = []string{}
			}
			return nil
		}
		out[ln.ID] = append(out[ln.ID], value)
		return nil
	}

	for _, ln := range doc.Ungrouped {
		if err := place(ln, ""); err != nil {
			return nil, err
		}
	}

	if err := doc.walkSectionValues(place); err != nil {
		return nil, err
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
