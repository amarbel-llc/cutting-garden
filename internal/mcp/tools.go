package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strings"

	"code.linenisgreat.com/cutting-garden/internal/command_components"
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/mcp_tool_perms"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/purse-first/libs/go-mcp/protocol"
	"code.linenisgreat.com/purse-first/libs/go-mcp/server"
)

// mutatorResolveFunc resolves a URI string to its parsed URL and the
// NodeMutator registered for its scheme. It has the shape of
// command_components.ResolveNodeMutatorPlugin and is a field on Tools so
// tests can substitute a registry-free fake.
type mutatorResolveFunc func(
	uriStr string,
) (*url.URL, cutting_garden_plugins.NodeMutator, error)

// resourceReader is the read surface the read_node / list_nodes tools wrap —
// ReadNode, the selector-aware read the resources/read MCP method serves at
// its default (*Resources satisfies it). Exposing the read tree as tools
// makes it reachable from clients that render only tools, not MCP resources
// (the claude.ai web UI; circus#29).
type resourceReader interface {
	ReadNode(
		ctx context.Context, uri string, content string,
	) (*protocol.ResourceReadResult, error)
}

// facetReader is the read_facets tool's read surface — Resources.ReadFacets,
// the facet-view counterpart to resourceReader's ReadNode
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
	// resolveLister resolves the RootLister capability directly
	// (cutting-garden#160): list_nodes needs it whenever a filter or the
	// bare opt-out is requested, since those paths bypass the reader's
	// ReadNode delegation (which has no filter/bare parameters of its
	// own) and drive ListRoots/EnrichedLister themselves.
	resolveLister resolveFunc
	// rootLabels overrides a root's display name in the no-uri list_nodes
	// listing (cutting-garden#120): keyed by the root URL's String() form,
	// from command_components.AggregateRootLabels. nil (the test-harness
	// default) means every root falls back to rootLabel's bare URL
	// derivation, exactly as before #120.
	rootLabels map[string]string
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
		resolveLister:  command_components.ResolveRootListerPlugin,
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
		`"properties":{"uri":{"type":"string","description":"the node URI to read (a leaf returns its parsed fields + a raw-bytes link; a container returns its child listing, plus its own body when it has one)"},` +
		`"content":{"type":"string","enum":["both","children","body"],"description":"what to return for a node that has both a body and children (optional, default both): both = the node's own body AND its child listing; children = the child listing only (cheap, no body fetch); body = the node's own body only, skipping the listing"}}}`
	listNodesSchema = `{"type":"object","properties":{` +
		`"uri":{"type":"string","description":"the container/prefix to list children of; omit to list the configured roots (the entry points)"},` +
		`"limit":{"type":"integer","minimum":0,"description":"max number of child nodes to return (optional). A response SHORTER than limit means you have reached the end of the listing; a full-length response may or may not be the end — pass a larger offset to check."},` +
		`"offset":{"type":"integer","minimum":0,"description":"number of child nodes to skip before applying limit (optional, default 0). An offset past the end yields an empty array, not an error."},` +
		`"filter":{"type":"string","description":"optional comma-separated dimension=value predicates (AND-composed), e.g. \"due_band=overdue\" or \"status=CONFIRMED,year=2026\" — the SAME grammar and dimension keys as read_facets. Call describe_node_types first to see a type's declared facets: each dimension's key and, when closed=true, its complete valid values array (values); an open dimension (closed=false) accepts any value, discovered at enumeration. An undeclared dimension or an out-of-domain closed-dimension value is a REJECTED, actionable error naming what was wrong — never a silent empty/unfiltered result. When present the result is wrapped as {nodes, filterApplied, filterMode} instead of a bare array — filterApplied is false (filterMode \"none\") on the rare scheme with no way to filter, so the caller always knows whether the returned nodes are actually narrowed."},` +
		`"bare":{"type":"boolean","description":"opt out of the default enrichment: true returns the cheap pre-enrichment shape {uri,name,type,container,mimeType} with no facets/fields, skipping any extra data-bearing fetch a plugin would otherwise perform (e.g. caldav skips its per-object body fetch, staying hrefs-only). Combining bare with filter still fetches whatever the filter requires; only the OUTPUT is stripped down. Default false: every entry carries its facets and any plugin-declared human-readable fields (e.g. summary/due/status) inline."}}}`
	readFacetsSchema = `{"type":"object","required":["uri"],` +
		`"properties":{` +
		`"uri":{"type":"string","description":"the container node URI to summarize"},` +
		`"filter":{"type":"string","description":"optional comma-separated dimension=value predicates (AND-composed), e.g. \"due_band=overdue\" or \"status=CONFIRMED,year=2026\" — same grammar as list_nodes/list --filter. Call describe_node_types first to see this scheme's declared facet dimensions: each dimension's key and, when closed=true, its complete valid values array (values), e.g. due_band's values are [\"overdue\",\"today\",\"this-week\",\"later\"]; an open dimension (closed=false, e.g. status) accepts any value, discovered at enumeration rather than declared up front. An undeclared dimension, or a value outside a closed dimension's declared set, is a REJECTED, actionable error naming what was wrong and the valid options — never a silent {facets:{}} indistinguishable from a genuine zero-match filter."}}}`
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
				"leaf, mimetype), the facet dimensions and listing fields a type may " +
				"carry in an enriched listing (see list_nodes) — including, for a " +
				"CLOSED dimension (closed:true), its complete valid values array so " +
				"a read_facets/list_nodes filter value never has to be guessed — " +
				"and, for writable types, the body payload create_node/put_node " +
				"accept, with a concrete example. Read-only; call it first to learn " +
				"a type tag, body shape, and filterable dimensions before create_node " +
				"or a filtered read_facets/list_nodes call.",
			InputSchema: json.RawMessage(describeNodeTypesSchema),
			Annotations: annotationFor(mcp_tool_perms.ToolDescribeNodeTypes),
		},
		{
			Name: mcp_tool_perms.ToolListNodes,
			Description: "Browse the tree: list the child nodes under a container URI " +
				"(or, with no uri, the configured roots — the entry points). " +
				"Each node carries its uri, so you descend by listing deeper or read a " +
				"leaf with read_node. Every entry is ENRICHED BY DEFAULT with its " +
				"facets and any plugin-declared human-readable fields (e.g. a caldav " +
				"object's summary/status/dtstart/dtend) inline — pass bare=true to opt out to " +
				"the cheap {uri,name,type,container,mimeType} shape. An optional " +
				"filter (the same dimension=value grammar as read_facets) narrows the " +
				"returned nodes to those matching, wrapping the result as " +
				"{nodes, filterApplied, filterMode} so you always know whether the " +
				"nodes were actually narrowed — this is the direct way to retrieve " +
				"the matching set read_facets can only count. Optional limit/offset " +
				"page a large listing host-side after enumeration; a response " +
				"shorter than limit signals the end.",
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
				"no filter, serves the memoized summary; filter is a comma-separated " +
				"dimension=value grammar, AND-composed (e.g. \"due_band=overdue\"), " +
				"and computes a fresh narrowed summary directly. When the resolved " +
				"container has its own child containers below it (e.g. a caldav " +
				"account with several calendars beneath it), the result MAY carry a " +
				"byContainer breakdown: which immediate child container each " +
				"matching item lives under, and how many — call this WITH a filter " +
				"(e.g. \"due_band=overdue\") to learn exactly which child to descend " +
				"into next instead of guessing across every one, capped at 50 " +
				"non-empty entries with byContainerTruncated set if more existed. " +
				"byContainer is absent when the plugin does not compute " +
				"per-container attribution for this node — this counts across the " +
				"subtree but only sometimes tells you where; list_nodes with the " +
				"SAME filter still retrieves the matching nodes at ONE level. Call " +
				"describe_node_types FIRST to see each type's declared dimensions " +
				"and, for closed ones, their complete valid values — an undeclared " +
				"dimension or an out-of-domain value is a rejected, actionable " +
				"error, not a silent empty result. Errors when the URI's scheme " +
				"declares no facets.",
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
				"without reading and re-sending the entire object. On success the " +
				"result names the fields that were actually applied — a field the " +
				"node type does not accept is ignored rather than applied, and a " +
				"patch that applies nothing is an error, not a silent no-op. Check " +
				"describe_node_types for the fields a type accepts.",
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
		applied, err := m.PatchNode(ctx, u, strings.NewReader(in.Body))
		if err != nil {
			return "", err
		}
		return patchOutcome(in.URI, applied)

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
			URI     string `json:"uri"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", errors.Wrap(err)
		}
		content := in.Content
		if content == "" {
			content = contentBoth
		}
		switch content {
		case contentBoth, contentChildren, contentBody:
		default:
			return "", errors.ErrorWithStackf(
				"read_node: content must be both, children, or body",
			)
		}
		res, err := t.reader.ReadNode(ctx, in.URI, content)
		if err != nil {
			return "", err
		}
		return renderReadNode(res.Contents), nil

	case mcp_tool_perms.ToolListNodes:
		var in struct {
			URI    string `json:"uri"`
			Limit  int    `json:"limit"`
			Offset int    `json:"offset"`
			Filter string `json:"filter"`
			Bare   bool   `json:"bare"`
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
		filter, err := cutting_garden_plugins.ParseFacetFilter(in.Filter)
		if err != nil {
			return "", err
		}
		// No uri: the configured roots themselves are the browse entry points
		// (a root is a container you descend into), mirroring the `list`
		// command's no-arg listing. This deliberately differs from
		// resources/list, which returns the roots' children: when a root is
		// itself a calendar (a per-calendar account), listing its children
		// flattens every event into the entry-point listing (circus#29). A
		// uri lists that container's children (= reading the container, which
		// yields its child listing). Roots are plugin entry points, not
		// data-bearing nodes, so enrichment/filtering do not apply here; bare
		// is accepted (a no-op — this shape was always bare) but filter is
		// rejected, since there is nothing meaningful to filter.
		if in.URI == "" {
			if len(filter) > 0 {
				return "", errors.ErrorWithStackf(
					"list_nodes: filter requires a uri; the aggregated roots " +
						"listing has no facets to filter",
				)
			}
			views := make([]nodeView, 0, len(t.roots))
			for _, root := range t.roots {
				views = append(views, nodeView{
					URI:       root.String(),
					Name:      t.rootDisplayLabel(root),
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
		return t.listNodesURI(ctx, in.URI, filter, in.Bare, in.Offset, in.Limit)

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

// patchOutcome turns NodeMutator.PatchNode's applied into the patch_node
// tool result, and is the single place the framework decides what an
// entirely-ignored patch means (cutting-garden#182). Plugins report the
// fact; only this layer judges it, so the judgement is uniform across
// every scheme instead of relitigated per plugin.
//
// An authoritative empty applied is an ERROR here: the caller named fields,
// nothing was applied, and a "patched <uri>" success is precisely the false
// signal that let cutting-garden#180 sit undetected — the caller had no
// reason to re-read. A nil applied carries no information (the plugin does
// not report applied fields), so it keeps the plain success message rather
// than being guessed either way.
func patchOutcome(uri string, applied []string) (string, error) {
	if applied == nil {
		return "patched " + uri, nil
	}

	if len(applied) == 0 {
		return "", errors.BadRequestf(
			"patch_node: nothing was applied to %s — the plugin recognized"+
				" none of the fields in the body, so the node is unchanged."+
				" Call describe_node_types to see which fields this node"+
				" type accepts on patch.",
			uri,
		)
	}

	return fmt.Sprintf(
		"patched %s (applied: %s)", uri, strings.Join(applied, ", "),
	), nil
}

// filteredListingView is list_nodes(uri)'s output shape whenever a filter
// was requested (cutting-garden#160): a wrapper carrying the honest
// filter-precedence signal (RFC 0012 §6) alongside the (possibly narrowed)
// nodes, so a caller always knows whether the returned set was actually
// filtered. With no filter requested, list_nodes(uri) keeps returning a
// bare JSON array of nodeView (now enriched by default) — unwrapped, for
// continuity with the pre-#160 shape and with resources/read's listing.
type filteredListingView struct {
	Nodes []nodeView `json:"nodes"`
	// Filter is the requested filter string, echoed back for clarity.
	Filter string `json:"filter"`
	// FilterApplied is true iff Nodes was actually narrowed by Filter
	// (filterMode "plugin" or "host"); false means the filter could NOT be
	// applied (filterMode "none") and Nodes is the UNFILTERED listing —
	// never silently presented as filtered.
	FilterApplied bool `json:"filterApplied"`
	// FilterMode names which precedence branch produced Nodes: "plugin"
	// (the scheme's own efficient filtered fetch), "host" (framework-side
	// FacetFilter.Matches over already-populated Facets), or "none" (could
	// not filter; Nodes is unfiltered).
	FilterMode string `json:"filterMode"`
}

// listNodesURI implements the list_nodes(uri) branch: the byte-for-byte
// unchanged default (delegates to t.reader.ReadResource, cutting-garden#86)
// when neither filter nor bare is requested, and the enrichment/filter/bare
// paths (cutting-garden#160) otherwise. bare's cheap path (plain ListRoots,
// no EnrichedLister fetch) applies only with an empty filter — combining
// bare with a filter still pays whatever fetch the filter requires; only
// the OUTPUT is stripped down to the bare shape.
func (t *Tools) listNodesURI(
	ctx context.Context,
	uri string,
	filter cutting_garden_plugins.FacetFilter,
	bare bool,
	offset, limit int,
) (string, error) {
	if len(filter) == 0 && !bare {
		// list_nodes is pure child enumeration: it never fetches a node's
		// own body (RFC 0018 §7.3), so it reads children-only — which is
		// byte-for-byte the pre-RFC-0018 default (cutting-garden#86).
		res, err := t.reader.ReadNode(ctx, uri, contentChildren)
		if err != nil {
			return "", err
		}
		text := renderContents(res.Contents)
		// A byte-for-byte-unchanged default is REQUIRED (cutting-garden#86):
		// skip pagination entirely when neither param was given, rather
		// than slicing with limit=0 (which would empty a listing).
		if limit > 0 || offset > 0 {
			var perr error
			text, perr = paginateListingText(text, offset, limit)
			if perr != nil {
				return "", errors.Wrap(perr)
			}
		}
		return text, nil
	}

	if len(filter) == 0 {
		// bare, no filter: the cheap path — plain ListRoots only, no
		// EnrichedLister fetch. A childless result may be a leaf (or an
		// empty container); that rare case falls back to the full
		// (enriched) ReadResource path so a leaf body read still works
		// through list_nodes, exactly as the non-bare path does.
		u, lister, err := t.resolveLister(uri)
		if err != nil {
			return "", err
		}
		nodes, err := lister.ListRoots(ctx, u)
		if err != nil {
			return "", errors.Wrapf(err, "list roots under %s", uri)
		}
		if len(nodes) == 0 {
			res, err := t.reader.ReadNode(ctx, uri, contentChildren)
			if err != nil {
				return "", err
			}
			return renderContents(res.Contents), nil
		}
		views := make([]nodeView, 0, len(nodes))
		for _, n := range nodes {
			views = append(views, bareNodeView(lister, n))
		}
		views = paginate(views, offset, limit)
		body, err := json.MarshalIndent(views, "", "  ")
		if err != nil {
			return "", errors.Wrap(err)
		}
		return string(body), nil
	}

	// A filter is requested (bare or not): resolve the lister directly and
	// run the RFC 0012 §6 precedence (plugin -> host -> honest-unfiltered).
	u, lister, err := t.resolveLister(uri)
	if err != nil {
		return "", err
	}

	// Validate against the plugin's declared facet schema BEFORE listing
	// (cutting-garden#161, same rule read_facets applies): an undeclared
	// dimension or an out-of-domain closed-dimension value is rejected
	// with an actionable error rather than silently narrowing to nothing.
	var dims []cutting_garden_plugins.NodeTypeFacets
	if describer, ok := lister.(cutting_garden_plugins.FacetDescriber); ok {
		dims = describer.DescribeFacets()
	}
	if verr := filter.Validate(dims); verr != nil {
		return "", errors.Wrapf(verr, "list_nodes %s (filtered)", uri)
	}

	nodes, mode, err := enrichedListing(ctx, lister, u, filter)
	if err != nil {
		return "", errors.Wrapf(err, "list %s (filtered)", uri)
	}
	views := make([]nodeView, 0, len(nodes))
	for _, n := range nodes {
		if bare {
			views = append(views, bareNodeView(lister, n))
		} else {
			views = append(views, enrichedNodeView(lister, n))
		}
	}
	views = paginate(views, offset, limit)
	body, err := json.MarshalIndent(filteredListingView{
		Nodes:         views,
		Filter:        filterString(filter),
		FilterApplied: mode != filterModeNone,
		FilterMode:    mode,
	}, "", "  ")
	if err != nil {
		return "", errors.Wrap(err)
	}
	return string(body), nil
}

// filterString renders a FacetFilter back to its "dim=val,dim2=val2" wire
// form, for echoing in filteredListingView.
func filterString(filter cutting_garden_plugins.FacetFilter) string {
	parts := make([]string, 0, len(filter))
	for _, p := range filter {
		parts = append(parts, p.Dimension+"="+p.Value)
	}
	return strings.Join(parts, ",")
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
// renderReadNode renders a read_node result to a single tool text value.
// In `both` mode a body-bearing container yields both a body block and a
// child-listing block; mimeObject and mimeListing are the same media type,
// so the two are told apart structurally — the child listing is the JSON
// array, the body the JSON object — and combined into one
// {"body":…, "children":…} object so the tool text stays a single JSON
// value. A raw-bytes blob link, when present, is appended as a note line as
// renderContents does. With only one of the two (a leaf object, or a plain
// child array) the output is that value alone — byte-for-byte the
// pre-RFC-0018 read_node shape.
func renderReadNode(contents []protocol.ResourceContent) string {
	var bodyText, listingText, rawNote string
	for _, c := range contents {
		switch {
		case c.MimeType == mimeFacets:
			continue
		case c.Text != "":
			if strings.HasPrefix(strings.TrimSpace(c.Text), "[") {
				listingText = c.Text
			} else {
				bodyText = c.Text
			}
		case c.URI != "":
			rawNote = "raw bytes: " + c.URI
			if c.MimeType != "" {
				rawNote += " (" + c.MimeType + ")"
			}
		}
	}

	var out string
	switch {
	case bodyText != "" && listingText != "":
		combined, err := json.MarshalIndent(map[string]json.RawMessage{
			"body":     json.RawMessage(bodyText),
			"children": json.RawMessage(listingText),
		}, "", "  ")
		if err != nil {
			// Unreachable (both are already valid JSON); fall back to the
			// generic concatenation rather than dropping content.
			return renderContents(contents)
		}
		out = string(combined)
	case bodyText != "":
		out = bodyText
	default:
		out = listingText
	}

	if rawNote != "" {
		if out != "" {
			out += "\n"
		}
		out += rawNote
	}

	return out
}

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
	// ListingFields are the human-readable keys a node of this type may
	// carry in an enriched listing's `fields` (cutting-garden#160) — e.g.
	// a caldav object's summary/status/dtstart/dtend/duration/location/due.
	// Empty means the
	// plugin declares no listing fields for this type (it may still
	// enrich with Facets alone, via ListRoots or EnrichedLister).
	ListingFields []listingFieldSchema `json:"listingFields,omitempty"`
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
	// Values lists the dimension's complete value domain, in the plugin's
	// declared order, when Closed is true (cutting-garden#161) — e.g.
	// ["overdue","today","this-week","later"] for due_band — so a caller
	// can look up a valid filter value here instead of guessing one.
	// Absent when Closed is false: an open dimension's values are
	// discovered at enumeration, not known up front.
	Values []string `json:"values,omitempty"`
	// RevalidateAfterSeconds, when nonzero, marks the dimension VOLATILE
	// (RFC 0012 §11.3): its bucketing is a function of (data, now), so a
	// memoized summary containing it expires after this many seconds even
	// with an unmoved change token. Zero (the default, omitted) means pure.
	RevalidateAfterSeconds int64 `json:"revalidateAfterSeconds,omitempty"`
}

// facetDimSchemas projects a plugin's declared FacetDimensions into their
// describe_node_types view. A non-nil Values list marks a closed domain and
// is surfaced verbatim (cutting-garden#161) so a filter value is
// discoverable rather than guessed.
func facetDimSchemas(
	dims []cutting_garden_plugins.FacetDimension,
) []facetDimSchema {
	out := make([]facetDimSchema, 0, len(dims))
	for _, d := range dims {
		var values []string
		if d.Values != nil {
			values = make([]string, len(d.Values))
			for i, v := range d.Values {
				values[i] = v.Key
			}
		}
		out = append(out, facetDimSchema{
			Key:                    d.Key,
			Label:                  d.Label,
			Kind:                   string(d.Kind),
			Multi:                  d.Multi,
			Closed:                 d.Values != nil,
			Values:                 values,
			RevalidateAfterSeconds: int64(d.RevalidateAfter.Seconds()),
		})
	}
	return out
}

// listingFieldSchema describes one declared listing field of a node type,
// for the describe_node_types tool: its key and display label. Symmetric
// with facetDimSchema. See cutting-garden#160.
type listingFieldSchema struct {
	Key   string `json:"key"`
	Label string `json:"label,omitempty"`
}

// listingFieldSchemas projects a plugin's declared ListingFields into their
// describe_node_types view.
func listingFieldSchemas(
	fields []cutting_garden_plugins.ListingField,
) []listingFieldSchema {
	out := make([]listingFieldSchema, 0, len(fields))
	for _, f := range fields {
		out = append(out, listingFieldSchema{Key: f.Key, Label: f.Label})
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
		listingFields := map[string][]cutting_garden_plugins.ListingField{}
		if lfd, ok := p.(cutting_garden_plugins.ListingFieldsDescriber); ok {
			for _, ntf := range lfd.DescribeListingFields() {
				listingFields[ntf.Tag] = ntf.Fields
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
			if fields, ok := listingFields[nt.Tag]; ok {
				ts.ListingFields = listingFieldSchemas(fields)
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
// way. The framework-side fallback when no plugin supplies a friendlier
// label (see rootDisplayLabel, cutting-garden#120).
func rootLabel(u *url.URL) string {
	if trimmed := strings.TrimRight(u.Path, "/"); trimmed != "" {
		return path.Base(trimmed)
	}
	if u.Host != "" {
		return u.Host
	}
	return u.String()
}

// rootDisplayLabel resolves a root's display name for the no-uri
// list_nodes listing: the RootLabeler-supplied friendly label
// (cutting-garden#120, e.g. a caldav calendar's DAV displayname) when
// present, else the framework's default URL-derived rootLabel().
func (t *Tools) rootDisplayLabel(u *url.URL) string {
	if label, ok := t.rootLabels[u.String()]; ok && label != "" {
		return label
	}
	return rootLabel(u)
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
