package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/amarbel-llc/cutting-garden/internal/command_components"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/cutting-garden/internal/mcp_tool_perms"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/protocol"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/server"
)

// mutatorResolveFunc resolves a URI string to its parsed URL and the
// NodeMutator registered for its scheme. It has the shape of
// command_components.ResolveNodeMutatorPlugin and is a field on Tools so
// tests can substitute a registry-free fake.
type mutatorResolveFunc func(
	uriStr string,
) (*url.URL, cutting_garden_plugins.NodeMutator, error)

// Tools implements go-mcp's ToolProviderV1. It always advertises the
// read-only describe_node_types schema-discovery tool, and adds the
// create_node / update_node / delete_node write tools (FDR 0020) when a
// configured root's plugin implements NodeMutator. A mutation targets the
// same node URI resources/read surfaces, so the read and write axes share
// one address space.
type Tools struct {
	roots   []*url.URL
	resolve mutatorResolveFunc
}

var _ server.ToolProviderV1 = (*Tools)(nil)

// newTools builds a tool provider over the given roots, wired to the real
// plugin registry.
func newTools(roots []*url.URL) *Tools {
	return &Tools{
		roots:   roots,
		resolve: command_components.ResolveNodeMutatorPlugin,
	}
}

// hasMutator reports whether any configured root's plugin can mutate, so the
// server advertises the write tools only where they apply.
func (t *Tools) hasMutator() bool {
	for _, r := range t.roots {
		if _, _, err := t.resolve(r.String()); err == nil {
			return true
		}
	}
	return false
}

const (
	createNodeSchema = `{"type":"object","required":["uri","body"],` +
		`"properties":{` +
		`"uri":{"type":"string","description":"the node URI to create (e.g. caldav://host/cal/x.ics)"},` +
		`"body":{"type":"string","description":"the object as raw iCalendar (.ics) or the {component,event|task} JSON resources/read returns"},` +
		`"type":{"type":"string","description":"the node type tag, e.g. caldav-object-v1"}}}`
	updateNodeSchema = `{"type":"object","required":["uri","body"],` +
		`"properties":{` +
		`"uri":{"type":"string","description":"the existing node URI to overwrite"},` +
		`"body":{"type":"string","description":"the new object as raw iCalendar or {component,event|task} JSON"}}}`
	deleteNodeSchema = `{"type":"object","required":["uri"],` +
		`"properties":{"uri":{"type":"string","description":"the node URI to delete"}}}`
	describeNodeTypesSchema = `{"type":"object","properties":{}}`
)

// toolDefs is the V1 tool catalogue this server advertises: the read-only
// schema-discovery tool always, plus the CUD write tools when a configured
// root can mutate. Names, schemas, and permission annotations come from
// mcp_tool_perms (the single classifier shared with the clown hook).
func (t *Tools) toolDefs() []protocol.ToolV1 {
	defs := []protocol.ToolV1{describeToolDef()}
	if t.hasMutator() {
		defs = append(defs, cudToolDefs()...)
	}
	return defs
}

// describeToolDef is the read-only schema-discovery tool: it tells an agent
// which node types each scheme exposes and what body the write tools accept,
// so create_node can be called without guessing the type tag or payload.
func describeToolDef() protocol.ToolV1 {
	return protocol.ToolV1{
		Name: mcp_tool_perms.ToolDescribeNodeTypes,
		Description: "List the node types each scheme exposes (tag, container vs " +
			"leaf, mimetype) and, for writable types, the body payload create_node/" +
			"update_node accept, with a concrete example. Read-only; call it first " +
			"to learn a type tag and body shape before create_node.",
		InputSchema: json.RawMessage(describeNodeTypesSchema),
		Annotations: annotationFor(mcp_tool_perms.ToolDescribeNodeTypes),
	}
}

// cudToolDefs is the create/update/delete write-tool catalogue.
func cudToolDefs() []protocol.ToolV1 {
	return []protocol.ToolV1{
		{
			Name: mcp_tool_perms.ToolCreateNode,
			Description: "Create a new node (e.g. a calendar event or task) at a node URI. " +
				"Strict: errors if the node already exists (use update_node to overwrite).",
			InputSchema: json.RawMessage(createNodeSchema),
			Annotations: annotationFor(mcp_tool_perms.ToolCreateNode),
		},
		{
			Name: mcp_tool_perms.ToolUpdateNode,
			Description: "Overwrite an existing node's body at a node URI. " +
				"Strict: errors if the node does not exist (use create_node).",
			InputSchema: json.RawMessage(updateNodeSchema),
			Annotations: annotationFor(mcp_tool_perms.ToolUpdateNode),
		},
		{
			Name:        mcp_tool_perms.ToolDeleteNode,
			Description: "Delete the node at a node URI.",
			InputSchema: json.RawMessage(deleteNodeSchema),
			Annotations: annotationFor(mcp_tool_perms.ToolDeleteNode),
		},
	}
}

// annotationFor maps a tool's permission class to MCP behavior hints, so a
// client surfaces the right confirmation: a destructive tool is not
// read-only and may be destructive.
func annotationFor(tool string) *protocol.ToolAnnotations {
	class, _ := mcp_tool_perms.Classify(tool)
	destructive := class == mcp_tool_perms.ClassDestructive
	return &protocol.ToolAnnotations{
		ReadOnlyHint:    protocol.BoolPtr(!destructive),
		DestructiveHint: protocol.BoolPtr(destructive),
	}
}

// ListTools (V0) advertises the catalogue without annotations.
func (t *Tools) ListTools(context.Context) ([]protocol.Tool, error) {
	defs := t.toolDefs()
	out := make([]protocol.Tool, len(defs))
	for i, d := range defs {
		out[i] = protocol.Tool{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: d.InputSchema,
		}
	}
	return out, nil
}

// ListToolsV1 advertises the catalogue with permission annotations.
func (t *Tools) ListToolsV1(
	context.Context, string,
) (*protocol.ToolsListResultV1, error) {
	return &protocol.ToolsListResultV1{Tools: t.toolDefs()}, nil
}

// CallTool dispatches a V0 tool call.
func (t *Tools) CallTool(
	ctx context.Context, name string, args json.RawMessage,
) (*protocol.ToolCallResult, error) {
	msg, err := t.call(ctx, name, args)
	if err != nil {
		return protocol.ErrorResult(err.Error()), nil
	}
	return &protocol.ToolCallResult{
		Content: []protocol.ContentBlock{protocol.TextContent(msg)},
	}, nil
}

// CallToolV1 dispatches a V1 tool call.
func (t *Tools) CallToolV1(
	ctx context.Context, name string, args json.RawMessage,
) (*protocol.ToolCallResultV1, error) {
	msg, err := t.call(ctx, name, args)
	if err != nil {
		return protocol.ErrorResultV1(err.Error()), nil
	}
	return &protocol.ToolCallResultV1{
		Content: []protocol.ContentBlockV1{protocol.TextContentV1(msg)},
	}, nil
}

// call performs one mutation and returns a human-readable success line. A
// returned error is surfaced as an MCP tool error result (IsError), not a
// transport failure: a mutation rejection (already-exists, not-found, a
// server 4xx/5xx) is information for the agent, not a protocol fault.
func (t *Tools) call(
	ctx context.Context, name string, args json.RawMessage,
) (string, error) {
	switch name {
	case mcp_tool_perms.ToolCreateNode:
		var in struct {
			URI  string `json:"uri"`
			Body string `json:"body"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", errors.Wrap(err)
		}
		u, m, err := t.resolve(in.URI)
		if err != nil {
			return "", err
		}
		if err := m.CreateNode(ctx, u, strings.NewReader(in.Body), in.Type); err != nil {
			return "", err
		}
		return "created " + in.URI, nil

	case mcp_tool_perms.ToolUpdateNode:
		var in struct {
			URI  string `json:"uri"`
			Body string `json:"body"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", errors.Wrap(err)
		}
		u, m, err := t.resolve(in.URI)
		if err != nil {
			return "", err
		}
		if err := m.UpdateNode(ctx, u, strings.NewReader(in.Body)); err != nil {
			return "", err
		}
		return "updated " + in.URI, nil

	case mcp_tool_perms.ToolDeleteNode:
		var in struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", errors.Wrap(err)
		}
		u, m, err := t.resolve(in.URI)
		if err != nil {
			return "", err
		}
		if err := m.DeleteNode(ctx, u); err != nil {
			return "", err
		}
		return "deleted " + in.URI, nil

	case mcp_tool_perms.ToolDescribeNodeTypes:
		body, err := json.MarshalIndent(
			collectSchema(cutting_garden_plugins.RegisteredPlugins()), "", "  ",
		)
		if err != nil {
			return "", errors.Wrap(err)
		}
		return string(body), nil

	default:
		return "", errors.ErrorWithStackf("unknown tool %q", name)
	}
}

// schemeSchema is one scheme's node-type catalogue in the describe_node_types
// output.
type schemeSchema struct {
	Scheme string       `json:"scheme"`
	Types  []typeSchema `json:"types"`
}

// typeSchema describes one node type: its tag, whether it is a container or a
// leaf, a leaf's body mimetype, and — for writable types — the accepted
// create/update payload.
type typeSchema struct {
	Tag          string      `json:"tag"`
	Container    bool        `json:"container"`
	LeafMimeType string      `json:"leafMimeType,omitempty"`
	Writable     bool        `json:"writable"`
	Body         *bodySchema `json:"body,omitempty"`
}

// bodySchema is the create/update payload description for a writable type: the
// accepted formats and a concrete example. A formal JSON Schema per type is a
// future addition (cutting-garden FDR 0020).
type bodySchema struct {
	Accepts []string `json:"accepts"`
	Example any      `json:"example,omitempty"`
}

// collectSchema builds the describe_node_types catalogue from the registered
// plugins: every RootLister contributes its node types, and a BodyDescriber
// adds the writable types' payload detail. Plugins without traversal (the
// file plugin) contribute nothing.
func collectSchema(plugins []cutting_garden_plugins.Plugin) []schemeSchema {
	out := make([]schemeSchema, 0, len(plugins))
	for _, p := range plugins {
		rl, ok := p.(cutting_garden_plugins.RootLister)
		if !ok {
			continue
		}
		bodies := map[string]cutting_garden_plugins.NodeTypeBody{}
		if bd, ok := p.(cutting_garden_plugins.BodyDescriber); ok {
			for _, b := range bd.DescribeBodies() {
				bodies[b.Tag] = b
			}
		}
		nts := rl.Types()
		types := make([]typeSchema, 0, len(nts))
		for _, nt := range nts {
			ts := typeSchema{
				Tag:          nt.Tag,
				Container:    nt.Container,
				LeafMimeType: nt.BodyMimeType(),
			}
			if b, ok := bodies[nt.Tag]; ok {
				ts.Writable = true
				ts.Body = &bodySchema{Accepts: b.Accepts, Example: b.Example}
			}
			types = append(types, ts)
		}
		out = append(out, schemeSchema{Scheme: firstScheme(p), Types: types})
	}
	return out
}

// firstScheme is the plugin's first non-empty scheme — the name a user types
// — or "(schemeless)" for the default plugin.
func firstScheme(p cutting_garden_plugins.Plugin) string {
	for _, s := range p.Schemes() {
		if s != "" {
			return s
		}
	}
	return "(schemeless)"
}
