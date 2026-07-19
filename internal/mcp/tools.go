package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"path"
	"strings"

	"code.linenisgreat.com/cutting-garden/internal/command_components"
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/mcp_tool_perms"
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

// resourceReader is the read surface the read_node / list_nodes tools wrap —
// the ReadResource the resources/read MCP method serves (*Resources
// satisfies it). Exposing the read tree as tools makes it reachable from
// clients that render only tools, not MCP resources (the claude.ai web UI;
// circus#29).
type resourceReader interface {
	ReadResource(ctx context.Context, uri string) (*protocol.ResourceReadResult, error)
}

// facetReader is the read_facets tool's read surface — Resources.ReadFacets,
// the facet-view counterpart to resourceReader's ReadResource
// (cutting-garden#151, RFC 0012 §7/§9). *Resources satisfies it.
type facetReader interface {
	ReadFacets(
		ctx context.Context, uri string, filter cutting_garden_plugins.FacetFilter,
	) (*facetView, error)
}

// readSurface is the combined read tree the discovery tools wrap; *Resources
// satisfies both halves, so newTools takes one provider for both fields.
type readSurface interface {
	resourceReader
	facetReader
}

// Tools implements go-mcp's ToolProviderV1. It always advertises the
// read-only schema/read/list discovery tools (describe_node_types, read_node,
// list_nodes), and adds the create_node / put_node / patch_node / delete_node
// write tools (FDR 0020) when a configured root's plugin implements
// NodeMutator. A mutation targets the same node URI read_node/list_nodes
// surface, so the read and write axes share one address space.
type Tools struct {
	roots   []*url.URL
	resolve mutatorResolveFunc
	// resolveCreator resolves the ContainerCreator capability
	// (cutting-garden#143): create_node dispatches to CreateChild for
	// types the plugin declares ServerAssignedIdentity.
	resolveCreator creatorResolveFunc
	reader         resourceReader
	// facets is the read_facets tool's read surface (cutting-garden#151).
	facets facetReader
}

// creatorResolveFunc mirrors mutatorResolveFunc for the
// server-assigned-identity creation capability.
type creatorResolveFunc func(
	uriStr string,
) (*url.URL, cutting_garden_plugins.ContainerCreator, error)

var _ server.ToolProviderV1 = (*Tools)(nil)

// newTools builds a tool provider over the given roots, wired to the real
// plugin registry and the resource read surface the read/list/facets tools
// wrap.
func newTools(roots []*url.URL, reader readSurface) *Tools {
	return &Tools{
		roots:          roots,
		resolve:        command_components.ResolveNodeMutatorPlugin,
		resolveCreator: command_components.ResolveContainerCreatorPlugin,
		reader:         reader,
		facets:         reader,
	}
}

// hasMutator reports whether any configured root's plugin can mutate —
// NodeMutator or ContainerCreator — so the server advertises the write
// tools only where they apply.
func (t *Tools) hasMutator() bool {
	for _, r := range t.roots {
		if _, _, err := t.resolve(r.String()); err == nil {
			return true
		}
		if t.resolveCreator != nil {
			if _, _, err := t.resolveCreator(r.String()); err == nil {
				return true
			}
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
	putNodeSchema = `{"type":"object","required":["uri","body"],` +
		`"properties":{` +
		`"uri":{"type":"string","description":"the existing node URI to overwrite (full replace)"},` +
		`"body":{"type":"string","description":"the new object as raw iCalendar or {component,event|task} JSON"}}}`
	patchNodeSchema = `{"type":"object","required":["uri","body"],` +
		`"properties":{` +
		`"uri":{"type":"string","description":"the existing node URI to patch"},` +
		`"body":{"type":"string","description":"a JSON object with only the fields to change; absent fields are left untouched"}}}`
	deleteNodeSchema = `{"type":"object","required":["uri"],` +
		`"properties":{"uri":{"type":"string","description":"the node URI to delete"}}}`
	describeNodeTypesSchema = `{"type":"object","properties":{}}`
	readNodeSchema          = `{"type":"object","required":["uri"],` +
		`"properties":{"uri":{"type":"string","description":"the node URI to read (a leaf returns its parsed fields + a raw-bytes link; a container returns its child listing)"}}}`
	listNodesSchema = `{"type":"object","properties":{` +
		`"uri":{"type":"string","description":"the container/prefix to list children of; omit to list the configured roots (the entry points)"},` +
		`"limit":{"type":"integer","minimum":0,"description":"max number of child nodes to return (optional). A response SHORTER than limit means you have reached the end of the listing; a full-length response may or may not be the end — pass a larger offset to check."},` +
		`"offset":{"type":"integer","minimum":0,"description":"number of child nodes to skip before applying limit (optional, default 0). An offset past the end yields an empty array, not an error."}}}`
	readFacetsSchema = `{"type":"object","required":["uri"],` +
		`"properties":{` +
		`"uri":{"type":"string","description":"the container node URI to summarize"},` +
		`"filter":{"type":"string","description":"optional comma-separated dimension=value predicates (AND-composed, same grammar as list --filter) narrowing the summary, e.g. \"read=false\" or \"status=CONFIRMED,year=2026\""}}}`
)

// toolDefs is the V1 tool catalogue this server advertises: the read-only
// discovery tools (describe_node_types / read_node / list_nodes) always, plus
// the CUD write tools when a configured root can mutate. Names, schemas, and
// permission annotations come from mcp_tool_perms (the single classifier
// shared with the clown hook).
func (t *Tools) toolDefs() []protocol.ToolV1 {
	defs := readToolDefs()
	if t.hasMutator() {
		defs = append(defs, cudToolDefs()...)
	}
	return defs
}

// readToolDefs is the read-only catalogue: schema discovery plus read_node /
// list_nodes, which mirror resources/read + resources/list as tools so the
// tree is reachable from a tools-only client (circus#29).
func readToolDefs() []protocol.ToolV1 {
	return []protocol.ToolV1{
		{
			Name: mcp_tool_perms.ToolDescribeNodeTypes,
			Description: "List the node types each scheme exposes (tag, container vs " +
				"leaf, mimetype) and, for writable types, the body payload create_node/" +
				"put_node accept, with a concrete example. Read-only; call it first " +
				"to learn a type tag and body shape before create_node.",
			InputSchema: json.RawMessage(describeNodeTypesSchema),
			Annotations: annotationFor(mcp_tool_perms.ToolDescribeNodeTypes),
		},
		{
			Name: mcp_tool_perms.ToolListNodes,
			Description: "Browse the tree: list the child nodes under a container URI " +
				"(or, with no uri, the configured roots — the entry points). " +
				"Each node carries its uri, so you descend by listing deeper or read a " +
				"leaf with read_node. Optional limit/offset page a large listing " +
				"host-side after enumeration; a response shorter than limit signals " +
				"the end (there is no separate total — read_facets gives you counts " +
				"without paging through the whole listing).",
			InputSchema: json.RawMessage(listNodesSchema),
			Annotations: annotationFor(mcp_tool_perms.ToolListNodes),
		},
		{
			Name: mcp_tool_perms.ToolReadNode,
			Description: "Read one node by URI: a leaf returns its parsed fields (e.g. a " +
				"calendar event's {component,event|task} JSON) plus a raw-bytes link; a " +
				"container returns its child listing. The read sibling of create/put/" +
				"patch/delete_node.",
			InputSchema: json.RawMessage(readNodeSchema),
			Annotations: annotationFor(mcp_tool_perms.ToolReadNode),
		},
		{
			Name: mcp_tool_perms.ToolReadFacets,
			Description: "Summarize a container's children by its declared facet " +
				"dimensions (counts per value — status, category, read/unread, …) " +
				"WITHOUT enumerating them. Call this on a container BEFORE listing " +
				"its children: it orients you on size and shape (how many, of what " +
				"kind) cheaply, and a filter lets you narrow the counts to a slice " +
				"you care about before deciding whether/how to browse further. With " +
				"no filter, serves the memoized summary (see describe_node_types for " +
				"a scheme's declared dimensions); filter is the same comma-separated " +
				"dimension=value grammar as `list --filter` (e.g. \"read=false\"), " +
				"AND-composed, and computes a fresh narrowed summary directly. " +
				"Errors when the URI's scheme declares no facets.",
			InputSchema: json.RawMessage(readFacetsSchema),
			Annotations: annotationFor(mcp_tool_perms.ToolReadFacets),
		},
	}
}

// cudToolDefs is the create/put/patch/delete write-tool catalogue.
func cudToolDefs() []protocol.ToolV1 {
	return []protocol.ToolV1{
		{
			Name: mcp_tool_perms.ToolCreateNode,
			Description: "Create a new node (e.g. a calendar event or task) at a node URI. " +
				"Strict: errors if the node already exists (use put_node to overwrite). " +
				"For types describe_node_types marks serverAssignedIdentity, pass the " +
				"CONTAINER uri instead — the source names the node and the result " +
				"reports the created URI.",
			InputSchema: json.RawMessage(createNodeSchema),
			Annotations: annotationFor(mcp_tool_perms.ToolCreateNode),
		},
		{
			Name: mcp_tool_perms.ToolPutNode,
			Description: "Overwrite an existing node's body at a node URI (full replace). " +
				"Strict: errors if the node does not exist (use create_node).",
			InputSchema: json.RawMessage(putNodeSchema),
			Annotations: annotationFor(mcp_tool_perms.ToolPutNode),
		},
		{
			Name: mcp_tool_perms.ToolPatchNode,
			Description: "Partially update an existing node: body is a JSON object " +
				"containing only the fields to change; absent fields are left untouched. " +
				"Use instead of put_node when you only want to change one field " +
				"without reading and re-sending the entire object.",
			InputSchema: json.RawMessage(patchNodeSchema),
			Annotations: annotationFor(mcp_tool_perms.ToolPatchNode),
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

// createChild routes a create to ContainerCreator.CreateChild when the
// scheme's plugin both implements the capability and DECLARES the requested
// type ServerAssignedIdentity (via DescribeBodies — the explicit contract of
// cutting-garden#143). handled is false when either condition misses, and
// the caller falls through to the CreateNode path; err is meaningful only
// when handled.
func (t *Tools) createChild(
	ctx context.Context, uriStr, body, typ string,
) (created string, handled bool, err error) {
	if t.resolveCreator == nil || typ == "" {
		return "", false, nil
	}
	container, creator, err := t.resolveCreator(uriStr)
	if err != nil {
		return "", false, nil
	}

	describer, ok := creator.(cutting_garden_plugins.BodyDescriber)
	if !ok {
		return "", false, nil
	}
	declared := false
	for _, b := range describer.DescribeBodies() {
		if b.Tag == typ && b.ServerAssignedIdentity {
			declared = true
			break
		}
	}
	if !declared {
		return "", false, nil
	}

	createdURL, err := creator.CreateChild(
		ctx, container, strings.NewReader(body), typ,
	)
	if err != nil {
		return "", true, err
	}
	if createdURL == nil {
		return "", true, errors.ErrorWithStackf(
			"scheme %q: CreateChild returned no created URI", container.Scheme,
		)
	}
	return createdURL.String(), true, nil
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

		// Server-assigned-identity types (cutting-garden#143): the uri is
		// the CONTAINER, the source names the created node, and the result
		// reports the URI it chose. Dispatch is by the plugin's own
		// declaration, so caller-named and server-assigned types coexist
		// on one scheme.
		if created, handled, err := t.createChild(ctx, in.URI, in.Body, in.Type); handled {
			if err != nil {
				return "", err
			}
			return "created " + created, nil
		}

		u, m, err := t.resolve(in.URI)
		if err != nil {
			return "", err
		}
		if err := m.CreateNode(ctx, u, strings.NewReader(in.Body), in.Type); err != nil {
			return "", err
		}
		return "created " + in.URI, nil

	case mcp_tool_perms.ToolPutNode:
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
		if err := m.PutNode(ctx, u, strings.NewReader(in.Body)); err != nil {
			return "", err
		}
		return "put " + in.URI, nil

	case mcp_tool_perms.ToolPatchNode:
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
		if err := m.PatchNode(ctx, u, strings.NewReader(in.Body)); err != nil {
			return "", err
		}
		return "patched " + in.URI, nil

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

	case mcp_tool_perms.ToolReadNode:
		var in struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", errors.Wrap(err)
		}
		res, err := t.reader.ReadResource(ctx, in.URI)
		if err != nil {
			return "", err
		}
		return renderContents(res.Contents), nil

	case mcp_tool_perms.ToolListNodes:
		var in struct {
			URI    string `json:"uri"`
			Limit  int    `json:"limit"`
			Offset int    `json:"offset"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", errors.Wrap(err)
		}
		if in.Limit < 0 {
			return "", errors.ErrorWithStackf("list_nodes: limit must be >= 0")
		}
		if in.Offset < 0 {
			return "", errors.ErrorWithStackf("list_nodes: offset must be >= 0")
		}
		// No uri: the configured roots themselves are the browse entry points
		// (a root is a container you descend into), mirroring the `list`
		// command's no-arg listing. This deliberately differs from
		// resources/list, which returns the roots' children: when a root is
		// itself a calendar (a per-calendar account), listing its children
		// flattens every event into the entry-point listing (circus#29). A
		// uri lists that container's children (= reading the container, which
		// yields its child listing).
		if in.URI == "" {
			views := make([]nodeView, 0, len(t.roots))
			for _, root := range t.roots {
				views = append(views, nodeView{
					URI:       root.String(),
					Name:      rootLabel(root),
					Container: true,
				})
			}
			views = paginate(views, in.Offset, in.Limit)
			body, err := json.MarshalIndent(views, "", "  ")
			if err != nil {
				return "", errors.Wrap(err)
			}
			return string(body), nil
		}
		res, err := t.reader.ReadResource(ctx, in.URI)
		if err != nil {
			return "", err
		}
		text := renderContents(res.Contents)
		// A byte-for-byte-unchanged default is REQUIRED (cutting-garden#86):
		// skip pagination entirely when neither param was given, rather than
		// slicing with limit=0 (which would empty a container's listing).
		if in.Limit > 0 || in.Offset > 0 {
			text, err = paginateListingText(text, in.Offset, in.Limit)
			if err != nil {
				return "", errors.Wrap(err)
			}
		}
		return text, nil

	case mcp_tool_perms.ToolReadFacets:
		var in struct {
			URI    string `json:"uri"`
			Filter string `json:"filter"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", errors.Wrap(err)
		}
		filter, err := cutting_garden_plugins.ParseFacetFilter(in.Filter)
		if err != nil {
			return "", err
		}
		view, err := t.facets.ReadFacets(ctx, in.URI, filter)
		if err != nil {
			return "", err
		}
		body, err := json.MarshalIndent(view, "", "  ")
		if err != nil {
			return "", errors.Wrap(err)
		}
		return string(body), nil

	default:
		return "", errors.ErrorWithStackf("unknown tool %q", name)
	}
}

// paginate slices items to [offset:offset+limit] (cutting-garden#86 phase A):
// limit<=0 means unbounded (only offset applies), offset past the end yields
// an empty (never nil) slice so it marshals to `[]`, not `null`.
func paginate[T any](items []T, offset, limit int) []T {
	if offset >= len(items) {
		return []T{}
	}
	items = items[offset:]
	if limit > 0 && limit < len(items) {
		items = items[:limit]
	}
	return items
}

// paginateListingText applies paginate to a JSON array's text form — the
// list_nodes(uri) path, where the child listing already arrived as rendered
// text from t.reader.ReadResource (renderContents). Text that does not
// decode as a JSON array (a leaf object read, or an empty listing already
// smaller than any reasonable page) is returned unchanged: pagination is a
// listing concern, not a leaf-read one.
func paginateListingText(text string, offset, limit int) (string, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return text, nil
	}
	body, err := json.MarshalIndent(paginate(raw, offset, limit), "", "  ")
	if err != nil {
		return "", errors.Wrap(err)
	}
	return string(body), nil
}

// renderContents flattens a resources/read result into tool text: each
// content's text in order, and a one-line note for a link-only content (a
// raw-bytes madder blob, which carries a URI + mimetype but no inline text).
//
// A container's facet-summary block (mimeFacets) is skipped: the tool text is
// consumed as a single JSON value (the child listing array or a leaf object),
// and appending a second JSON object would break that. Facets ride on the
// resources/read method's Contents[] for MCP resource clients; a structured
// facet surface for tools-only clients is a separate follow-up.
func renderContents(contents []protocol.ResourceContent) string {
	var b strings.Builder
	for _, c := range contents {
		if c.MimeType == mimeFacets {
			continue
		}
		switch {
		case c.Text != "":
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(c.Text)
		case c.URI != "":
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString("raw bytes: " + c.URI)
			if c.MimeType != "" {
				b.WriteString(" (" + c.MimeType + ")")
			}
		}
	}
	return b.String()
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
	Tag          string           `json:"tag"`
	Container    bool             `json:"container"`
	LeafMimeType string           `json:"leafMimeType,omitempty"`
	Writable     bool             `json:"writable"`
	Body         *bodySchema      `json:"body,omitempty"`
	Facets       []facetDimSchema `json:"facets,omitempty"`
}

// bodySchema is the create/update payload description for a writable type: the
// accepted formats and a concrete example. A formal JSON Schema per type is a
// future addition (cutting-garden FDR 0020). ServerAssignedIdentity marks a
// container-create type (cutting-garden#143): create_node takes the CONTAINER
// URI and the result reports the URI the source assigned.
type bodySchema struct {
	Accepts                []string `json:"accepts"`
	Example                any      `json:"example,omitempty"`
	ServerAssignedIdentity bool     `json:"serverAssignedIdentity,omitempty"`
}

// facetDimSchema describes one declared facet dimension of a node type, for
// the describe_node_types tool: its key, display label, value-shape kind,
// whether a node may carry several values, whether its value domain is
// closed (known up front), and — when nonzero — the volatile revalidation
// window. See RFC 0012 §2, §11.3.
type facetDimSchema struct {
	Key    string `json:"key"`
	Label  string `json:"label,omitempty"`
	Kind   string `json:"kind"`
	Multi  bool   `json:"multi,omitempty"`
	Closed bool   `json:"closed,omitempty"`
	// RevalidateAfterSeconds, when nonzero, marks the dimension VOLATILE
	// (RFC 0012 §11.3): its bucketing is a function of (data, now), so a
	// memoized summary containing it expires after this many seconds even
	// with an unmoved change token. Zero (the default, omitted) means pure.
	RevalidateAfterSeconds int64 `json:"revalidateAfterSeconds,omitempty"`
}

// facetDimSchemas projects a plugin's declared FacetDimensions into their
// describe_node_types view. A non-nil Values list marks a closed domain.
func facetDimSchemas(
	dims []cutting_garden_plugins.FacetDimension,
) []facetDimSchema {
	out := make([]facetDimSchema, 0, len(dims))
	for _, d := range dims {
		out = append(out, facetDimSchema{
			Key:                    d.Key,
			Label:                  d.Label,
			Kind:                   string(d.Kind),
			Multi:                  d.Multi,
			Closed:                 d.Values != nil,
			RevalidateAfterSeconds: int64(d.RevalidateAfter.Seconds()),
		})
	}
	return out
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
		facets := map[string][]cutting_garden_plugins.FacetDimension{}
		if fd, ok := p.(cutting_garden_plugins.FacetDescriber); ok {
			for _, ntf := range fd.DescribeFacets() {
				facets[ntf.Tag] = ntf.Dimensions
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
				ts.Body = &bodySchema{
					Accepts:                b.Accepts,
					Example:                b.Example,
					ServerAssignedIdentity: b.ServerAssignedIdentity,
				}
			}
			if dims, ok := facets[nt.Tag]; ok {
				ts.Facets = facetDimSchemas(dims)
			}
			types = append(types, ts)
		}
		out = append(out, schemeSchema{Scheme: firstScheme(p), Types: types})
	}
	return out
}

// rootLabel derives a short display name for a root URI: the last path
// segment, else the host, else the full URI. Mirrors list.rootLabel so the
// `list` command and the list_nodes tool label the entry points the same
// way. A friendlier per-calendar label (the server displayname or the
// configured account name) is a follow-up (#120).
func rootLabel(u *url.URL) string {
	if trimmed := strings.TrimRight(u.Path, "/"); trimmed != "" {
		return path.Base(trimmed)
	}
	if u.Host != "" {
		return u.Host
	}
	return u.String()
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
