package traversal_serve

// The RFC 0013 facet_writes amendment: an out-of-process plugin declares
// which of its facet dimensions are WRITABLE (the wire form of
// FacetWriteDescriber, RFC 0012 §Write mapping), and the HOST builds the
// node.patch body for an organize move or membership change from that
// declaration — there is no plugin-side patch-building RPC. So a wire
// plugin needs nothing beyond the initialize block and a node.patch that
// accepts the two host-built shapes to be organize-writable.

import (
	"encoding/json"
	"fmt"
	"slices"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// FacetWriteView is the wire form of cutting_garden_plugins.FacetWrite: one
// dimension's write mapping. mode is "none" | "one" | "many"; field is
// REQUIRED unless mode is "none". The remaining members are OPTIONAL and
// omitted when zero.
type FacetWriteView struct {
	Dimension         string   `json:"dimension"`
	Mode              string   `json:"mode"`
	Field             string   `json:"field,omitempty"`
	IdentityAffecting bool     `json:"identity_affecting,omitempty"`
	CreationRequired  bool     `json:"creation_required,omitempty"`
	CompletionHint    string   `json:"completion_hint,omitempty"`
	Values            []string `json:"values,omitempty"`
	// Clearable (forge organize F12) opts a one write into the clear shape
	// `{"<field>": null}`; absent ≙ false, so a peer predating it is simply
	// not clearable.
	Clearable bool `json:"clearable,omitempty"`
}

// NodeTypeFacetWritesView is the wire form of
// cutting_garden_plugins.NodeTypeFacetWrites, carried in the initialize
// facet_writes block (parallel to the facets block, keyed by the same tag).
type NodeTypeFacetWritesView struct {
	Tag    string           `json:"tag"`
	Writes []FacetWriteView `json:"writes"`
}

// NodeTypeFacetWritesViewFrom projects one type's write mappings onto the
// wire. An empty Values list projects to absent (the two mean the same:
// no pre-rendered write buckets).
func NodeTypeFacetWritesViewFrom(
	declared cutting_garden_plugins.NodeTypeFacetWrites,
) NodeTypeFacetWritesView {
	view := NodeTypeFacetWritesView{
		Tag:    declared.Tag,
		Writes: make([]FacetWriteView, len(declared.Writes)),
	}

	for i, write := range declared.Writes {
		view.Writes[i] = FacetWriteView{
			Dimension:         write.DimensionKey,
			Mode:              string(write.Mode),
			Field:             write.Field,
			IdentityAffecting: write.IdentityAffecting,
			CreationRequired:  write.CreationRequired,
			CompletionHint:    write.CompletionHint,
			Values:            nilIfEmpty(write.Values),
			Clearable:         write.Clearable,
		}
	}

	return view
}

// ToNodeTypeFacetWrites is the inverse of NodeTypeFacetWritesViewFrom.
func (v NodeTypeFacetWritesView) ToNodeTypeFacetWrites() cutting_garden_plugins.NodeTypeFacetWrites {
	declared := cutting_garden_plugins.NodeTypeFacetWrites{
		Tag:    v.Tag,
		Writes: make([]cutting_garden_plugins.FacetWrite, len(v.Writes)),
	}

	for i, write := range v.Writes {
		declared.Writes[i] = cutting_garden_plugins.FacetWrite{
			DimensionKey:      write.Dimension,
			Mode:              cutting_garden_plugins.FacetWriteMode(write.Mode),
			Field:             write.Field,
			IdentityAffecting: write.IdentityAffecting,
			CreationRequired:  write.CreationRequired,
			CompletionHint:    write.CompletionHint,
			Values:            nilIfEmpty(write.Values),
			Clearable:         write.Clearable,
		}
	}

	return declared
}

func nilIfEmpty(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	return values
}

// ValidateFacetWriteDeclaration is the host's bring-up check of an
// initialize facet_writes block (exported for the conformance driver and
// for a Go peer's own tests). Beyond ValidateFacetWrites' cross-check
// against the facets block (declared type, declared dimension, a field for
// every non-none mode, a known mode), the wire adds what the host itself
// relies on when it builds patches: a many write MUST sit on a multi
// dimension (its body is a set), a dimension is mapped at most once per
// type (the host must know WHICH field to write), and a writable mapping
// requires the mutate capability (node.patch is how it lands).
func ValidateFacetWriteDeclaration(init InitializeResult) error {
	if len(init.FacetWrites) == 0 {
		return nil
	}

	reads := make([]cutting_garden_plugins.NodeTypeFacets, len(init.Facets))
	multi := map[string]map[string]bool{}
	for i, view := range init.Facets {
		reads[i] = view.ToNodeTypeFacets()
		multi[view.Tag] = map[string]bool{}
		for _, dimension := range view.Dimensions {
			multi[view.Tag][dimension.Key] = dimension.Multi
		}
	}

	writes := make(
		[]cutting_garden_plugins.NodeTypeFacetWrites, len(init.FacetWrites),
	)
	for i, view := range init.FacetWrites {
		writes[i] = view.ToNodeTypeFacetWrites()
	}

	if err := cutting_garden_plugins.ValidateFacetWrites(reads, writes); err != nil {
		return err
	}

	mutate := slices.Contains(init.Capabilities, CapMutate)

	for _, declared := range writes {
		seen := map[string]bool{}
		for _, write := range declared.Writes {
			if seen[write.DimensionKey] {
				return fmt.Errorf(
					"facet write: type %q dimension %q is mapped more than once",
					declared.Tag, write.DimensionKey,
				)
			}
			seen[write.DimensionKey] = true

			if write.Mode == cutting_garden_plugins.FacetWriteMany &&
				!multi[declared.Tag][write.DimensionKey] {
				return fmt.Errorf(
					"facet write: type %q dimension %q mode %q requires a"+
						" multi-valued dimension",
					declared.Tag, write.DimensionKey, write.Mode,
				)
			}

			if write.Mode != cutting_garden_plugins.FacetWriteNone && !mutate {
				return fmt.Errorf(
					"facet write: type %q dimension %q is writable (mode %q) but"+
						" the plugin does not advertise %q",
					declared.Tag, write.DimensionKey, write.Mode, CapMutate,
				)
			}
		}
	}

	return nil
}

// HostFacetWritePatch builds the node.patch body for a write:one bucket
// move: `{"<field>": "<bucket>"}` — the one shape a wire plugin declaring a
// one mapping MUST accept on node.patch — or, for an EMPTY bucket (a move
// into the no-value section) on a Clearable write, the clear shape
// `{"<field>": null}` (forge organize F12). Anything else (a non-one mode,
// an empty bucket on a non-clearable write) is a bad request. Exported so a
// Go plugin whose patch format is a flat JSON object can reuse it as its
// own FacetWriteApplier (the testpeer does, keeping linked and wire bodies
// identical).
func HostFacetWritePatch(
	write cutting_garden_plugins.FacetWrite, toBucket string,
) ([]byte, error) {
	if write.Mode != cutting_garden_plugins.FacetWriteOne {
		return nil, errors.BadRequestf(
			"facet write: dimension %q is mode %q; a bucket move needs mode %q",
			write.DimensionKey, write.Mode, cutting_garden_plugins.FacetWriteOne,
		)
	}

	if toBucket == "" {
		if !write.Clearable {
			return nil, errors.BadRequestf(
				"facet write: dimension %q cannot be cleared (its write is not"+
					" declared clearable)", write.DimensionKey,
			)
		}

		return marshalFieldPatch(write.Field, nil)
	}

	return marshalFieldPatch(write.Field, toBucket)
}

// HostMembershipWritePatch builds the node.patch body for a write:many
// membership change: `{"<field>": [<complete new set>]}`. The array is a
// FULL replacement (the MembershipWriteApplier contract); an empty or nil
// set encodes as `[]`, clearing the dimension. A non-many mode is a bad
// request.
func HostMembershipWritePatch(
	write cutting_garden_plugins.FacetWrite, newTags []string,
) ([]byte, error) {
	if write.Mode != cutting_garden_plugins.FacetWriteMany {
		return nil, errors.BadRequestf(
			"facet write: dimension %q is mode %q; a membership set needs mode %q",
			write.DimensionKey, write.Mode, cutting_garden_plugins.FacetWriteMany,
		)
	}

	if newTags == nil {
		newTags = []string{}
	}

	return marshalFieldPatch(write.Field, newTags)
}

func marshalFieldPatch(field string, value any) ([]byte, error) {
	if field == "" {
		return nil, errors.BadRequestf("facet write: mapping names no field")
	}

	body, err := json.Marshal(map[string]any{field: value})
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return body, nil
}
