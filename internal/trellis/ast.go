// Package trellis implements a hand-rolled recursive-descent parser for the
// trellis query language (RFC 0014, docs/rfcs/0014-trellis-query-language.md).
// It produces an AST only: no evaluation, no validation beyond what the
// grammar itself demands. Forms the grammar reserves for later semantics
// (the `~=` field operator, the `-[pred]->>` typed-transitive-closure
// combinator) parse successfully into dedicated AST values so a later
// validation layer can reject them by inspecting the AST — this package
// never errors on them itself.
//
// Every exported type here mirrors one production of the normative grammar,
// docs/rfcs/0014-trellis.peg; see that file for the authoritative rule
// bodies and rationale, and its trailing conformance-vector comments (this
// package's tests parse every one of them — RFC 0014's Examples).
package trellis

// Query <- SP? Path SP? EOF
//
// The parse of one complete trellis query string.
type Query struct {
	Path Path
}

// Path <- (Combinator SP)? Step (SP Combinator SP Step)*
//
// A sequence of Steps joined by Combinators. len(Combinators) is always
// len(Steps)-1; Combinators[i] joins Steps[i] to Steps[i+1]. The result set
// of a query is the objects matched by the LAST step (RFC 0014 "Query
// structure") — this package does not compute that; it only parses.
//
// Leading is the optional `(Combinator SP)?` prefix (grammar amended for
// cutting-garden#152): when non-nil it is a combinator applied to the
// IMPLICIT DEFAULT ANCHOR — the root aggregate, FDR 0022 "roots as nodes" —
// so the path originates from that default anchor rather than from an
// explicit first Step. nil means the path begins directly at Steps[0] with
// no default-anchor origin. A leading combinator is NOT one of the interior
// Combinators: Steps[0] is still the first explicit step, and the
// len(Combinators) == len(Steps)-1 invariant counts only the combinators
// BETWEEN explicit steps.
type Path struct {
	Leading     *Combinator // (Combinator SP)? — default-anchor origin; nil when absent
	Steps       []Step
	Combinators []Combinator
}

// Step <- Term (SP !Combinator Term)*
//
// A maximal run of space-separated Terms, ANDed, all predicating the same
// (object, version) pair.
type Step struct {
	Terms []Term
}

// ConjRun <- Term (SP !Combinator Term)*
//
// Structurally identical to Step (a space-separated AND-run of Terms) but a
// distinct grammar rule: ConjRun is one alternative of an OR-group
// (Alternatives) or the content of a typed-edge predicate bracket, never a
// top-level step. Kept as its own type for grammar fidelity and because the
// "last step" result-set rule (Path) must not confuse the two: subpaths and
// OR-alternatives never count as top-level steps.
type ConjRun struct {
	Terms []Term
}

// Term <- '^'? '='? BasicTerm
//
// `^` negates the term (or, when Basic is a Group, the whole group); `=`
// forces exact (non-prefix) matching. Both are doddish-inherited prefixes
// applying to the BasicTerm they decorate.
type Term struct {
	Negate bool // leading '^'
	Exact  bool // leading '='
	Basic  BasicTerm
}

// BasicTerm is the sum type of BasicTerm's nine grammar alternatives:
//
//	BasicTerm <- Group
//	           / Qualifier
//	           / FieldPred
//	           / TypeTerm Sigil?
//	           / DigestTerm Sigil?
//	           / MarklTerm Sigil?
//	           / QuotedRef Sigil?
//	           / Ident Sigil?
//	           / Sigil
//
// Each alternative below is a dedicated struct implementing this interface;
// the five "X Sigil?" alternatives carry an optional trailing Sigil that
// scopes the enclosing Step to a version-set (RFC 0014 "Terms").
type BasicTerm interface{ isBasicTerm() }

// GroupBasicTerm is BasicTerm's `Group` alternative.
type GroupBasicTerm struct{ Group Group }

// QualifierBasicTerm is BasicTerm's `Qualifier` alternative: a parenthetical
// standing as its own term (`(tags)`), a META QUALIFIER rather than a
// predicate over objects (native tags design G10). Reserved in query
// position — the evaluator's validation layer rejects it; organize's
// group-by spelling is its consumer.
type QualifierBasicTerm struct{ Qualifier Qualifier }

// FieldPredBasicTerm is BasicTerm's `FieldPred` alternative.
type FieldPredBasicTerm struct{ FieldPred FieldPred }

// TypeBasicTerm is BasicTerm's `TypeTerm Sigil?` alternative.
type TypeBasicTerm struct {
	Type  TypeTerm
	Sigil *Sigil // nil when absent
}

// DigestBasicTerm is BasicTerm's `DigestTerm Sigil?` alternative.
type DigestBasicTerm struct {
	Digest DigestTerm
	Sigil  *Sigil
}

// MarklBasicTerm is BasicTerm's `MarklTerm Sigil?` alternative.
type MarklBasicTerm struct {
	Markl MarklTerm
	Sigil *Sigil
}

// QuotedRefBasicTerm is BasicTerm's `QuotedRef Sigil?` alternative: a quoted
// string in identifier position, the opaque-reference escape hatch for
// content containing reserved runes.
type QuotedRefBasicTerm struct {
	Ref   QuotedRef
	Sigil *Sigil
}

// IdentBasicTerm is BasicTerm's `Ident Sigil?` alternative — the common
// case, an opaque object reference whose tag-vs-id semantics resolve
// through the type system at evaluation, never from token shape.
type IdentBasicTerm struct {
	Ident Ident
	Sigil *Sigil
}

// SigilBasicTerm is BasicTerm's bare `Sigil` alternative: a sigil standing
// as its own term (doddish's bare `:`), scoping the step without adding a
// predicate.
type SigilBasicTerm struct{ Sigil Sigil }

func (GroupBasicTerm) isBasicTerm()     {}
func (QualifierBasicTerm) isBasicTerm() {}
func (FieldPredBasicTerm) isBasicTerm() {}
func (TypeBasicTerm) isBasicTerm()      {}
func (DigestBasicTerm) isBasicTerm()    {}
func (MarklBasicTerm) isBasicTerm()     {}
func (QuotedRefBasicTerm) isBasicTerm() {}
func (IdentBasicTerm) isBasicTerm()     {}
func (SigilBasicTerm) isBasicTerm()     {}

// String renders the term back in its grammar spelling, `(name)`.
func (q QualifierBasicTerm) String() string { return q.Qualifier.String() }

// Qualifier <- '(' Ident ')'
//
// A parenthesized identifier: a meta qualifier ON a term rather than a
// predicate over objects. It appears in two positions — as a BasicTerm on
// its own (`(tags)`: the type's whole tag set) and as a FieldPred Value
// (`date_due=(month)`: a value hole carrying a granularity qualifier). Both
// are the native-tags group-by spellings (design G10); in query position
// both are RESERVED and rejected by trellis_eval's validation.
type Qualifier struct{ Name string }

// String renders the qualifier in its grammar spelling, `(name)` — the
// writer half of the parse round-trip.
func (q Qualifier) String() string { return "(" + q.Name + ")" }

// Qualifier is also Value's `Qualifier` alternative: `k=(x)` is a FieldPred
// whose value is a value HOLE with a meta qualifier (`date_due=(month)` —
// group the field at month granularity), not a literal to compare against.
func (Qualifier) isValue() {}

// TypeTerm <- '!' Ident
//
// Type identity (not genre; dodder FDR 0018) — `!task`.
type TypeTerm struct{ Name string }

// DigestTerm <- '@' Ident
//
// Blob-identity predicate, the content-addressed analog of exact match.
type DigestTerm struct{ Digest string }

// QuotedRef <- String
//
// A quoted string parsed where an identifier is expected: an opaque
// reference (the escape hatch for content containing reserved runes, e.g.
// `"one/uno.zettel"`, a URI with a query string).
type QuotedRef struct{ Value string } // decoded string content

// MarklTerm <- (String / Ident) '@' Ident
//
// A purpose-full markl id, decomposed at the grammar level as a structured
// two-slot term: a purpose (bare identifier or quoted string) joined by '@'
// to a digest. The digest is always structurally visible — never trapped
// inside string quoting (`"my thing"@blake2b256-…`, never
// `"my thing@blake2b256-…"`).
type MarklTerm struct {
	Purpose       string // decoded purpose text
	PurposeQuoted bool   // true when the purpose slot was a quoted String
	Digest        string
}

func (MarklTerm) isValue() {}

// Ident <- IdentRune+
//
// An opaque object reference. Sigil runes are identifier-interior unless
// term-final (the strict sigil rule; see IsIdentRuneAt in lex.go), so
// `caldav:fastmail`, `web:http://example.com`, and `12.7` are single
// identifiers.
type Ident struct{ Name string }

// Bareword <- IdentRune+
//
// Value's catch-all identifier-shaped token. Lexically identical to Ident
// (same IdentRune+ production) but a distinct grammar rule: Bareword only
// ever appears as a ValueList/Value element, never as a BasicTerm or field
// name.
type Bareword struct{ Name string }

func (Bareword) isValue() {}

// Sigil <- SigilRune+
//
// Chooses the step's (or version subpath's) candidate version-set;
// combinable (`:.`).
//   - `:` latest (default)     — `+` history / captured revisions
//   - `.` external / local materializations
//   - `?` hidden (host-defined)
type Sigil struct{ Runes string }

// Group <- '[' SP? GroupBody SP? ']'
//
// One bracket syntax, three readings (see GroupBody).
type Group struct{ Body GroupBody }

// GroupBody <- SubPath / VersionSub / Alternatives
//
// Disambiguated by the first token: a combinator makes it a SubPath, a
// sigil makes it a VersionSub, anything else is Alternatives
// (OR-alternatives). Each concrete type below implements this interface.
type GroupBody interface{ isGroupBody() }

// SubPath <- Combinator (SP Path)?
//
// A spatial subpath predicate: existential, subject unchanged. Path is nil
// for the empty form (`[->]`, "has any outgoing edge").
type SubPath struct {
	Combinator Combinator
	Path       *Path
}

func (SubPath) isGroupBody() {}

// VersionSub <- Sigil (SP Step)?
//
// A version subpath predicate: existential over this object's revisions in
// the sigil's version-set. Step is nil for the empty form (`[+]`, "has at
// least one recorded revision"). v1 restricts the content to a single Step
// (no combinators inside — deferred, RFC 0014 "Deferred").
type VersionSub struct {
	Sigil Sigil
	Step  *Step
}

func (VersionSub) isGroupBody() {}

// Alternatives <- ConjRun (SP? ',' SP? ConjRun)*
//
// Comma-separated OR-alternatives, each a conjunction run. Extends
// doddish's single-atom alternatives; load-bearing for the trellis/espalier
// isometry (every box-format interior parses as a one-alternative group).
type Alternatives struct {
	Alts []ConjRun // len(Alts) >= 1
}

func (Alternatives) isGroupBody() {}

// FieldPred <- FieldName SP? FieldOp SP? (ValueList / Value)
//
// A field predicate. Values holds exactly one element for a bare Value and
// N elements for a bracketed ValueList (List is true in the latter case,
// even for a syntactically-bracketed singleton) — value lists distribute
// the operator as OR (RFC 0014 "Value lists").
type FieldPred struct {
	Field  FieldName
	Op     FieldOp
	Values []Value // len(Values) >= 1
	List   bool    // true when parsed from a bracketed ValueList
}

// FieldName <- String / Ident
//
// A quoted field name is opaque to the framework: it names a field, never
// a path the evaluator walks.
type FieldName struct {
	Name   string
	Quoted bool
}

// FieldOp <- '*=' / '^=' / '$=' / '!=' / '<=' / '>=' / '~=' / '=' / '<' / '>'
//
// Longest-match first (encoded in the parser's try-order, not here).
type FieldOp int

const (
	FieldOpInvalid  FieldOp = iota
	FieldOpEq               // =
	FieldOpNotEq            // !=
	FieldOpContains         // *=
	FieldOpPrefix           // ^=
	FieldOpSuffix           // $=
	FieldOpLt               // <
	FieldOpLte              // <=
	FieldOpGt               // >
	FieldOpGte              // >=
	// FieldOpRegex is RESERVED (regex matching, deferred): this package
	// parses it like any other operator; a later validation layer rejects
	// it (RFC 0014 "Field operators", trellis.peg's FieldOp comment).
	FieldOpRegex // ~=
)

// String returns the operator's grammar spelling (e.g. "*=" for
// FieldOpContains), primarily for diagnostics and test failure messages.
func (op FieldOp) String() string {
	switch op {
	case FieldOpEq:
		return "="
	case FieldOpNotEq:
		return "!="
	case FieldOpContains:
		return "*="
	case FieldOpPrefix:
		return "^="
	case FieldOpSuffix:
		return "$="
	case FieldOpLt:
		return "<"
	case FieldOpLte:
		return "<="
	case FieldOpGt:
		return ">"
	case FieldOpGte:
		return ">="
	case FieldOpRegex:
		return "~="
	default:
		return "<invalid FieldOp>"
	}
}

// Value <- Qualifier / MarklTerm / String / DigestTerm / Bareword
//
// One element of a FieldPred's value (or value list). MarklTerm and
// DigestTerm implement this directly (Value's grammar carries no trailing
// Sigil, unlike BasicTerm's alternatives); String is represented as
// StringValue and Bareword as the Bareword type above; Qualifier implements
// it directly (a value hole with a meta qualifier, `k=(x)`).
type Value interface{ isValue() }

// StringValue is Value's `String` alternative (decoded content).
type StringValue struct{ Value string }

func (StringValue) isValue() {}

func (DigestTerm) isValue() {}

// Combinator is the typed-arrow traversal operator joining two Steps (Path)
// or heading a SubPath (Group):
//
//	Combinator   <- TypedClosure / TypedFwd / TypedBack
//	              / '->>' / '<<-'
//	              / '->'  / '<-'
type Combinator struct {
	Kind CombinatorKind
	// Pred is the edge predicate for TypedFwd/TypedBack/TypedClosure
	// (`-[pred]->`, `<-[pred]-`, `-[pred]->>`); nil otherwise.
	Pred *ConjRun
}

// CombinatorKind discriminates Combinator's seven forms.
type CombinatorKind int

const (
	CombinatorInvalid CombinatorKind = iota
	// CombinatorFwd is `->`: follow any arrow forward, one hop.
	CombinatorFwd
	// CombinatorBack is `<-`: follow any arrow backward, one hop.
	CombinatorBack
	// CombinatorFwdClosure is `->>`: transitive closure forward,
	// one-or-more hops, visited-set deduplication.
	CombinatorFwdClosure
	// CombinatorBackClosure is `<<-`: transitive closure backward.
	CombinatorBackClosure
	// CombinatorTypedFwd is `-[pred]->`: restrict the forward hop by a
	// compound predicate evaluated against the edge.
	CombinatorTypedFwd
	// CombinatorTypedBack is `<-[pred]-`: restrict the backward hop.
	CombinatorTypedBack
	// CombinatorTypedClosure is `-[pred]->>`, RESERVED: typed transitive
	// closure — spelling reserved, semantics deferred (RFC 0014
	// "Deferred", walkthrough #6). This package parses it into a
	// Combinator with this Kind; a later validation layer rejects it.
	CombinatorTypedClosure
)
