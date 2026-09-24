package traversal_serve

// The RFC 0013 presentation additions (forge organize F11): per node type, a
// wire plugin MAY declare how organize presents its nodes — a tag_set (which
// multi-valued facet dimension is the node's tag set, under which tag
// interpreter). The declarations ride the type's node_types entry.
//
// The host does NOT grow a wire-specific organize path for them. Instead the
// adapter SYNTHESIZES the minimal linked-plugin surface the framework already
// consults — a UnifiedDescriber declaring one FieldTag field per tag-set type,
// and each listed node's tag memberships projected into Node.Fields under the
// dimension key — so node_view's tag presenter, organize's tag atoms and
// membership writes, trellis bare-tag terms, `list -format json` tags and the
// mcp tag_set all run their ONE linked code path over a wire plugin.
// Presentation holds that synthesis; a Go peer can reuse it to present
// itself identically when linked (the testpeer does).

import (
	"encoding/json"
	"maps"
	"slices"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// TagSetView is the wire form of a node type's tag set: the facet dimension
// whose values ARE the node's tags, and the name of the tag interpreter
// (RFC 0019) governing them. Both are REQUIRED; the dimension MUST be a
// multi-valued dimension the type's facets entry declares, and the
// interpreter MUST be one the host knows (naive, dodder-hyphen).
type TagSetView struct {
	Dimension   string `json:"dimension"`
	Interpreter string `json:"interpreter"`
}

// NodeTypePresentation is one node type's presentation declaration — what a
// Go peer returns from PresentationDescriber and what the host reads back off
// the node_types entry. Every member is OPTIONAL.
type NodeTypePresentation struct {
	Tag    string
	TagSet *TagSetView
	// InlineFields are single-valued facet dimensions of the type rendered, in
	// this order, as inline `name=value` box atoms; the value is the node's
	// facet value key in that dimension (no value, no atom). An inline field
	// with a one facet write is editable through it; otherwise read-only.
	InlineFields []string
	// TrailerField is the node.patch key the box trailer writes: the trailer
	// SHOWS the node's name, and an edit to it is sent as
	// `{"<TrailerField>": "<text>"}`. Empty keeps the trailer read-only.
	TrailerField string
}

// PresentationDescriber is the OPTIONAL Go-peer capability Serve advertises
// on the node_types entries (RFC 0013 presentation additions). It has no
// linked-plugin meaning of its own: a linked plugin declares the same things
// through UnifiedDescriber; a Go peer that wants to be indistinguishable
// linked and over the wire builds its linked surface from the same
// declaration with NewPresentation.
type PresentationDescriber interface {
	cutting_garden_plugins.Plugin

	DescribePresentation() []NodeTypePresentation
}

// presentation reads a node_types entry's presentation members back.
func (v NodeTypeView) presentation() NodeTypePresentation {
	return NodeTypePresentation{
		Tag:          v.Tag,
		TagSet:       v.TagSet,
		InlineFields: v.InlineFields,
		TrailerField: v.TrailerField,
	}
}

// declaresPresentation reports whether any presentation member is set.
func (p NodeTypePresentation) declaresPresentation() bool {
	return p.TagSet != nil || len(p.InlineFields) > 0 || p.TrailerField != ""
}

// PresentationsOf collects the presentation-declaring node types of an
// initialize result, in node_types order (exported for the conformance
// driver).
func PresentationsOf(init InitializeResult) []NodeTypePresentation {
	var out []NodeTypePresentation
	for _, view := range init.NodeTypes {
		if p := view.presentation(); p.declaresPresentation() {
			out = append(out, p)
		}
	}
	return out
}

// applyPresentations writes a Go peer's presentation declarations onto its
// node_types entries. A declaration naming a type the plugin does not
// declare has no entry to ride on and is refused, so a peer bug never
// silently drops a presentation.
func applyPresentations(views []NodeTypeView, declared []NodeTypePresentation) error {
	for _, p := range declared {
		found := false
		for i := range views {
			if views[i].Tag != p.Tag {
				continue
			}
			views[i].TagSet = p.TagSet
			views[i].InlineFields = p.InlineFields
			views[i].TrailerField = p.TrailerField
			found = true
		}
		if !found {
			return errors.ErrorWithStackf(
				"traversal serve: presentation: type %q is not a declared node type",
				p.Tag,
			)
		}
	}
	return nil
}

// ValidatePresentationDeclaration is the host's bring-up check of the
// presentation members of an initialize result's node_types entries
// (exported for the conformance driver and a Go peer's own tests). A tag_set
// MUST name a multi-valued dimension the type's facets entry declares and a
// tag interpreter this host knows; a type carries at most one tag set.
func ValidatePresentationDeclaration(init InitializeResult) error {
	dims := map[string]map[string]FacetDimensionView{}
	for _, view := range init.Facets {
		if dims[view.Tag] == nil {
			dims[view.Tag] = map[string]FacetDimensionView{}
		}
		for _, dim := range view.Dimensions {
			dims[view.Tag][dim.Key] = dim
		}
	}

	mutate := slices.Contains(init.Capabilities, CapMutate)

	tagSets := map[string]int{}
	for _, view := range init.NodeTypes {
		if err := validateInlineFields(view, dims[view.Tag]); err != nil {
			return err
		}
		if err := validateTrailerField(view, dims[view.Tag], mutate); err != nil {
			return err
		}

		set := view.TagSet
		if set == nil {
			continue
		}
		tagSets[view.Tag]++
		if tagSets[view.Tag] > 1 {
			return errors.ErrorWithStackf(
				"tag set: type %q declares more than one tag set", view.Tag,
			)
		}
		dim, declared := dims[view.Tag][set.Dimension]
		switch {
		case !declared:
			return errors.ErrorWithStackf(
				"tag set: type %q dimension %q is not a declared facet dimension",
				view.Tag, set.Dimension,
			)
		case !dim.Multi:
			return errors.ErrorWithStackf(
				"tag set: type %q dimension %q is not multi-valued",
				view.Tag, set.Dimension,
			)
		case set.Interpreter == "":
			return errors.ErrorWithStackf(
				"tag set: type %q names no interpreter", view.Tag,
			)
		}
		if _, known := cutting_garden_plugins.LookupTagInterpreter(set.Interpreter); !known {
			return errors.ErrorWithStackf(
				"tag set: type %q names interpreter %q, which this host does not know",
				view.Tag, set.Interpreter,
			)
		}
	}

	return nil
}

// validateInlineFields: every inline field is a single-valued facet
// dimension the type declares, listed once.
func validateInlineFields(view NodeTypeView, dims map[string]FacetDimensionView) error {
	seen := map[string]bool{}
	for _, field := range view.InlineFields {
		dim, declared := dims[field]
		switch {
		case seen[field]:
			return errors.ErrorWithStackf(
				"inline fields: type %q field %q is listed more than once",
				view.Tag, field,
			)
		case !declared:
			return errors.ErrorWithStackf(
				"inline fields: type %q field %q is not a declared facet dimension",
				view.Tag, field,
			)
		case dim.Multi:
			return errors.ErrorWithStackf(
				"inline fields: type %q field %q is multi-valued; an inline atom"+
					" carries one value (a multi-valued dimension is a tag_set)",
				view.Tag, field,
			)
		}
		seen[field] = true
	}
	return nil
}

// validateTrailerField: the trailer field is a node.patch key, not a facet
// dimension (the trailer shows the node's name, not a bucket), and — being
// writable by declaration — requires the mutate capability.
func validateTrailerField(
	view NodeTypeView, dims map[string]FacetDimensionView, mutate bool,
) error {
	field := view.TrailerField
	if field == "" {
		return nil
	}
	if _, isDim := dims[field]; isDim {
		return errors.ErrorWithStackf(
			"trailer field: type %q field %q is a facet dimension; the trailer"+
				" writes the node's name, not a bucket",
			view.Tag, field,
		)
	}
	if !mutate {
		return errors.ErrorWithStackf(
			"trailer field: type %q field %q is writable but the plugin does"+
				" not advertise %q",
			view.Tag, field, CapMutate,
		)
	}
	return nil
}

// Presentation is the linked-surface synthesis over a set of presentation
// declarations and the same plugin's facet writes.
type Presentation struct {
	types  []NodeTypePresentation
	writes map[string]map[string]cutting_garden_plugins.FacetWrite
}

// NewPresentation builds the synthesis. writes is the plugin's declared
// facet writes — a tag set is writable exactly when its dimension has a many
// write.
func NewPresentation(
	types []NodeTypePresentation,
	writes []cutting_garden_plugins.NodeTypeFacetWrites,
) Presentation {
	byType := map[string]map[string]cutting_garden_plugins.FacetWrite{}
	for _, declared := range writes {
		if byType[declared.Tag] == nil {
			byType[declared.Tag] = map[string]cutting_garden_plugins.FacetWrite{}
		}
		for _, write := range declared.Writes {
			byType[declared.Tag][write.DimensionKey] = write
		}
	}
	return Presentation{types: types, writes: byType}
}

func (p Presentation) forType(tag string) (NodeTypePresentation, bool) {
	for _, t := range p.types {
		if t.Tag == tag {
			return t, true
		}
	}
	return NodeTypePresentation{}, false
}

// DescribeUnified is the synthesized UnifiedDescriber: one set per tag-set
// type holding ONE FieldTag field keyed by the tag-set dimension, carrying
// its interpreter, writable iff the dimension has a many write. nil when no
// type declares a tag set — the "plugin omits the interface" outcome.
func (p Presentation) DescribeUnified() []cutting_garden_plugins.NodeTypeUnifiedFields {
	var sets []cutting_garden_plugins.NodeTypeUnifiedFields
	for _, t := range p.types {
		if t.TagSet == nil {
			continue
		}
		write := p.writes[t.Tag][t.TagSet.Dimension]
		sets = append(sets, cutting_garden_plugins.NodeTypeUnifiedFields{
			Tag: t.Tag,
			Codecs: []cutting_garden_plugins.Codec{tagSetCodec{
				field: cutting_garden_plugins.UnifiedField{
					Key:         t.TagSet.Dimension,
					Kind:        cutting_garden_plugins.FieldTag,
					Groupable:   true,
					MultiValued: true,
					Writable:    write.Mode == cutting_garden_plugins.FacetWriteMany,
					Interpreter: t.TagSet.Interpreter,
				},
			}},
		})
	}
	return sets
}

// ProjectFields returns node with its presented values projected into
// Node.Fields — the stored values the synthesized surfaces read: a tag-set
// type's tag keys under the dimension key (what the tag codec Formats), each
// inline field's value under its dimension key, and the node's name under the
// trailer field (what organize's trailer reads). A node whose type declares
// no presentation, or that carries none of the values, is returned
// unchanged; the Fields map is cloned before writing, never mutated in place.
func (p Presentation) ProjectFields(node cutting_garden_plugins.Node) cutting_garden_plugins.Node {
	t, ok := p.forType(node.Type)
	if !ok {
		return node
	}

	projected := map[string]any{}
	if t.TagSet != nil {
		if values := node.Facets[t.TagSet.Dimension]; len(values) > 0 {
			tags := make([]string, len(values))
			for i, value := range values {
				tags[i] = value.Key
			}
			projected[t.TagSet.Dimension] = tags
		}
	}
	for _, field := range t.InlineFields {
		if values := node.Facets[field]; len(values) > 0 {
			projected[field] = values[0].Key
		}
	}
	if t.TrailerField != "" {
		projected[t.TrailerField] = node.Name
	}
	if len(projected) == 0 {
		return node
	}

	fields := maps.Clone(node.Fields)
	if fields == nil {
		fields = map[string]any{}
	}
	maps.Copy(fields, projected)
	node.Fields = fields
	return node
}

// PresentBoxAtoms is the synthesized FieldPresenter: each inline field with a
// value, in declared order, as a `name=value` atom named by its dimension.
func (p Presentation) PresentBoxAtoms(node cutting_garden_plugins.Node) []cutting_garden_plugins.BoxAtom {
	t, ok := p.forType(node.Type)
	if !ok {
		return nil
	}
	var atoms []cutting_garden_plugins.BoxAtom
	for _, field := range t.InlineFields {
		if values := node.Facets[field]; len(values) > 0 {
			atoms = append(atoms, cutting_garden_plugins.BoxAtom{
				Name: field, Value: values[0].Key,
			})
		}
	}
	return atoms
}

// DescribeListingFields is the synthesized ListingFieldsDescriber — the
// single source of organize's field writability: each inline field
// (writable iff its dimension has a one write), then the trailer field
// (writable, the Trailer slot). nil when no type declares either.
func (p Presentation) DescribeListingFields() []cutting_garden_plugins.NodeTypeListingFields {
	var sets []cutting_garden_plugins.NodeTypeListingFields
	for _, t := range p.types {
		var fields []cutting_garden_plugins.ListingField
		for _, field := range t.InlineFields {
			fields = append(fields, cutting_garden_plugins.ListingField{
				Key:      field,
				Writable: p.writes[t.Tag][field].Mode == cutting_garden_plugins.FacetWriteOne,
			})
		}
		if t.TrailerField != "" {
			fields = append(fields, cutting_garden_plugins.ListingField{
				Key: t.TrailerField, Writable: true, Trailer: true,
			})
		}
		if len(fields) > 0 {
			sets = append(sets, cutting_garden_plugins.NodeTypeListingFields{
				Tag: t.Tag, Fields: fields,
			})
		}
	}
	return sets
}

// BuildFieldWritePatch is the synthesized FieldWriteApplier: ONE node.patch
// body for a node's batch of box edits. The trailer edit writes
// `"<trailer_field>": "<text>"`; an inline atom edit writes through its
// dimension's one write exactly as a bucket move does (HostFacetWritePatch:
// `"<field>": "<value>"`, or null for the clear of a clearable write). A
// read-only or undeclared atom, a non-clearable clear, and an empty batch are
// bad requests.
func (p Presentation) BuildFieldWritePatch(
	node cutting_garden_plugins.Node, edits []cutting_garden_plugins.FieldEdit,
) ([]byte, error) {
	if len(edits) == 0 {
		return nil, errors.BadRequestf("field write: %s: no edits", node.URIString())
	}
	t, _ := p.forType(node.Type)

	body := map[string]any{}
	for _, edit := range edits {
		if t.TrailerField != "" && edit.Name == t.TrailerField {
			body[t.TrailerField] = edit.Value
			continue
		}

		write, declared := p.writes[t.Tag][edit.Name]
		if !slices.Contains(t.InlineFields, edit.Name) || !declared ||
			write.Mode != cutting_garden_plugins.FacetWriteOne {
			return nil, errors.BadRequestf(
				"field write: type %q field %q is not writable (no inline field"+
					" with a one facet write, and not the trailer)",
				node.Type, edit.Name,
			)
		}

		one, err := HostFacetWritePatch(write, edit.Value)
		if err != nil {
			return nil, err
		}
		var fragment map[string]any
		if err := json.Unmarshal(one, &fragment); err != nil {
			return nil, errors.Wrap(err)
		}
		maps.Copy(body, fragment)
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return encoded, nil
}

// tagSetCodec is the synthesized codec for a tag-set dimension: the stored
// value (ProjectFields' projection) IS the presented tag set.
type tagSetCodec struct {
	field cutting_garden_plugins.UnifiedField
}

func (c tagSetCodec) Fields() []cutting_garden_plugins.UnifiedField {
	return []cutting_garden_plugins.UnifiedField{c.field}
}

func (c tagSetCodec) Format(stored map[string]any) (map[string][]string, error) {
	presented := map[string][]string{}
	if tags := cutting_garden_plugins.StringsOf(stored, c.field.Key); len(tags) > 0 {
		presented[c.field.Key] = tags
	}
	return presented, nil
}

func (c tagSetCodec) Parse(
	edited map[string][]string, _ map[string]any,
) (map[string]any, error) {
	updates := map[string]any{}
	if tags, ok := edited[c.field.Key]; ok {
		updates[c.field.Key] = tags
	}
	return updates, nil
}
