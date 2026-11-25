package mcp

import (
	"context"
	"encoding/json"
	"net/url"

	"code.linenisgreat.com/cutting-garden/internal/mcp_tool_perms"
	"code.linenisgreat.com/cutting-garden/internal/mutate"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/purse-first/libs/go-mcp/protocol"
)

// mutate_nodes is the ONE write tool (cutting-garden#315): every create,
// put, patch and delete goes through it as a batch, a single write being a
// one-op batch. The write engine itself — group building, dispatch, result
// rendering — lives in internal/mutate, shared with the `mutate` command;
// this file is the MCP front: the tool's schema and description, the
// argument decode, and the mapping of "nothing was applied" to an error
// result.

const mutateNodesSchema = `{"type":"object","required":["groups"],"properties":{` +
	`"strict":{"type":"boolean","description":"optional, default false (tolerant): when true, a patch op naming a key its node type does not accept is refused before anything is written for it, naming the keys; a patch whose node type declares no accepted keys is refused too, and so is a patch sweep. When false, undeclared keys are dropped and reported as ignored. Applies to the whole call"},` +
	`"groups":{"type":"array","minItems":1,"description":"the batch: one group per anchor, applied in order. Groups are independent: each has its own temp ids and atomicity, and one group's refusal does not stop the next (it reports its error); a malformed group refuses the whole call before anything is written","items":{"type":"object","required":["anchor"],"properties":{` +
	`"anchor":{"type":"string","description":"the node URI every op in the group is addressed relative to (e.g. a calendar, mailbox, or org file URI); it fixes the group's plugin"},` +
	`"atomicity":{"type":"string","enum":["best-effort","atomic"],"description":"optional, default best-effort: each op applies independently and is reported on its own; atomic is all-or-nothing and is refused unless the plugin supports it"},` +
	`"ops":{"type":"array","description":"the changeset, applied in order. Exactly ONE of ops or sweep.","items":{"type":"object","required":["kind","id"],"properties":{` +
	`"kind":{"type":"string","enum":["create","put","patch","delete"]},` +
	`"id":{"type":"string","description":"the node, as the id organize and list -format espalier print for it under the anchor (e.g. task1.ics, T1, _inbox.org?id=… under the org:// folder anchor; under a file anchor id=… or the same _inbox.org?id=…), or a full URI under the anchor. On a create, a temp id (+name, +\"name\", or bare +) asks the source to name the node; later ops in the group address it as +name"},` +
	`"in":{"type":"string","description":"temp-id creates only: the container to create in, as an id under the anchor or an earlier +name; default the anchor itself"},` +
	`"body":{"type":"string","description":"the create/put/patch body as a STRING (omit for delete, unless describe_node_types says the type's delete takes options, e.g. org's {\"subtree\": true}; a source whose delete takes none refuses a body): JSON-encode a JSON body into it, or pass raw text for a raw-format type (caldav iCalendar). describe_node_types lists what each type accepts"},` +
	`"type":{"type":"string","description":"creates only: the node type tag, e.g. caldav-object-vtodo-v1; checked against describe_node_types (a type the source names, like a jira issue, needs a temp id and the container it belongs under, given as the group anchor or in; describe_node_types' createIn says which)"}}}},` +
	`"sweep":{"type":"object","required":["op"],"description":"apply one op to every node under the anchor that matches filter. Exactly ONE of ops or sweep.","properties":{` +
	`"filter":{"type":"string","description":"one trellis step, as for list_nodes' filter (e.g. \"read=unread\", \"^status=completed\"); empty matches every node under the anchor. A term beyond plain dimension=value is matched by the host, which then writes the matched nodes as an explicit changeset"},` +
	`"op":{"type":"object","required":["kind"],"properties":{` +
	`"kind":{"type":"string","enum":["put","patch","delete"]},` +
	`"body":{"type":"string"}}}}}}}}}}`

func mutateNodesToolDef() protocol.ToolV1 {
	return protocol.ToolV1{
		Name: mcp_tool_perms.ToolMutateNodes,
		Description: "Create, replace, patch, or delete nodes — the one write " +
			"tool, so batch your writes: a single write is a one-op batch. " +
			"Pass one or more groups, applied in order: each an anchor URI and " +
			"either ops (applied in order) or a sweep (one op applied to every " +
			"node under the anchor matching a filter). Temp ids and atomicity " +
			"are per group. Each op addresses its node by the id organize " +
			"prints under the anchor (or a full URI under it). Best-effort by " +
			"default: the result lists applied, patched_nothing (accepted, " +
			"nothing changed), failed (with the error), and created (temp id → " +
			"URI) per id, plus outcomes, present when the source reports " +
			"per-op detail (otherwise read applied/failed): one entry per op " +
			"in your ids with " +
			"status (applied, patched_nothing, failed, deleted, not_found — " +
			"prefer it to the lists: a deleted node is also in applied, a " +
			"not_found one also in failed), fields (the patch keys that took), " +
			"ignored (the patch keys the source tolerated but did not apply, when it reports them), " +
			"uri (the node's address afterwards, only when it changed: an org " +
			"headline edited by ?h= is then addressed by ?id=) and name (a " +
			"deleted node's name). Bodies are STRINGS — JSON-encode a JSON body. A " +
			"patch body holds only the fields to change; describe_node_types " +
			"lists the fields each type accepts. A patch naming a key its type " +
			"does not accept (for a source that declares them) drops that key " +
			"and reports it in ignored; pass strict: true to refuse such an op, " +
			"or a patch to a source that declares none, before anything is " +
			"written for it. Example: " +
			`{"groups":[{"anchor":"caldav://host/cal/","ops":[{"kind":"create","id":"+t","type":"caldav-object-vtodo-v1","body":"{\"summary\":\"call\"}"},{"kind":"patch","id":"+t","body":"{\"priority\":1}"},{"kind":"delete","id":"old.ics"}]}]}` +
			". Sweep example (mark every unread story read): " +
			`{"groups":[{"anchor":"newsblur://stories","sweep":{"filter":"read=unread","op":{"kind":"patch","body":"{\"read\":true}"}}}]}`,
		InputSchema: json.RawMessage(mutateNodesSchema),
		Annotations: annotationFor(mcp_tool_perms.ToolMutateNodes),
	}
}

// mutateEngine is the write engine wired to this server's resolvers, config
// override, log and read caches.
func (t *Tools) mutateEngine() *mutate.Engine {
	return &mutate.Engine{
		ResolveLister: t.resolveLister,
		ResolveNode:   t.resolve,
		ResolveBulk:   t.resolveBulk,
		TagsOverride:  t.tagsOverride,
		Log:           t.log,
		Forget:        t.forgetWrittenSource,
	}
}

func (t *Tools) callMutateNodes(
	ctx context.Context, args json.RawMessage,
) (string, error) {
	countMutateCall(args)

	var in mutate.Input
	if err := json.Unmarshal(args, &in); err != nil {
		return "", errors.BadRequestf("mutate_nodes: arguments: %s", err)
	}

	via := ""
	if caller, ok := callerFrom(ctx); ok {
		via = caller.logSuffix()
	}

	result, err := t.mutateEngine().Apply(ctx, in, via)
	if err != nil {
		return "", err
	}

	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", errors.Wrap(err)
	}

	// Nothing landed in any group: an error result, so a caller skimming for
	// success cannot read a fully refused batch as done (cutting-garden#180).
	// The per-id detail rides along in the message.
	if result.NothingApplied() {
		return "", errors.BadRequestf(
			"mutate_nodes: nothing was applied\n%s", body,
		)
	}

	return string(body), nil
}

// sourceForgetter is the read surface's memo, dropped after a write so the
// next read through this server sees it (*Resources satisfies it).
type sourceForgetter interface {
	ForgetSource(written *url.URL)
}

// forgetWrittenSource drops the read caches of the anchor's source after a
// dispatch — even a failed one, which may have applied part of a batch.
func (t *Tools) forgetWrittenSource(anchor string) {
	forgetter, ok := t.reader.(sourceForgetter)
	if !ok {
		return
	}
	if u, err := url.Parse(anchor); err == nil {
		forgetter.ForgetSource(u)
	}
}
