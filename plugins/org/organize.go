package org

import (
	"context"
	"encoding/json"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The organize side of headline edits (FDR 0026 slice 3b). organize moves a
// headline between state, priority or date headings, edits its tag atoms,
// its priority and date atoms, and its title trailer; each change is routed
// through the unified codecs (unified.go) to a patch_node body, which
// PatchNode then applies under the lock with its read-back (edit.go).

var (
	_ cutting_garden_plugins.FacetWriteDescriber    = (*Plugin)(nil)
	_ cutting_garden_plugins.FacetWriteApplier      = (*Plugin)(nil)
	_ cutting_garden_plugins.MembershipWriteApplier = (*Plugin)(nil)
	_ cutting_garden_plugins.FieldWriteApplier      = (*Plugin)(nil)
	_ cutting_garden_plugins.FieldPresenter         = (*Plugin)(nil)
)

// BuildFacetWritePatch moves a headline into toBucket of a state,
// priority or date dimension; an empty toBucket clears a date.
func (Plugin) BuildFacetWritePatch(
	_ context.Context, node cutting_garden_plugins.Node, write cutting_garden_plugins.FacetWrite, toBucket string,
) ([]byte, error) {
	updates, err := cutting_garden_plugins.ParseUnifiedBucketMove(
		headlineCodecs(), write.DimensionKey, toBucket, node.Fields)
	return patchBody(node, updates, err)
}

// BuildMembershipWritePatch replaces a headline's effective tag set; its
// own tags become the set minus the inherited ones.
func (Plugin) BuildMembershipWritePatch(
	_ context.Context, node cutting_garden_plugins.Node, write cutting_garden_plugins.FacetWrite, newTags []string,
) ([]byte, error) {
	updates, err := cutting_garden_plugins.ParseUnifiedMembershipWrite(
		headlineCodecs(), write.DimensionKey, newTags, node.Fields)
	return patchBody(node, updates, err)
}

// BuildFieldWritePatch applies edited box atoms (priority, scheduled,
// deadline) and the title trailer.
func (Plugin) BuildFieldWritePatch(
	_ context.Context, node cutting_garden_plugins.Node, edits []cutting_garden_plugins.FieldEdit,
) ([]byte, error) {
	updates, err := cutting_garden_plugins.ParseUnifiedFieldEdits(headlineCodecs(), edits, node.Fields)
	return patchBody(node, updates, err)
}

// PresentBoxAtoms renders a headline's priority and dates as box atoms.
func (Plugin) PresentBoxAtoms(node cutting_garden_plugins.Node) []cutting_garden_plugins.BoxAtom {
	if node.Type != typeHeadline {
		return nil
	}
	return cutting_garden_plugins.PresentUnifiedAtoms(headlineCodecs(), node)
}

func patchBody(node cutting_garden_plugins.Node, updates map[string]any, err error) ([]byte, error) {
	if err != nil {
		// A bad-request root naming the headline, so a failing write among
		// many says which one refused.
		return nil, errors.BadRequestf("org plugin: %s: %s", node.URIString(), err)
	}
	if len(updates) == 0 {
		return nil, errors.BadRequestf("org plugin: %s: nothing to write", node.URIString())
	}
	return json.Marshal(updates)
}
