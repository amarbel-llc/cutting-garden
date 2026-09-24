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
	"maps"

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
	return NodeTypePresentation{Tag: v.Tag, TagSet: v.TagSet}
}

// declaresPresentation reports whether any presentation member is set.
func (p NodeTypePresentation) declaresPresentation() bool {
	return p.TagSet != nil
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

	tagSets := map[string]int{}
	for _, view := range init.NodeTypes {
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

// ProjectFields returns node with its presented memberships projected into
// Node.Fields — a tag-set type's tag keys under the dimension key — the
// stored values the synthesized codecs Format. A node whose type declares no
// presentation (or carries no value) is returned unchanged; the Fields map
// is cloned before writing, never mutated in place.
func (p Presentation) ProjectFields(node cutting_garden_plugins.Node) cutting_garden_plugins.Node {
	t, ok := p.forType(node.Type)
	if !ok || t.TagSet == nil {
		return node
	}
	values := node.Facets[t.TagSet.Dimension]
	if len(values) == 0 {
		return node
	}
	tags := make([]string, len(values))
	for i, value := range values {
		tags[i] = value.Key
	}
	fields := maps.Clone(node.Fields)
	if fields == nil {
		fields = map[string]any{}
	}
	fields[t.TagSet.Dimension] = tags
	node.Fields = fields
	return node
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
