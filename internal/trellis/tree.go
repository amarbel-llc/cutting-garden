package trellis

import (
	"strings"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// IDKind says what a TreeNode's id slot holds (the PEG's IDSlot).
type IDKind int

const (
	// IDReal is an ordinary id, bare or quoted, optionally locked.
	IDReal IDKind = iota
	// IDNull is the null id `_`: a node that equals no id, not even itself
	// (a bucket heading's box, a stencil's pattern node). An object literally
	// named `_` is IDReal with ID "_", spelled `"_"`.
	IDNull
	// IDTemp is a temp id `+`, `+id`, `+"id"`: an object that does not exist
	// yet. ID holds the handle, empty for a bare `+`.
	IDTemp
)

// A TreeAtom is one field predicate of a TreeNode with its `^` prefix.
type TreeAtom struct {
	Negate bool
	Pred   FieldPred
}

// A TreeNode is one literal of the canonical nested form (RFC 0014
// "Isomorphism"; the one-isomorphism rulings of 2026-11-09): the id slot, at most
// one `!type`, tags, field atoms with any operator, an optional blob digest,
// and children from `[-> …]` subpaths, nesting to any depth. It is the shape
// espalier data, organize documents and ajour stencils share; which holes a
// consumer admits (non-`=` operators, the null id, a lock) is that
// consumer's validity condition over this one tree, as groundness is.
type TreeNode struct {
	Kind IDKind
	// ID is the decoded id (IDReal) or temp handle (IDTemp); empty for IDNull.
	ID string
	// Lock is the digest of a locked id `name@<digest>`, empty when unlocked.
	Lock string
	// Globstar records the input spelling `<id>/**` (a bare id only); ID then
	// holds the id without the suffix. Its meaning is the consumer's (ajour
	// reads it as the `subtree` tag); WriteTree never spells it.
	Globstar bool
	Type     string
	// Tags are the bare or quoted identifier terms, decoded, in source order.
	Tags []string
	// Atoms are the field predicates, in source order.
	Atoms []TreeAtom
	// Digest is a standalone `@digest` term (espalier's blob identity).
	Digest   string
	Children []TreeNode
}

// ParseTree parses src as a sequence of root literals in either input
// spelling: the canonical nested form (src opens with `[`) or the heading
// rendering (src opens with `#` or `-`; ParseTreeHeadings). Every refusal is a
// bad request naming the offending term.
func ParseTree(src string) ([]TreeNode, error) {
	trimmed := strings.TrimLeft(src, " \t\r\n")
	if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "-") {
		return ParseTreeHeadings(src)
	}
	return ParseTreeNested(src)
}

// ParseTreeNested parses src as whitespace-separated root literals in the
// canonical nested spelling.
func ParseTreeNested(src string) ([]TreeNode, error) {
	p := &parser{src: []rune(src)}
	var roots []TreeNode
	for {
		separated := p.skipSP()
		if p.atEOF() {
			return roots, nil
		}
		if len(roots) > 0 && !separated {
			// The PEG's Literals entry: `[a] [b]`, never `[a][b]`
			// (cutting-garden#402).
			return nil, p.treeError("roots must be separated by whitespace: `%s` follows the last `]`", p.termTextAt())
		}
		node, err := p.parseTreeLiteral()
		if err != nil {
			return nil, err
		}
		roots = append(roots, node)
	}
}

// parseTreeLiteral parses one `[` IDSlot attrs `]` at p.pos.
func (p *parser) parseTreeLiteral() (TreeNode, error) {
	if !p.literal("[") {
		return TreeNode{}, p.treeError("expected a `[…]` literal, found `%s`", p.termTextAt())
	}
	return p.parseTreeBody()
}

// parseTreeBody parses IDSlot (SP attr)* SP? `]`, the `[` already consumed.
// A root literal and a child's interior after `-> ` are both read here.
func (p *parser) parseTreeBody() (TreeNode, error) {
	p.skipSPOpt()
	node, err := p.parseTreeIDSlot()
	if err != nil {
		return TreeNode{}, err
	}
	for {
		if !p.skipSP() {
			break
		}
		if p.atEOF() || p.src[p.pos] == ']' {
			break
		}
		lookahead := p.pos
		if comb, ok := p.parseCombinator(); ok {
			p.pos = lookahead
			return TreeNode{}, p.treeError(
				"`%s` after a literal's terms: a literal is one step, and a child is written `[-> …]`",
				combinatorString(comb),
			)
		}
		p.pos = lookahead
		if err := p.parseTreeAttr(&node); err != nil {
			return TreeNode{}, err
		}
	}
	p.skipSPOpt()
	if !p.literal("]") {
		if p.atEOF() {
			return TreeNode{}, p.treeError("unclosed `[`: the input ended inside a literal")
		}
		return TreeNode{}, p.treeError("unexpected `%s` in a literal", p.termTextAt())
	}
	return node, nil
}

// parseTreeIDSlot parses the PEG's IDSlot: TempID / NullID / MarklTerm /
// QuotedRef / Ident, plus the `<id>/**` input spelling.
func (p *parser) parseTreeIDSlot() (TreeNode, error) {
	if p.atEOF() || p.src[p.pos] == ']' {
		return TreeNode{}, p.treeError("a literal with no id slot")
	}
	start := p.pos
	var node TreeNode
	switch r := p.src[p.pos]; {
	case r == '^' || r == '=':
		return TreeNode{}, p.treeError("a prefix on the id slot: `%s`", p.termTextAt())
	case r == '+':
		p.pos++
		node.Kind = IDTemp
		id, ok := p.parseTempIDHandle()
		if !ok {
			return TreeNode{}, p.treeError("an unterminated temp id `%s`", string(p.src[start:]))
		}
		node.ID = id
	case r == '_' && !IsIdentRuneAt(p.src, p.pos+1):
		p.pos++
		node.Kind = IDNull
	default:
		if m, ok := p.parseMarklTerm(); ok {
			node.ID, node.Lock = m.Purpose, m.Digest
			break
		}
		if s, ok := p.parseString(); ok {
			node.ID = s
			break
		}
		id, ok := p.scanIdentText()
		if !ok {
			return TreeNode{}, p.treeErrorNamingAt(start, "a literal with no id slot: `%s` stands where the id goes")
		}
		node.ID = id
		if p.hasPrefix("**") {
			p.pos += 2
			if !strings.HasSuffix(id, "/") || len(id) < 2 || p.continuesTerm() {
				return TreeNode{}, p.treeErrorNamingAt(start, "`/**` may only end an id: `%s`")
			}
			node.ID, node.Globstar = strings.TrimSuffix(id, "/"), true
		}
	}
	if p.continuesTerm() {
		return TreeNode{}, p.treeErrorNamingAt(start, "a sigil or stray rune on the id slot: `%s`")
	}
	return node, nil
}

// parseTreeAttr parses one term after the id slot into node: a child
// `[-> …]`, a type, a tag, an atom or a digest.
func (p *parser) parseTreeAttr(node *TreeNode) error {
	start := p.pos
	if p.literal("[") {
		p.skipSPOpt()
		combStart := p.pos
		if comb, ok := p.parseCombinator(); ok {
			if comb.Kind != CombinatorFwd {
				return p.treeErrorAt(combStart, "a child combinator other than `->`: `%s`", combinatorString(comb))
			}
			if !p.skipSP() || p.atEOF() || p.src[p.pos] == ']' {
				return p.treeErrorAt(start, "an empty child `[->]` (a child is `[-> id …]`, with a space after the arrow)")
			}
			// A child's interior is read through IDSlot like a root
			// literal's: the PEG's Child production (cutting-garden#397).
			child, err := p.parseTreeBody()
			if err != nil {
				return err
			}
			node.Children = append(node.Children, child)
			return nil
		}
		p.pos = start
	}
	term, ok := p.parseTerm()
	if !ok || p.continuesTerm() {
		return p.treeErrorNamingAt(start, "unexpected `%s` in a literal")
	}
	refuse := func(why string) error {
		return p.treeErrorAt(start, "term `%s` %s", termString(term), why)
	}
	kind, sigiled := literalTermKind(term.Basic)
	if term.Negate && kind != termAtom {
		return refuse("is not a literal term: `^` negates only a field atom")
	}
	if sigiled {
		return refuse("carries a sigil (a version-set scope, not a literal term)")
	}
	// A leading `=` (exact) is inert in a literal: ids already match
	// exactly (FDR 0028 "Matching"), so the canonical form drops it.
	switch kind {
	case termAtom:
		pred := term.Basic.(FieldPredBasicTerm).FieldPred
		node.Atoms = append(node.Atoms, TreeAtom{Negate: term.Negate, Pred: pred})
	case termType:
		if node.Type != "" {
			return refuse("is a second `!type` (a literal carries at most one)")
		}
		node.Type = term.Basic.(TypeBasicTerm).Type.Name
	case termDigest:
		if node.Digest != "" {
			return refuse("is a second `@digest` (a literal carries at most one)")
		}
		node.Digest = term.Basic.(DigestBasicTerm).Digest.Digest
	case termTag:
		tag, _ := plainIdent(term.Basic)
		node.Tags = append(node.Tags, tag)
	case termVersionSub:
		return refuse("is a version subpath, not a literal term")
	case termOrGroup:
		return refuse("is a group of OR-alternatives, not a literal term")
	case termGroup:
		return refuse("is a bracketed group, not a literal term (a child is `[-> …]`)")
	case termLocked:
		return refuse("is a locked id outside the id slot")
	default:
		return refuse("is not a literal term (a qualifier or a bare sigil)")
	}
	return nil
}

// termKind classifies one term of a literal. ParseLiteral's ground projection
// and the tree reader share this one classification and differ only in which
// kinds they admit and how they word a refusal.
type termKind int

const (
	termAtom termKind = iota
	termType
	termTag
	termDigest
	termLocked     // a MarklTerm, `name@digest`
	termVersionSub // `[: …]`
	termOrGroup    // `[a, b]`
	termGroup      // any other bracketed group
	termOther      // a qualifier or a bare sigil
)

// literalTermKind classifies b, and reports whether it carries a trailing
// sigil (basicSigil).
func literalTermKind(b BasicTerm) (kind termKind, sigiled bool) {
	sigiled = basicSigil(b) != nil
	switch b := b.(type) {
	case FieldPredBasicTerm:
		return termAtom, false
	case TypeBasicTerm:
		return termType, sigiled
	case IdentBasicTerm, QuotedRefBasicTerm:
		return termTag, sigiled
	case DigestBasicTerm:
		return termDigest, sigiled
	case MarklBasicTerm:
		return termLocked, sigiled
	case GroupBasicTerm:
		if _, isVersionSub := b.Group.Body.(VersionSub); isVersionSub {
			return termVersionSub, false
		}
		if alts, ok := b.Group.Body.(Alternatives); ok && len(alts.Alts) > 1 {
			return termOrGroup, false
		}
		return termGroup, false
	}
	return termOther, false
}

// basicSigil is the trailing sigil a basic term carries, nil when none.
func basicSigil(b BasicTerm) *Sigil {
	switch b := b.(type) {
	case TypeBasicTerm:
		return b.Sigil
	case DigestBasicTerm:
		return b.Sigil
	case MarklBasicTerm:
		return b.Sigil
	case QuotedRefBasicTerm:
		return b.Sigil
	case IdentBasicTerm:
		return b.Sigil
	}
	return nil
}

func (p *parser) hasPrefix(s string) bool {
	rs := []rune(s)
	if p.pos+len(rs) > len(p.src) {
		return false
	}
	for i, r := range rs {
		if p.src[p.pos+i] != r {
			return false
		}
	}
	return true
}

// continuesTerm reports that the rune at p.pos is glued to the term just
// read: anything but whitespace, `]` or the end of input.
func (p *parser) continuesTerm() bool {
	return !p.atEOF() && !isSP1(p.src[p.pos]) && p.src[p.pos] != ']'
}

// termTextAt is the run of non-space runes at p.pos (through any `]`), the
// text a refusal names when no term parsed there.
func (p *parser) termTextAt() string {
	end := p.pos
	for end < len(p.src) && !isSP1(p.src[end]) {
		end++
	}
	if end == p.pos {
		return "end of input"
	}
	return string(p.src[p.pos:end])
}

// treeErrorAt is treeError reported at offset at (the start of the term it
// names).
func (p *parser) treeErrorAt(at int, format string, args ...any) error {
	p.pos = at
	return p.treeError(format, args...)
}

// treeErrorNamingAt reports at offset at, naming the source text there
// (termTextAt read after the reset); format has one %s for it.
func (p *parser) treeErrorNamingAt(at int, format string) error {
	p.pos = at
	return p.treeError(format, p.termTextAt())
}

func (p *parser) treeError(format string, args ...any) error {
	args = append([]any{p.pos}, args...)
	return errors.BadRequestf("trellis literal at offset %d: "+format, args...)
}

func combinatorString(c Combinator) string {
	pred := ""
	if c.Pred != nil {
		pred = termsString(c.Pred.Terms)
	}
	switch c.Kind {
	case CombinatorFwd:
		return "->"
	case CombinatorBack:
		return "<-"
	case CombinatorFwdClosure:
		return "->>"
	case CombinatorBackClosure:
		return "<<-"
	case CombinatorTypedFwd:
		return "-[" + pred + "]->"
	case CombinatorTypedBack:
		return "<-[" + pred + "]-"
	case CombinatorTypedClosure:
		return "-[" + pred + "]->>"
	default:
		return "?"
	}
}

func termsString(terms []Term) string {
	parts := make([]string, len(terms))
	for i, t := range terms {
		parts[i] = termString(t)
	}
	return strings.Join(parts, " ")
}

// ---- writer -----------------------------------------------------------------

// WriteTree writes roots in the canonical nested spelling: per literal the
// id slot, `!type`, the tags, the atoms, the digest, then the children, one
// space between terms; each child on its own line indented two spaces per
// depth; roots separated by a blank line; a trailing newline. Two equal trees
// write equal bytes, and the output re-parses (ParseTreeNested) to the same
// tree with Globstar cleared: the `/**` input spelling is never written, so
// a consumer that gives it meaning (ajour: `subtree`) folds it in first.
func WriteTree(b *strings.Builder, roots []TreeNode) {
	for i, root := range roots {
		if i > 0 {
			b.WriteString("\n")
		}
		writeTreeNode(b, root, 0)
		b.WriteString("\n")
	}
}

func writeTreeNode(b *strings.Builder, n TreeNode, depth int) {
	b.WriteByte('[')
	if depth > 0 {
		b.WriteString("-> ")
	}
	b.WriteString(SpellTreeID(n))
	if n.Type != "" {
		b.WriteString(" !")
		b.WriteString(n.Type)
	}
	for _, tag := range n.Tags {
		b.WriteByte(' ')
		b.WriteString(QuoteIfNeeded(tag))
	}
	for _, atom := range n.Atoms {
		b.WriteByte(' ')
		b.WriteString(SpellTreeAtom(atom))
	}
	if n.Digest != "" {
		b.WriteString(" @")
		b.WriteString(n.Digest)
	}
	for _, child := range n.Children {
		b.WriteByte('\n')
		b.WriteString(strings.Repeat("  ", depth+1))
		writeTreeNode(b, child, depth+1)
	}
	b.WriteByte(']')
}

// SpellTreeID spells a node's id slot so it re-parses to the same kind: `_`
// for the null id, `+…` for a temp id, and a real id quoted whenever its
// bare spelling would read as one of those markers.
func SpellTreeID(n TreeNode) string {
	switch n.Kind {
	case IDNull:
		return "_"
	case IDTemp:
		return SpellTempID(n.ID)
	}
	id := spellRealID(n.ID)
	if n.Lock != "" {
		id += "@" + n.Lock
	}
	return id
}

// spellRealID is QuoteIfNeeded for an id slot: `_` and a leading `+` would
// re-read as the null or temp id, so they are quoted too.
func spellRealID(id string) string {
	if id == "_" || strings.HasPrefix(id, "+") {
		return quoteString(id)
	}
	return QuoteIfNeeded(id)
}

// SpellTreeAtom spells one atom with its `^` prefix (fieldPredString).
func SpellTreeAtom(a TreeAtom) string {
	if a.Negate {
		return "^" + fieldPredString(a.Pred)
	}
	return fieldPredString(a.Pred)
}
