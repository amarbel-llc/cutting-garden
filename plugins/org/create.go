package org

import (
	"context"
	"encoding/json"
	"maps"
	"slices"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Creation from an organize document (forge organize F10): a `+` box in a
// document anchored at a file becomes a new level-1 headline at the file's
// end. BuildCreateBody turns the merged document fields into the capture body
// CreateChild takes (capture.go), so an organize creation and an agent's
// mutate_nodes create are one write path — the new headline gets a minted
// :ID:, the :CREATED: stamp and the zz-agent_captured tag either way.

var (
	_ cutting_garden_plugins.CreationDescriber = Plugin{}
	_ cutting_garden_plugins.CreateApplier     = Plugin{}
)

// creatableFields are the document fields a new headline can be given: the
// writable headline fields, with the tag set becoming its own tags.
var creatableFields = []string{
	fieldTitle, fieldState, fieldPriority, fieldScheduled, fieldDeadline, fieldTags,
}

// DescribeCreation declares a headline creatable in a file; it needs a title.
func (Plugin) DescribeCreation() []cutting_garden_plugins.NodeTypeCreation {
	return []cutting_garden_plugins.NodeTypeCreation{{
		Tag: typeHeadline, ContainerType: typeFile, Required: []string{fieldTitle},
	}}
}

// BuildCreateBody routes each document field through the headline codec that
// edits it, so a create validates exactly as a later edit would. The
// idempotency key is unused: the :ID: is a minted UUID, and organize's
// creation ledger guards a re-apply.
func (Plugin) BuildCreateBody(
	_ context.Context, typ string, fields map[string][]string, _ string,
) ([]byte, error) {
	if typ != typeHeadline {
		return nil, errors.BadRequestf("org plugin: organize cannot create a %q (creatable: %s)", typ, typeHeadline)
	}
	codecs := map[string]cutting_garden_plugins.Codec{}
	for _, c := range headlineCodecs() {
		for _, f := range c.Fields() {
			codecs[f.Key] = c
		}
	}
	body := map[string]any{}
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		if !slices.Contains(creatableFields, name) {
			return nil, errors.BadRequestf("org plugin: a new headline cannot be given %q", name)
		}
		updates, err := codecs[name].Parse(map[string][]string{name: fields[name]}, nil)
		if err != nil {
			return nil, err
		}
		maps.Copy(body, updates)
	}
	return json.Marshal(body)
}
