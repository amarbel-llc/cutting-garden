package cutting_garden_plugins

import (
	"context"
	"fmt"
	"io"
	"net/url"
)

// NodeTypeCreation declares that organize (RFC 0015, forge organize F10) may
// CREATE nodes of one type: a `+` temp-id box in an organize document becomes
// a new node of Tag, created under a container node of ContainerType through
// the plugin's ContainerCreator — the plugin assigns and returns the new
// node's identity.
//
// Fields are named in the DOCUMENT's vocabulary — the presented field keys a
// box and the grouping speak (caldav `summary`, `status`, `date_start`,
// `time_start`, `priority`, `categories`; a forge's `title`, `state`,
// `milestone`, `label`): the grouped dimension's bucket, each inline atom, the
// tag dimension's merged set, and the trailer under the type's declared
// trailer field. It is the creation-side sibling of FacetWrite (RFC 0012
// §Write mapping) and, like it, metadata only: the plugin's CreateApplier
// owns the document-field → substrate mapping.
type NodeTypeCreation struct {
	// Tag is the NodeType.Tag organize may create.
	Tag string
	// ContainerType is the NodeType.Tag of the container a new node is
	// created under (the document's `_anchor` must be one). REQUIRED.
	ContainerType string
	// Required names the document fields a new node MUST be given — e.g. a
	// caldav VEVENT's `date_start`, a forge issue's `title`. Organize refuses a
	// creation missing one at plan time, before the diff and before anything
	// is written.
	Required []string
}

// CreationDescriber is the OPTIONAL capability declaring which node types
// organize may create (forge organize F10). Probed by type assertion, like
// FacetWriteDescriber. A plugin that implements it MUST also implement
// CreateApplier (to build the body) and ContainerCreator (to create).
type CreationDescriber interface {
	Plugin

	// DescribeCreation returns one NodeTypeCreation per creatable type.
	DescribeCreation() []NodeTypeCreation
}

// CreateApplier BUILDS the body of one new node from its merged document
// fields — the creation-side sibling of FacetWriteApplier / FieldWriteApplier:
// the organize apply engine hands the plugin the merged object and sends
// whatever bytes come back through ContainerCreator.CreateChild, so the
// framework never learns the substrate's body shape (RFC 0009 no-inversion).
// The plugin owns every completion a create needs (a caldav date+time split
// recombined into one DTSTART with a default TZID, a priority band completed
// to its canonical integer, a minted UID).
type CreateApplier interface {
	Plugin

	// BuildCreateBody returns the CreateChild body for a new node of type typ.
	// fields maps each supplied document field to its values: ONE value for a
	// single-valued field (an atom, the grouped bucket, the trailer), the
	// complete set for a multi-valued one (the tag dimension). A field the
	// plugin cannot write on create, or an unusable value, is a bad request —
	// organize calls this at PLAN time, so the refusal lands before the diff.
	//
	// key is the creation's IDEMPOTENCY KEY (printable, `[a-z0-9-]`, stable
	// for one document's temp id — organize derives it from the document's
	// `_base` and the temp id). A plugin whose substrate lets the client
	// choose identity SHOULD derive the new node's identity from it (caldav:
	// UID = key), so a repeated create is recognizable (IdempotentCreator).
	// Empty means no key: mint identity as usual.
	BuildCreateBody(
		ctx context.Context, typ string, fields map[string][]string, key string,
	) ([]byte, error)
}

// IdempotentCreator is the OPTIONAL keyed sibling of ContainerCreator: create
// a child under container from body, carrying the creation's idempotency key.
// A repeated call with the same key MUST NOT create a duplicate: the plugin
// recognizes that the keyed create already landed and returns the SAME node's
// URI with existed = true. Organize prefers it over CreateChild when a plugin
// has it; CreateChild itself (and so MCP create_node) stays strict.
type IdempotentCreator interface {
	Plugin

	CreateChildWithKey(
		ctx context.Context, container *url.URL, body io.Reader, typ, key string,
	) (created *url.URL, existed bool, err error)
}

// MissingRequiredFields returns the Required fields of creation absent (or
// empty) in fields, in declaration order — the plan-time check organize makes
// before building any body.
func MissingRequiredFields(
	creation NodeTypeCreation, fields map[string][]string,
) []string {
	var missing []string
	for _, key := range creation.Required {
		if !hasNonEmptyValue(fields[key]) {
			missing = append(missing, key)
		}
	}
	return missing
}

func hasNonEmptyValue(values []string) bool {
	for _, v := range values {
		if v != "" {
			return true
		}
	}
	return false
}

// ValidateCreations cross-checks creation declarations against the plugin's
// declared node types: every Tag and ContainerType MUST be a declared type,
// the container type MUST be a container, and a type is declared creatable at
// most once. It returns the first violation (nil when consistent).
func ValidateCreations(types []NodeType, creations []NodeTypeCreation) error {
	byTag := make(map[string]NodeType, len(types))
	for _, t := range types {
		byTag[t.Tag] = t
	}
	seen := map[string]bool{}
	for _, c := range creations {
		if _, ok := byTag[c.Tag]; !ok {
			return fmt.Errorf("creation: type %q is not a declared node type", c.Tag)
		}
		if seen[c.Tag] {
			return fmt.Errorf("creation: type %q is declared creatable more than once", c.Tag)
		}
		seen[c.Tag] = true
		container, ok := byTag[c.ContainerType]
		switch {
		case c.ContainerType == "":
			return fmt.Errorf("creation: type %q names no container type", c.Tag)
		case !ok:
			return fmt.Errorf(
				"creation: type %q container %q is not a declared node type",
				c.Tag, c.ContainerType,
			)
		case !container.Container:
			return fmt.Errorf(
				"creation: type %q container %q is not a container type",
				c.Tag, c.ContainerType,
			)
		}
		for _, key := range c.Required {
			if key == "" {
				return fmt.Errorf("creation: type %q names an empty required field", c.Tag)
			}
		}
	}
	return nil
}
