package traversal_serve_testpeer

import (
	"context"
	"os"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/traversal_serve"
)

// The leaf type's writable facets (the RFC 0013 facet_writes amendment's
// fixture): state is a status-like write:one enum written through the
// `state` field; tag is a labels-like write:many set written through the
// `tags` field — deliberately a different name than the dimension, so a
// host that wrote the dimension key instead of the declared field would
// be caught. month is declared read-only (mode none); feed is unmapped.
const (
	StateWriteField = "state"
	TagWriteField   = "tags"
)

// DescribeFacetWrites is the FacetWriteDescriber capability. With
// UndeclaredFacetWriteEnv set it also claims a write for a dimension the
// facets block never declares — the unusable declaration the host rejects.
func (p *TreePlugin) DescribeFacetWrites() []cutting_garden_plugins.NodeTypeFacetWrites {
	writes := []cutting_garden_plugins.FacetWrite{
		{
			DimensionKey: "state",
			Mode:         cutting_garden_plugins.FacetWriteOne,
			Field:        StateWriteField,
			Values:       []string{"open", "closed"},
		},
		{
			DimensionKey: "month",
			Mode:         cutting_garden_plugins.FacetWriteNone,
		},
		{
			DimensionKey: "tag",
			Mode:         cutting_garden_plugins.FacetWriteMany,
			Field:        TagWriteField,
		},
	}

	if os.Getenv(UndeclaredFacetWriteEnv) != "" {
		writes = append(writes, cutting_garden_plugins.FacetWrite{
			DimensionKey: UndeclaredFacetWriteDimension,
			Mode:         cutting_garden_plugins.FacetWriteOne,
			Field:        "nowhere",
		})
	}

	return []cutting_garden_plugins.NodeTypeFacetWrites{
		{Tag: LeafType, Writes: writes},
	}
}

// BuildFacetWritePatch is the FacetWriteApplier. It builds exactly the
// host-defined shape a wire host sends (traversal_serve.HostFacetWritePatch),
// so the linked and wire paths put the same bytes on node.patch — the
// indistinguishability the amendment's e2e pins.
func (p *TreePlugin) BuildFacetWritePatch(
	_ context.Context,
	_ cutting_garden_plugins.Node,
	write cutting_garden_plugins.FacetWrite,
	toBucket string,
) ([]byte, error) {
	return traversal_serve.HostFacetWritePatch(write, toBucket)
}

// BuildMembershipWritePatch is the MembershipWriteApplier, likewise the
// host-defined full-set shape (traversal_serve.HostMembershipWritePatch).
func (p *TreePlugin) BuildMembershipWritePatch(
	_ context.Context,
	_ cutting_garden_plugins.Node,
	write cutting_garden_plugins.FacetWrite,
	newTags []string,
) ([]byte, error) {
	return traversal_serve.HostMembershipWritePatch(write, newTags)
}

// facetUpdatesFromPatch maps a patch body's facet-write fields onto the
// dimensions they move, for a node of type typ: a one field must carry a
// non-empty string (the new bucket); a many field an array of strings (the
// COMPLETE new set — full replacement, [] clears). A JSON null reads as
// "not supplied" and moves nothing. Any other value on a mapped field is a
// recognized key with an unusable value: a bad request (-32602), never a
// silently dropped field.
func (p *TreePlugin) facetUpdatesFromPatch(
	key, typ string, fields map[string]any,
) (map[string][]cutting_garden_plugins.FacetValue, error) {
	updates := map[string][]cutting_garden_plugins.FacetValue{}

	for _, declared := range p.DescribeFacetWrites() {
		if declared.Tag != typ {
			continue
		}

		for _, write := range declared.Writes {
			if write.Mode == cutting_garden_plugins.FacetWriteNone {
				continue
			}

			value, present := fields[write.Field]
			if !present || value == nil {
				continue
			}

			switch write.Mode {
			case cutting_garden_plugins.FacetWriteOne:
				bucket, ok := value.(string)
				if !ok || bucket == "" {
					return nil, errors.BadRequestf(
						"patch %s: %q must be a non-empty string", key, write.Field,
					)
				}
				updates[write.DimensionKey] = []cutting_garden_plugins.FacetValue{
					{Key: bucket},
				}

			case cutting_garden_plugins.FacetWriteMany:
				items, ok := value.([]any)
				if !ok {
					return nil, errors.BadRequestf(
						"patch %s: %q must be an array of strings", key, write.Field,
					)
				}
				set := make([]cutting_garden_plugins.FacetValue, len(items))
				for i, item := range items {
					member, ok := item.(string)
					if !ok || member == "" {
						return nil, errors.BadRequestf(
							"patch %s: %q must be an array of non-empty strings",
							key, write.Field,
						)
					}
					set[i] = cutting_garden_plugins.FacetValue{Key: member}
				}
				updates[write.DimensionKey] = set
			}
		}
	}

	return updates, nil
}
