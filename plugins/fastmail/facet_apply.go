package fastmail

import (
	"context"
	"encoding/json"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

var _ cutting_garden_plugins.MembershipWriteApplier = (*Plugin)(nil)

// BuildMembershipWritePatch turns the COMPLETE tag set the organize engine's
// interpreter resolved into the PatchNode body PatchNode then applies. It
// routes the set through the unified field-codec model (FDR 0025): the SDK
// helper finds the codec owning the grouped dimension on the node's type, and
// tagsCodec.Parse persists a full-set replacement — so the body is just
// `{"tags": [...]}` and the plugin has exactly one place that knows the shape.
//
// node's live Fields are passed as the codec's `current`, which tagsCodec
// ignores: the replacement is absolute, and PatchNode re-reads the live
// members and mailbox tree anyway (a stale listing must never decide which
// memberships change).
//
// There is deliberately NO BuildFacetWritePatch sibling: `tags` is fastmail's
// only writable dimension and it is multi-valued, so no `write:one` bucket
// move exists to build. The organize engine's membership path needs only
// NodeMutator + FacetWriteDescriber + MembershipWriteApplier.
func (Plugin) BuildMembershipWritePatch(
	ctx context.Context,
	node cutting_garden_plugins.Node,
	write cutting_garden_plugins.FacetWrite,
	newTags []string,
) ([]byte, error) {
	updates, err := cutting_garden_plugins.ParseUnifiedMembershipWrite(
		codecsForType(node.Type), write.DimensionKey, newTags, node.Fields,
	)
	if err != nil {
		// Reclassify as a bad-request ROOT rather than wrapping: dewey renders
		// only the root's message, and the node URI must reach the user so a
		// failing write among many names WHICH thread refused.
		return nil, errors.BadRequestf("fastmail plugin: %s: %s", node.URIString(), err)
	}
	return json.Marshal(updates)
}
