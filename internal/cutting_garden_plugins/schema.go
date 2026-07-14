package cutting_garden_plugins

// NodeTypeBody describes the create/update payload one writable node type
// accepts — the schema-discovery surface behind the mcp server's
// describe_node_types tool, so an agent can construct a valid body without
// guessing or reading an existing node first.
type NodeTypeBody struct {
	// Tag is the NodeType.Tag this describes (e.g. "caldav-object-v1").
	Tag string
	// Accepts names the body formats the create_node/put_node tools take
	// for this type, human-readable and ordered most-preferred first (e.g.
	// "application/json (the {component,event|task} object)",
	// "text/calendar (raw iCalendar)").
	Accepts []string
	// Example is a concrete sample body in the structured (JSON) form, for an
	// agent to copy and adapt. It is JSON-marshalable; nil when the plugin
	// offers no structured form. A formal JSON Schema is a future addition
	// (an Example is the pragmatic first cut).
	Example any
}

// BodyDescriber is the OPTIONAL capability a write-capable plugin implements
// to describe the create/update payloads of its writable node types, for the
// mcp server's `describe_node_types` schema tool. It is probed by type
// assertion exactly as NodeMutator / RootLister are; a plugin that omits it
// contributes types (from RootLister.Types) without payload detail.
//
// A type is reported "writable" by the schema tool iff DescribeBodies
// returns an entry for its tag — so a plugin lists exactly the types its
// NodeMutator can currently create (e.g. caldav describes its object leaf
// but not its calendar container, which awaits MKCALENDAR).
type BodyDescriber interface {
	Plugin

	// DescribeBodies returns one NodeTypeBody per writable node type.
	DescribeBodies() []NodeTypeBody
}
