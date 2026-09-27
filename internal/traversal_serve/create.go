package traversal_serve

// The RFC 0013 creation declaration (forge organize F10): a node type MAY
// declare itself `creatable` on its node_types entry, naming the container
// type it is created under and the document fields a new node requires. The
// HOST then builds the node.create_child body from an organize document's
// merged new object — reusing the facet_writes fields and the trailer_field as
// the field mapping, exactly as a patch does — so a wire plugin needs nothing
// beyond the declaration and a node.create_child accepting that body to be
// organize-creatable. There is no plugin-side body-building RPC (reserved, as
// for patches — §Facet writes).

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// CreatableView is the wire form of cutting_garden_plugins.NodeTypeCreation,
// carried as the OPTIONAL `creatable` member of the created type's node_types
// entry. Container is REQUIRED: the node_types tag of the container type a
// new node is created under. Required names the document fields a new node
// MUST be given (facet dimension keys or the type's trailer_field); absent ≙
// none.
type CreatableView struct {
	Container string   `json:"container"`
	Required  []string `json:"required,omitempty"`
}

// CreationsOf collects the creatable node types of an initialize result, in
// node_types order (exported for the conformance driver).
func CreationsOf(init InitializeResult) []cutting_garden_plugins.NodeTypeCreation {
	var out []cutting_garden_plugins.NodeTypeCreation
	for _, view := range init.NodeTypes {
		if view.Creatable == nil {
			continue
		}
		out = append(out, cutting_garden_plugins.NodeTypeCreation{
			Tag:           view.Tag,
			ContainerType: view.Creatable.Container,
			Required:      nilIfEmpty(slices.Clone(view.Creatable.Required)),
		})
	}
	return out
}

// applyCreations writes a Go peer's CreationDescriber declarations onto its
// node_types entries. A declaration naming an undeclared type is refused, so a
// peer bug never silently drops a creation.
func applyCreations(
	views []NodeTypeView, declared []cutting_garden_plugins.NodeTypeCreation,
) error {
	for _, c := range declared {
		index := slices.IndexFunc(views, func(v NodeTypeView) bool { return v.Tag == c.Tag })
		if index < 0 {
			return errors.ErrorWithStackf(
				"traversal serve: creation: type %q is not a declared node type", c.Tag,
			)
		}
		views[index].Creatable = &CreatableView{
			Container: c.ContainerType,
			Required:  nilIfEmpty(slices.Clone(c.Required)),
		}
	}
	return nil
}

// ValidateCreationDeclaration is the host's bring-up check of the `creatable`
// members of an initialize result's node_types entries (exported for the
// conformance driver and a Go peer's own tests):
//
//  1. `container` names a declared node_types tag whose entry is a container;
//  2. the plugin advertises `container-create` (creation rides
//     node.create_child);
//  3. the type declares a `trailer_field` (organize requires every new
//     object's description, which is sent under it);
//  4. every `required` entry is a field the host can send: the type's
//     trailer_field or a dimension with a `one` / `many` facet write.
func ValidateCreationDeclaration(init InitializeResult) error {
	creations := CreationsOf(init)
	if len(creations) == 0 {
		return nil
	}

	types := make([]cutting_garden_plugins.NodeType, len(init.NodeTypes))
	for i, view := range init.NodeTypes {
		types[i] = view.ToNodeType()
	}
	if err := cutting_garden_plugins.ValidateCreations(types, creations); err != nil {
		return err
	}
	if !slices.Contains(init.Capabilities, CapContainerCreate) {
		return fmt.Errorf(
			"creation: type %q is creatable but the plugin does not advertise %q",
			creations[0].Tag, CapContainerCreate,
		)
	}

	presentation := presentationOfInit(init)
	for _, c := range creations {
		t, _ := presentation.forType(c.Tag)
		if t.TrailerField == "" {
			return fmt.Errorf(
				"creation: type %q is creatable but declares no trailer_field"+
					" (a new object's description is sent under it)",
				c.Tag,
			)
		}
		for _, key := range c.Required {
			if _, err := presentation.createFieldKey(c.Tag, key); err != nil {
				return fmt.Errorf("creation: type %q required field: %w", c.Tag, err)
			}
		}
	}
	return nil
}

// presentationOfInit builds the linked-surface synthesis from an initialize
// result's declarations (node_types presentation members + facet_writes).
func presentationOfInit(init InitializeResult) Presentation {
	writes := make(
		[]cutting_garden_plugins.NodeTypeFacetWrites, len(init.FacetWrites),
	)
	for i, view := range init.FacetWrites {
		writes[i] = view.ToNodeTypeFacetWrites()
	}
	return NewPresentation(PresentationsOf(init), writes)
}

// createFieldKey resolves one document field of a new node of type tag to the
// node.create_child body key it is sent under, and whether it is multi-valued:
// the trailer_field is itself; a dimension maps through its `one` / `many`
// facet write's field. Anything else cannot be set on create.
func (p Presentation) createFieldKey(tag, field string) (createKey, error) {
	t, _ := p.forType(tag)
	if t.TrailerField != "" && field == t.TrailerField {
		return createKey{key: field}, nil
	}
	write, declared := p.writes[tag][field]
	switch {
	case !declared:
		return createKey{}, fmt.Errorf(
			"field %q is neither the trailer_field nor a dimension with a facet write", field,
		)
	case write.Mode == cutting_garden_plugins.FacetWriteOne:
		return createKey{key: write.Field}, nil
	case write.Mode == cutting_garden_plugins.FacetWriteMany:
		return createKey{key: write.Field, many: true}, nil
	}
	return createKey{}, fmt.Errorf("field %q is read-only (facet write mode %q)", field, write.Mode)
}

type createKey struct {
	key  string
	many bool
}

// BuildCreateBody builds the node.create_child body for a new node of type
// tag from its document fields (the CreateApplier contract): each field is
// sent under its body key with the §Facet writes value shapes — a `one`
// dimension and the trailer as a JSON string, a `many` dimension as the
// COMPLETE array of its values:
//
//	{"title": "Wrapped boxes lose their description", "milestone": "v0.3", "labels": ["bug"]}
//
// A field that cannot be set on create, or a single-valued field given more
// than one value, is a bad request.
func (p Presentation) BuildCreateBody(tag string, fields map[string][]string) ([]byte, error) {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)

	body := map[string]any{}
	for _, name := range names {
		key, err := p.createFieldKey(tag, name)
		if err != nil {
			return nil, errors.BadRequestf("create: type %q: %s", tag, err)
		}
		values := fields[name]
		if key.many {
			body[key.key] = append([]string{}, values...)
			continue
		}
		if len(values) != 1 {
			return nil, errors.BadRequestf(
				"create: type %q: field %q is single-valued, got %d value(s)",
				tag, name, len(values),
			)
		}
		body[key.key] = values[0]
	}
	if len(body) == 0 {
		return nil, errors.BadRequestf("create: type %q: no fields", tag)
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return encoded, nil
}
