package fastmail

import "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"

var _ cutting_garden_plugins.FieldPresenter = (*Plugin)(nil)

// PresentBoxAtoms renders a node's detail fields as organize box atoms
// (FDR 0023) by delegating to the unified declaration (FDR 0025): the D6 box
// interior is `[<threadId> <tags…> from=<addr>] <subject>`, so the only
// inline field — and so the only atom — is `from`; the tag set renders
// key-free through the framework's tag path, and `subject` is the trailer.
// A type with no unified declaration (the raw message leaf) has no atoms.
func (Plugin) PresentBoxAtoms(
	node cutting_garden_plugins.Node,
) []cutting_garden_plugins.BoxAtom {
	return cutting_garden_plugins.PresentUnifiedAtoms(codecsForType(node.Type), node)
}
