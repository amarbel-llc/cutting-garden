package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/mcp_tool_perms"
	"code.linenisgreat.com/cutting-garden/internal/node_view"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/purse-first/libs/go-mcp/protocol"
)

// mutate_nodes is the ONE write tool (cutting-garden#315): every create,
// put, patch and delete goes through it as a batch, a single write being a
// one-op batch. A call is a list of groups — each an anchor plus its ops
// (or a sweep) — so batches spanning several plugins can come later without
// a shape change; for now exactly one group is accepted. Ops address nodes
// by the id organize prints for them, relative to the group's anchor
// (node_view.NodeURIFor), and a create may name a temp id (`+name`) that
// later ops in the group address.

const mutateNodesSchema = `{"type":"object","required":["groups"],"properties":{` +
	`"groups":{"type":"array","minItems":1,"maxItems":1,"description":"the batch, one group per plugin; exactly one group is accepted for now","items":{"type":"object","required":["anchor"],"properties":{` +
	`"anchor":{"type":"string","description":"the node URI every op in the group is addressed relative to (e.g. a calendar, mailbox, or org file URI); it fixes the group's plugin"},` +
	`"atomicity":{"type":"string","enum":["best-effort","atomic"],"description":"optional, default best-effort: each op applies independently and is reported on its own; atomic is all-or-nothing and is refused unless the plugin supports it"},` +
	`"ops":{"type":"array","description":"the changeset, applied in order. Exactly ONE of ops or sweep.","items":{"type":"object","required":["kind","id"],"properties":{` +
	`"kind":{"type":"string","enum":["create","put","patch","delete"]},` +
	`"id":{"type":"string","description":"the node, as the id organize and list -format espalier print for it under the anchor (e.g. task1.ics, T1, _inbox.org?id=…), or a full URI under the anchor. On a create, a temp id (+name, +\"name\", or bare +) asks the source to name the node; later ops in the group address it as +name"},` +
	`"in":{"type":"string","description":"temp-id creates only: the container to create in, as an id under the anchor or an earlier +name; default the anchor itself"},` +
	`"body":{"type":"string","description":"the create/put/patch body as a STRING (omit for delete): JSON-encode a JSON body into it, or pass raw text for a raw-format type (caldav iCalendar). describe_node_types lists what each type accepts"},` +
	`"type":{"type":"string","description":"creates only: the node type tag, e.g. caldav-object-vtodo-v1; checked against describe_node_types (a type the source names, like a jira issue, needs a temp id)"}}}},` +
	`"sweep":{"type":"object","required":["op"],"description":"apply one op to every node under the anchor that matches filter. Exactly ONE of ops or sweep.","properties":{` +
	`"filter":{"type":"string","description":"comma-separated dimension=value predicates (AND-composed), as for list_nodes; empty matches every node under the anchor"},` +
	`"op":{"type":"object","required":["kind"],"properties":{` +
	`"kind":{"type":"string","enum":["put","patch","delete"]},` +
	`"body":{"type":"string"}}}}}}}}}}`

func mutateNodesToolDef() protocol.ToolV1 {
	return protocol.ToolV1{
		Name: mcp_tool_perms.ToolMutateNodes,
		Description: "Create, replace, patch, or delete nodes — the one write " +
			"tool, so batch your writes: a single write is a one-op batch. " +
			"Pass one group: an anchor URI and either ops (applied in order) " +
			"or a sweep (one op applied to every node under the anchor " +
			"matching a filter). Each op addresses its node by the id organize " +
			"prints under the anchor (or a full URI under it). Best-effort by " +
			"default: the result lists applied, patched_nothing (accepted, " +
			"nothing changed), failed (with the error), and created (temp id → " +
			"URI) per id. Bodies are STRINGS — JSON-encode a JSON body. A " +
			"patch body holds only the fields to change; describe_node_types " +
			"lists the fields each type accepts. Example: " +
			`{"groups":[{"anchor":"caldav://host/cal/","ops":[{"kind":"create","id":"+t","type":"caldav-object-vtodo-v1","body":"{\"summary\":\"call\"}"},{"kind":"patch","id":"+t","body":"{\"priority\":1}"},{"kind":"delete","id":"old.ics"}]}]}` +
			". Sweep example (mark every unread story read): " +
			`{"groups":[{"anchor":"newsblur://stories","sweep":{"filter":"read=unread","op":{"kind":"patch","body":"{\"read\":true}"}}}]}`,
		InputSchema: json.RawMessage(mutateNodesSchema),
		Annotations: annotationFor(mcp_tool_perms.ToolMutateNodes),
	}
}

type mutateOpInput struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	In   string `json:"in"`
	Body string `json:"body"`
	Type string `json:"type"`
}

type mutateGroupInput struct {
	Anchor    string          `json:"anchor"`
	Atomicity string          `json:"atomicity"`
	Ops       []mutateOpInput `json:"ops"`
	Sweep     *struct {
		Filter string        `json:"filter"`
		Op     mutateOpInput `json:"op"`
	} `json:"sweep"`
}

type mutateFailureView struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

type mutateCreatedView struct {
	TempID string `json:"temp_id"`
	ID     string `json:"id"`
	URI    string `json:"uri"`
}

type mutateGroupView struct {
	Anchor         string              `json:"anchor"`
	Applied        []string            `json:"applied"`
	PatchedNothing []string            `json:"patched_nothing,omitempty"`
	Failed         []mutateFailureView `json:"failed,omitempty"`
	Created        []mutateCreatedView `json:"created,omitempty"`
	Atomic         bool                `json:"atomic,omitempty"`
	// Complete and Partial mark a sweep whose enumeration a plugin reported
	// bounded — the same signal list_nodes carries (cutting-garden#266).
	Complete *bool                                   `json:"complete,omitempty"`
	Partial  []cutting_garden_plugins.PartialListing `json:"partial,omitempty"`
}

// mutateGroup is one group resolved against its plugin: the domain request
// plus what rendering needs to report results in the caller's own ids.
type mutateGroup struct {
	anchor   string
	lister   cutting_garden_plugins.RootLister
	req      cutting_garden_plugins.BulkRequest
	idOf     map[string]string // node URI → the id the caller sent
	spelling map[string]string // temp key → the caller's spelling
}

func (t *Tools) callMutateNodes(
	ctx context.Context, args json.RawMessage,
) (string, error) {
	var in struct {
		Groups []mutateGroupInput `json:"groups"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", errors.BadRequestf("mutate_nodes: arguments: %s", err)
	}

	switch len(in.Groups) {
	case 0:
		return "", errors.BadRequestf("mutate_nodes: groups is empty")
	case 1:
	default:
		return "", errors.BadRequestf(
			"mutate_nodes: %d groups given; batches spanning several"+
				" anchors are not supported yet — send one group per call",
			len(in.Groups),
		)
	}

	group, err := t.buildMutateGroup(in.Groups[0])
	if err != nil {
		return "", err
	}

	ctx, partial := cutting_garden_plugins.WithPartialListings(ctx)
	result, err := t.dispatchMutateGroup(ctx, group)
	if err != nil {
		return "", err
	}

	view := group.render(result, partial.All())
	body, err := json.MarshalIndent(
		struct {
			Groups []mutateGroupView `json:"groups"`
		}{[]mutateGroupView{view}}, "", "  ",
	)
	if err != nil {
		return "", errors.Wrap(err)
	}

	// Nothing landed: an error result, so a caller skimming for success
	// cannot read a fully refused batch as done (cutting-garden#180). The
	// per-id detail rides along in the message.
	if len(view.Applied) == 0 &&
		(len(view.Failed) > 0 || len(view.PatchedNothing) > 0) {
		return "", errors.BadRequestf(
			"mutate_nodes: nothing was applied\n%s", body,
		)
	}

	return string(body), nil
}

func (t *Tools) buildMutateGroup(in mutateGroupInput) (*mutateGroup, error) {
	if in.Anchor == "" {
		return nil, errors.BadRequestf("mutate_nodes: group has no anchor")
	}
	anchorURL, lister, err := t.resolveLister(in.Anchor)
	if err != nil {
		return nil, err
	}

	group := &mutateGroup{
		anchor:   in.Anchor,
		lister:   lister,
		idOf:     make(map[string]string),
		spelling: make(map[string]string),
		req: cutting_garden_plugins.BulkRequest{
			Atomicity: cutting_garden_plugins.BulkAtomicity(in.Atomicity),
		},
	}
	if in.Atomicity == "" {
		group.req.Atomicity = cutting_garden_plugins.BulkBestEffort
	}

	if in.Sweep != nil {
		if len(in.Ops) > 0 {
			return nil, errors.BadRequestf(
				"mutate_nodes: a group takes exactly one of ops or sweep",
			)
		}
		filter, err := cutting_garden_plugins.ParseFacetFilter(in.Sweep.Filter)
		if err != nil {
			return nil, err
		}
		if err := cutting_garden_plugins.ValidateFilterFor(filter, lister); err != nil {
			return nil, errors.BadRequestf("mutate_nodes: sweep filter: %s", err)
		}
		group.req.Sweep = &cutting_garden_plugins.BulkSweep{
			Root:   anchorURL,
			Filter: filter,
			Op: cutting_garden_plugins.BulkOp{
				Kind: cutting_garden_plugins.BulkOpKind(in.Sweep.Op.Kind),
				Body: []byte(in.Sweep.Op.Body),
			},
		}
	}

	defined := make(map[string]bool)
	for i, op := range in.Ops {
		built, err := group.buildOp(i, op, defined)
		if err != nil {
			return nil, err
		}
		group.req.Ops = append(group.req.Ops, built)
	}

	if err := group.req.Validate(); err != nil {
		return nil, err
	}

	return group, nil
}

// buildOp turns one caller op into a BulkOp: a temp id becomes a TempID
// create (when it is new) or a Ref (when an earlier create defined it); any
// other id resolves to the node URI under the anchor.
func (g *mutateGroup) buildOp(
	i int, in mutateOpInput, defined map[string]bool,
) (cutting_garden_plugins.BulkOp, error) {
	op := cutting_garden_plugins.BulkOp{
		Kind: cutting_garden_plugins.BulkOpKind(in.Kind),
		Type: in.Type,
	}
	if in.Kind != string(cutting_garden_plugins.BulkDelete) {
		op.Body = []byte(in.Body)
	}

	key, isTemp, err := tempKey(i, in.ID)
	if err != nil {
		return op, errors.BadRequestf("mutate_nodes: ops[%d]: %s", i, err)
	}

	if op.Kind == cutting_garden_plugins.BulkCreate && op.Type != "" {
		if err := g.checkCreateType(i, op.Type, isTemp); err != nil {
			return op, err
		}
	}

	switch {
	case isTemp && !defined[key]:
		if op.Kind != cutting_garden_plugins.BulkCreate {
			return op, errors.BadRequestf(
				"mutate_nodes: ops[%d] %s %s: no earlier create defines it",
				i, in.Kind, in.ID,
			)
		}
		defined[key] = true
		g.spelling[key] = in.ID
		op.TempID = key
		if err := g.setContainer(i, &op, in.In, defined); err != nil {
			return op, err
		}

	case isTemp:
		if op.Kind == cutting_garden_plugins.BulkCreate {
			return op, errors.BadRequestf(
				"mutate_nodes: ops[%d] creates %s again", i, in.ID,
			)
		}
		op.Ref = key

	default:
		if in.In != "" {
			return op, errors.BadRequestf(
				"mutate_nodes: ops[%d]: in applies only to a temp-id create", i,
			)
		}
		uri, err := node_view.NodeURIFor(g.lister, in.ID, g.anchor)
		if err != nil {
			return op, errors.BadRequestf("mutate_nodes: ops[%d]: %s", i, err)
		}
		op.URI = uri
		g.idOf[uri.String()] = in.ID
	}

	return op, nil
}

// checkCreateType refuses a create the plugin's describe_node_types says
// cannot work, before anything is sent: a type it does not list as writable,
// or a caller-chosen id for a type whose source assigns identity (jira's
// issue keys) — that one needs a temp id. A plugin with no BodyDescriber
// declares nothing, so its creates go through unchecked.
func (g *mutateGroup) checkCreateType(i int, typeTag string, isTemp bool) error {
	describer, ok := g.lister.(cutting_garden_plugins.BodyDescriber)
	if !ok {
		return nil
	}

	bodies := describer.DescribeBodies()
	writable := make([]string, 0, len(bodies))
	for _, body := range bodies {
		if body.Tag != typeTag {
			writable = append(writable, body.Tag)
			continue
		}
		if body.ServerAssignedIdentity && !isTemp {
			return errors.BadRequestf(
				"mutate_nodes: ops[%d]: the source names %s nodes; create it"+
					" with a temp id (+name) instead of an id", i, typeTag,
			)
		}
		return nil
	}

	return errors.BadRequestf(
		"mutate_nodes: ops[%d]: type %q is not creatable here (writable"+
			" types: %s)", i, typeTag, strings.Join(writable, ", "),
	)
}

// setContainer fills a temp-id create's container: the anchor by default,
// an id under it, or an earlier create's temp id.
func (g *mutateGroup) setContainer(
	i int, op *cutting_garden_plugins.BulkOp, in string, defined map[string]bool,
) error {
	if in == "" {
		anchor, err := url.Parse(g.anchor)
		if err != nil {
			return errors.BadRequestf("mutate_nodes: anchor %q: %s", g.anchor, err)
		}
		op.In = anchor
		return nil
	}

	key, isTemp, err := tempKey(-1, in)
	if err != nil {
		return errors.BadRequestf("mutate_nodes: ops[%d] in: %s", i, err)
	}
	if isTemp {
		if !defined[key] || key == op.TempID {
			return errors.BadRequestf(
				"mutate_nodes: ops[%d] in %s: no earlier create defines it", i, in,
			)
		}
		op.InRef = key
		return nil
	}

	container, err := node_view.NodeURIFor(g.lister, in, g.anchor)
	if err != nil {
		return errors.BadRequestf("mutate_nodes: ops[%d] in: %s", i, err)
	}
	op.In = container
	return nil
}

// tempKey recognizes a temp id — `+name`, `+"name"`, or bare `+` (RFC 0015
// §Creation's spellings) — and returns the key BulkOp carries: `+name` for a
// named one, and for a bare `+`, which nothing can refer to, a key unique to
// its op index i (i < 0 when a bare `+` is not allowed).
func tempKey(i int, id string) (key string, isTemp bool, err error) {
	rest, ok := strings.CutPrefix(id, "+")
	if !ok {
		return "", false, nil
	}

	switch {
	case rest == "":
		if i < 0 {
			return "", true, fmt.Errorf("a bare + cannot be referred to")
		}
		return fmt.Sprintf("+\x00%d", i), true, nil
	case strings.HasPrefix(rest, `"`):
		name, err := strconv.Unquote(rest)
		if err != nil || name == "" {
			return "", true, fmt.Errorf("malformed temp id %s", id)
		}
		return "+" + name, true, nil
	default:
		return id, true, nil
	}
}

// dispatchMutateGroup sends the request to the plugin's BulkMutator, or —
// for a plugin with only single-node writes — applies it through the SDK's
// best-effort loop over its NodeMutator, which reports the same per-op
// result (an atomic request is refused there).
func (t *Tools) dispatchMutateGroup(
	ctx context.Context, g *mutateGroup,
) (cutting_garden_plugins.BulkResult, error) {
	var result cutting_garden_plugins.BulkResult
	var err error

	if bulk := t.bulkMutatorFor(g.anchor); bulk != nil {
		result, err = bulk.BulkMutate(ctx, g.req)
	} else {
		_, nm, rerr := t.resolve(g.anchor)
		if rerr != nil {
			return result, errors.BadRequestf(
				"mutate_nodes: the plugin for %q cannot write: %s", g.anchor, rerr,
			)
		}
		enriched, _ := g.lister.(cutting_garden_plugins.EnrichedLister)
		if g.req.Sweep != nil && enriched == nil {
			return result, errors.BadRequestf(
				"mutate_nodes: the plugin for %q cannot sweep (it has no"+
					" filtered listing); send ops instead", g.anchor,
			)
		}
		result, err = cutting_garden_plugins.BestEffortBulkMutate(
			ctx, nm, enriched, g.req,
		)
	}

	if errors.Is(err, cutting_garden_plugins.ErrBulkAtomicUnsupported) {
		return result, errors.BadRequestf(
			"mutate_nodes: the plugin for %q cannot apply a batch atomically;"+
				" omit atomicity for best-effort", g.anchor,
		)
	}
	return result, err
}

func (t *Tools) bulkMutatorFor(anchor string) cutting_garden_plugins.BulkMutator {
	if t.resolveBulk == nil {
		return nil
	}
	if _, bulk, err := t.resolveBulk(anchor); err == nil {
		return bulk
	}
	return nil
}

// render reports the result in the caller's own ids: the id an op was sent
// with, else the id organize would print (sweep matches, created nodes).
func (g *mutateGroup) render(
	result cutting_garden_plugins.BulkResult,
	partial []cutting_garden_plugins.PartialListing,
) mutateGroupView {
	completeness := completenessView(partial)
	view := mutateGroupView{
		Anchor:   g.anchor,
		Applied:  make([]string, 0, len(result.AppliedNodes)),
		Atomic:   result.Atomic,
		Complete: completeness.Complete,
		Partial:  completeness.Partial,
	}

	for _, uri := range result.AppliedNodes {
		view.Applied = append(view.Applied, g.id(uri))
	}
	for _, uri := range result.PatchedNothing {
		view.PatchedNothing = append(view.PatchedNothing, g.id(uri))
	}
	for _, failure := range result.Failed {
		id := g.spelling[failure.Ref]
		if id == "" && failure.Ref != "" {
			id = failure.Ref
		}
		if id == "" {
			id = g.id(failure.URI)
		}
		view.Failed = append(view.Failed, mutateFailureView{
			ID: id, Error: failure.Err,
		})
	}
	for _, created := range result.Created {
		if created.URI == nil {
			continue
		}
		view.Created = append(view.Created, mutateCreatedView{
			TempID: g.spelling[created.TempID],
			ID:     g.id(created.URI),
			URI:    created.URI.String(),
		})
	}

	return view
}

func (g *mutateGroup) id(uri *url.URL) string {
	if uri == nil {
		return ""
	}
	if id, ok := g.idOf[uri.String()]; ok {
		return id
	}
	return node_view.RelativeIDFor(g.lister, uri.String(), g.anchor)
}
