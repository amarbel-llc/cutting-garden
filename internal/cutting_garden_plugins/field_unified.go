package cutting_garden_plugins

import "time"

// The unified field-codec model (FDR 0025): ONE field-with-codec abstraction that
// supersedes the two parallel plugin data models — FacetDimension (the groupable
// heading surface, RFC 0012) and ListingField + BoxAtom (the inline atom surface,
// FDR 0023). A plugin declares its node types' fields ONCE as UnifiedFields; the
// renderer decides from Groupable/Inline whether each shows as a grouping heading,
// an inline box atom, or both — dissolving the heading/atom redundancy
// (cutting-garden#229). A Codec bridges the PRESENTATION fields declared here to the
// STORED substrate fields, reversibly and M<->N (a caldav DTSTART splits into
// date_start + time_start; a banded PRIORITY presents as both a groupable band and
// an inline integer).
//
// Slice 1 (this file + codec.go) introduces the TYPES and the reusable IdentityCodec
// only. Nothing consumes them yet — the legacy FacetDimension/ListingField surface is
// unchanged, so this is additive and behaviour-neutral. The derivation helpers that
// reproduce the legacy surface from a unified declaration, and caldav's migration to
// declare it, are the following slices.

// FieldKind classifies a unified field's value shape. It unifies FacetKind
// (categorical / numeric-bucket / labelled) with the presentation notions the codec
// model adds (date, tag, free text), so one enum spans both former surfaces.
type FieldKind string

const (
	// FieldCategorical is a plain discrete bucket (status, component, domain) —
	// the FacetCategorical carry-over.
	FieldCategorical FieldKind = "categorical"
	// FieldNumericBucket is a number quantized to an ordered bucket whose values
	// carry FieldValue.Order (a size band, priority band) — the FacetNumericBucket
	// carry-over.
	FieldNumericBucket FieldKind = "numeric-bucket"
	// FieldLabelled is an opaque stable key whose human name is resolved out of band
	// (a feed id, an account id) — the FacetLabelled carry-over.
	FieldLabelled FieldKind = "labelled"
	// FieldDate is a calendar date (optionally with a clock), presented split into a
	// date and time component by a SplitDateTime-style codec.
	FieldDate FieldKind = "date"
	// FieldTag is a multi-valued category membership (iCalendar CATEGORIES, a
	// carddav group, a fastmail label). MultiValued is set; a tag-interpreter codec
	// maps segment hierarchy to grouping buckets (cutting-garden#231).
	FieldTag FieldKind = "tag"
	// FieldText is free text (a summary / description trailer) — diffed at the word
	// level, never bucketed.
	FieldText FieldKind = "text"
)

// FieldValue is one declared bucket/enum value of a unified field — the
// presentation-layer counterpart of FacetValue (RFC 0012 §1). Order renders
// urgency-/chronology-first (descending); zero for an unordered categorical value.
type FieldValue struct {
	// Value is the bucket identifier within the field: what a grouping heading
	// pre-renders and what a predicate matches (e.g. "COMPLETED", "0_must").
	Value string
	// Order sorts declared values (higher first). Zero for unordered categoricals.
	Order int64
}

// UnifiedField describes one PRESENTATION field a codec produces (FDR 0025). It is
// the superset of FacetDimension (Key/Label/Kind/MultiValued/Values/TerminalValues)
// and ListingField (Writable, plus the inline/trailer presentation flags): the
// single declaration from which the renderer and the legacy-surface derivation
// helpers both read.
type UnifiedField struct {
	// Key identifies the field within a node type. MUST be non-empty and unique
	// within the node type's declared fields.
	Key string
	// Label is the human field name for display. MAY be empty (consumers fall back
	// to Key).
	Label string
	// Source names the STORED substrate field this presentation field attributes
	// to — the BoxAtom.Field a rendered atom carries, whose writability the field-
	// edit path gates on. Empty means the atom IS its own field (Key is the stored
	// key, as for a plain location/status atom); a SPLIT atom names its parent
	// stored field (a date_start and time_start both carry Source "dtstart", so an
	// edit to either is governed by that field's declared writability and recombined
	// by the owning codec). Mirrors BoxAtom.Field.
	Source string
	// Kind classifies value shape and ordering.
	Kind FieldKind
	// Groupable declares the field MAY be a grouping dimension (rendered as a
	// `<key>=` heading ladder). The FacetDimension surface derives from the
	// groupable fields.
	Groupable bool
	// Inline declares the field MAY be rendered as an inline box atom
	// (`key=value`). The ListingField/BoxAtom surface derives from the inline
	// fields. A field may be BOTH groupable and inline; the renderer (a later
	// slice) decides which to suppress when grouping BY it (cutting-garden#229).
	Inline bool
	// Trailer marks the node's description trailer — the free text after the box
	// (`- [id] <trailer>`), not a `key=value` atom. At most one field per node type
	// sets it. Implies Inline is irrelevant (the trailer is its own slot).
	Trailer bool
	// Writable declares an edit to this field is written back through its codec,
	// rather than surfaced as a read-only notice. The SINGLE source of field
	// writability (mirrors ListingField.Writable). A groupable+writable field's
	// bucket move and an inline+writable field's atom edit both route through the
	// owning codec's Parse.
	Writable bool
	// MultiValued is true when one node contributes several values (tags). false
	// means at most one value per node. Mirrors FacetDimension.Multi.
	MultiValued bool
	// Values, when non-nil, declares a CLOSED domain (the complete value set, known
	// up front) — the grouping headings pre-rendered in Order. nil is an OPEN domain
	// (values discovered from data).
	Values []FieldValue
	// TerminalValues names the Values marking a node DONE / terminal (caldav VTODO
	// ["COMPLETED","CANCELLED"]; cutting-garden#214). Orthogonal to open/closed.
	TerminalValues []string
	// WriteValues, when non-empty, is the ordered write-side convenience list for a
	// groupable+writable field: the target buckets organize pre-renders as headings
	// (mirrors FacetWrite.Values, which it derives). Independent of Values — a field
	// may be an OPEN read domain (Values nil, e.g. caldav status) yet still declare
	// the enum a write completes against. Empty on a writable CLOSED-domain field
	// means the write list IS the declared Values (DeriveFacetWrites falls back to
	// their keys).
	WriteValues []string
	// CompletionHint is a human/agent note on the plugin-owned completion a write
	// to this field performs (mirrors FacetWrite.CompletionHint, which it derives;
	// e.g. "reschedule-by-move preserves the clock time"). Descriptive only.
	CompletionHint string
	// RevalidateAfter, when nonzero, marks a GROUPABLE field VOLATILE: its
	// bucketing is a function of (data, now), so a memoized summary containing it
	// expires after this duration (mirrors FacetDimension.RevalidateAfter, which it
	// derives; RFC 0012 §11.3 — the volatile field MUST declare a closed Values
	// domain). Zero means pure.
	RevalidateAfter time.Duration
}

// NodeTypeUnifiedFields binds a node type to the codecs producing its unified
// fields (FDR 0025) — the unified counterpart of NodeTypeFacets /
// NodeTypeListingFields. Every presentation field a node type carries comes from
// exactly one codec in Codecs (an identity codec for a plain passthrough field, a
// split codec for a date, a band codec for a banded priority), so the full field
// set is the concatenation of each codec's Fields().
type NodeTypeUnifiedFields struct {
	// Tag is the NodeType.Tag these codecs apply to (ordinarily a leaf type).
	Tag string
	// Codecs are the field codecs for Tag, in display order. Their Fields() keys
	// MUST be unique across the whole slice.
	Codecs []Codec
}

// UnifiedDescriber is the OPTIONAL capability declaring a plugin's unified
// field-codec model (FDR 0025). Probed by type assertion like the other
// schema-describing capabilities. A plugin that implements it can have its legacy
// FacetDescriber / ListingFieldsDescriber / FieldPresenter / write surfaces DERIVED
// from the declaration (the following slices) rather than hand-written.
type UnifiedDescriber interface {
	Plugin

	// DescribeUnified returns one NodeTypeUnifiedFields per node type that declares
	// a unified field-codec model.
	DescribeUnified() []NodeTypeUnifiedFields
}
