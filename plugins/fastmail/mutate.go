package fastmail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

var (
	_ cutting_garden_plugins.NodeMutator   = (*Plugin)(nil)
	_ cutting_garden_plugins.BodyDescriber = (*Plugin)(nil)
)

// patchFieldMailbox is the thread's move field (cutting-garden#401): the
// full name-path of the mailbox to move the thread to.
const patchFieldMailbox = "mailbox"

// threadPatchFields is the recognized PatchNode key set (cutting-garden#182),
// SORTED so the applied report is deterministic: a thread's designated tag set
// and its move. `subject`, `from` and `date` are read-only projections of the
// member messages, and `has_attachment` has no stored counterpart at all. A
// body naming another key is TOLERATED but never reported in applied, so a
// caller can always tell what landed.
var threadPatchFields = []string{patchFieldMailbox, listingFieldTags}

// DescribeBodies declares the thread's patch for describe_node_types: the two
// fields, and a move as the example, since a move is the write a capability
// profile names as one cg-field-v1 step (FDR 0024).
func (Plugin) DescribeBodies() []cutting_garden_plugins.NodeTypeBody {
	return []cutting_garden_plugins.NodeTypeBody{{
		Tag: typeThread,
		Accepts: []string{
			`application/json, patch ops only, one of: "mailbox" (string: the full name-path of ` +
				`a mailbox, as a mailbox URI carries it, e.g. "Archive", "Inbox" or ` +
				`"payee/-one_medical") moves the thread out of the mailbox its URI is listed under ` +
				`and into that one: each member message in the source mailbox leaves it and joins ` +
				`the target, its other mailboxes kept, and members elsewhere (a Sent reply) are ` +
				`untouched. The result's uri is the thread under the target mailbox. Or "tags" ` +
				`(the whole tag set, dodder-hyphen tags plus the state tags _inbox, _unread, ` +
				`_flagged, _trash) replaces the thread's tag set: a label tag is a mailbox ` +
				`membership, a new tag creates its mailbox. A patch naming both is refused. ` +
				`Threads are never deleted: _trash or a move to "Trash" is the soft form.`,
		},
		Example: map[string]any{patchFieldMailbox: "Archive"},
	}}
}

// PatchNode is PatchNodeDetail without the new URI.
func (p Plugin) PatchNode(
	ctx context.Context, node *url.URL, body io.Reader,
) ([]string, error) {
	detail, err := p.PatchNodeDetail(ctx, node, body)
	return detail.Applied, err
}

// PatchNodeDetail moves a thread between mailboxes ("mailbox") or replaces its
// tag set ("tags"), fanning the change out over the member messages in ONE
// JMAP request (fastmail tags slice 1, D3/D4; cutting-garden#401).
//
// The node URI MUST be a thread (`fastmail://<account>/<mailbox>/?thread=…`):
// a mailbox, an email or a raw-message node is a bad request, since both
// writes are properties of the thread and nothing else here is writable. The
// body is flat JSON naming one of the two keys; the move is planned by
// planThreadMove, and the tag set is a FULL-SET replacement:
//
//	{"tags": ["_flagged", "_inbox", "payee-one_medical"]}
//
// PatchNode re-reads the live members and mailbox tree, diffs the requested
// set against the presented one (the same derivation the read side renders),
// and turns the difference into per-member `Email/set` patches plus, for a tag
// no mailbox realizes yet, a `Mailbox/set` create referenced from the same
// request by its creation id (D4). A `_`-prefixed tag that is not one of the
// four writable state tags (`_inbox`, `_unread`, `_flagged`, `_trash`) is
// refused by name (D2) BEFORE any of that — like the body shape, whether a
// tag can be written is a pure question, so a typo costs no round-trip and
// nothing is written.
//
// applied is `["mailbox"]` for a move, and `["tags"]` plus one
// `mailbox:<full/path>` entry per created mailbox for a tag-set write. A body
// naming no recognized key, and a write that changes nothing, apply nothing,
// issue no request, and report a non-nil EMPTY applied rather than a bare
// success.
func (Plugin) PatchNodeDetail(
	ctx context.Context, node *url.URL, body io.Reader,
) (cutting_garden_plugins.PatchDetail, error) {
	var none cutting_garden_plugins.PatchDetail
	if node == nil {
		return none, errors.ErrorWithStackf(
			"fastmail plugin: PatchNode requires a node URI",
		)
	}
	ref, err := classifyURI(node)
	if err != nil {
		return none, err
	}
	if ref.kind != kindThread {
		return none, errors.BadRequestf(
			"fastmail plugin: only thread nodes are patchable; %q is not one\n"+
				"hint: pass `fastmail://<account>/<mailbox>/?thread=<id>`", node,
		)
	}

	raw, err := io.ReadAll(body)
	if err != nil {
		return none, errors.Wrap(err)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return none, errors.BadRequestf(
			"fastmail plugin: PatchNode body must be JSON; got empty body",
		)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return none, errors.BadRequestf("fastmail plugin: invalid patch JSON: %s", err)
	}

	// Which keys this type recognizes is decided BEFORE any network
	// round-trip, so a body naming nothing we understand costs no fetch and
	// still reports honestly.
	recognized := cutting_garden_plugins.RecognizedPatchFields(fields, threadPatchFields)
	switch {
	case len(recognized) == 0:
		return cutting_garden_plugins.PatchDetail{Applied: recognized}, nil
	case len(recognized) > 1:
		return none, errors.BadRequestf(
			"fastmail plugin: a patch moves the thread (%q) or replaces its tag set (%q), not both",
			patchFieldMailbox, listingFieldTags,
		)
	case recognized[0] == patchFieldMailbox:
		var target string
		if err := json.Unmarshal(fields[patchFieldMailbox], &target); err != nil ||
			strings.Trim(target, "/") == "" {
			return none, errors.BadRequestf(
				"fastmail plugin: patch field %q must be a mailbox's full name-path, like \"Archive\"",
				patchFieldMailbox,
			)
		}
		return moveThread(ctx, ref, target)
	}
	applied, err := patchThreadTags(ctx, ref, fields[listingFieldTags])
	return cutting_garden_plugins.PatchDetail{Applied: applied}, err
}

// moveThread moves the thread at ref out of the mailbox its URI names and into
// the one at the name-path target (cutting-garden#401).
func moveThread(
	ctx context.Context, ref nodeRef, target string,
) (cutting_garden_plugins.PatchDetail, error) {
	var none cutting_garden_plugins.PatchDetail
	c, err := resolveClient(ref)
	if err != nil {
		return none, err
	}
	tree, err := c.loadMailboxTree(ctx)
	if err != nil {
		return none, err
	}
	source, ok := tree.resolvePath(ref.mailboxPath)
	if !ok {
		return none, errors.BadRequestf(
			"fastmail plugin: no mailbox at path %q in account %q",
			pathString(ref.mailboxPath), ref.account,
		)
	}
	dest, ok := tree.resolvePath(strings.Split(strings.Trim(target, "/"), "/"))
	if !ok || !inScope(dest) {
		return none, errors.BadRequestf(
			"fastmail plugin: no mailbox at path %q in account %q", target, ref.account,
		)
	}
	members, err := c.threadMembers(ctx, ref.threadID)
	if err != nil {
		return none, err
	}
	updates := planThreadMove(members, source.ID, dest.ID)
	if len(updates) == 0 {
		return cutting_garden_plugins.PatchDetail{Applied: []string{}}, nil
	}
	if _, err := c.applyThreadPatch(ctx, threadPatchRequest{Updates: updates}); err != nil {
		return none, err
	}
	return cutting_garden_plugins.PatchDetail{
		Applied: []string{patchFieldMailbox},
		NewURI:  threadURI(ref.account, tree.path(dest.ID), ref.threadID),
	}, nil
}

// planThreadMove is the PURE move planner: every member in sourceID leaves it
// and joins destID, keeping its other mailboxes; a member not in sourceID is
// untouched, and a move onto the same mailbox plans nothing. A member always
// ends in destID, so JMAP's at-least-one-mailbox rule cannot fire.
func planThreadMove(members []Email, sourceID, destID string) map[string]emailPatch {
	updates := map[string]emailPatch{}
	if sourceID == destID {
		return updates
	}
	for _, member := range members {
		if !member.MailboxIDs[sourceID] {
			continue
		}
		patch := emailPatch{"mailboxIds/" + sourceID: nil}
		if !member.MailboxIDs[destID] {
			patch["mailboxIds/"+destID] = true
		}
		updates[member.ID] = patch
	}
	return updates
}

// patchThreadTags replaces the thread's tag set from the "tags" value raw.
func patchThreadTags(
	ctx context.Context, ref nodeRef, raw json.RawMessage,
) ([]string, error) {
	applied := []string{listingFieldTags}
	// A recognized key carrying an unusable value is the caller's own bug,
	// never a tolerated field (FDR 0020, cutting-garden#185).
	var newTags []string
	if err := json.Unmarshal(raw, &newTags); err != nil {
		return nil, errors.BadRequestf(
			"fastmail plugin: patch field %q must be an array of strings: %s",
			listingFieldTags, err,
		)
	}
	// Whether a tag CAN be written is a pure string question — it needs
	// neither the mailbox tree nor the members — so it is settled here, on
	// the same principle as the body shape above: a typo'd `_sent` costs no
	// round-trip. planThreadPatch re-checks so it stays self-contained for
	// its table tests; this is the refusal the user actually hits.
	if _, err := validateRequestedTags(newTags); err != nil {
		return nil, err
	}

	c, err := resolveClient(ref)
	if err != nil {
		return nil, err
	}
	tree, err := c.loadMailboxTree(ctx)
	if err != nil {
		return nil, err
	}
	members, err := c.threadMembers(ctx, ref.threadID)
	if err != nil {
		return nil, err
	}

	plan, err := planThreadPatch(
		threadView{threadID: ref.threadID, members: members}, tree, newTags,
	)
	if err != nil {
		return nil, err
	}
	if plan.empty() {
		return []string{}, nil
	}
	if _, err := c.applyThreadPatch(ctx, plan.request); err != nil {
		return nil, err
	}
	for _, created := range plan.creations {
		applied = append(applied, "mailbox:"+created.path)
	}
	return applied, nil
}

// CreateNode is unsupported: a thread is not a node a caller creates — mail
// arrives, it is not authored here — and a mailbox is created implicitly by
// naming a not-yet-existing tag in a PatchNode tag set (D4), never at a
// caller-chosen URI. Rejecting is a caller fault, not a silent no-op.
func (Plugin) CreateNode(
	_ context.Context, _ *url.URL, _ io.Reader, _ string,
) error {
	return errors.BadRequestf(
		"fastmail plugin: CreateNode is not supported — threads arrive by mail," +
			" and a mailbox is created by naming a new tag in a thread's tag set",
	)
}

// PutNode is unsupported: a thread has no wholesale-replaceable body (its
// content is its member messages), so a full replace has no meaning. Tag-set
// changes go through PatchNode.
func (Plugin) PutNode(_ context.Context, _ *url.URL, _ io.Reader) error {
	return errors.BadRequestf(
		"fastmail plugin: PutNode (full replace) is not supported — a thread is" +
			" not a replaceable document; use patch to change its tag set",
	)
}

// DeleteNode is unsupported in this slice. Deleting a thread means destroying
// every member message, which is not a tag edit and wants its own confirmation
// story; moving a thread to Trash is the soft, reversible form and is spelled
// as the `_trash` state tag on PatchNode (D2).
func (Plugin) DeleteNode(_ context.Context, _ *url.URL) error {
	return errors.BadRequestf(
		"fastmail plugin: DeleteNode is not supported — destroying messages is" +
			" out of scope; add the `_trash` tag to move a thread to Trash",
	)
}

// threadMembers fetches a thread's member messages with the full facet
// property set, in the thread's own member order. An unknown thread is a bad
// request: the caller addressed a node that does not exist.
func (c *client) threadMembers(ctx context.Context, threadID string) ([]Email, error) {
	threads, err := c.threadGet(ctx, []string{threadID})
	if err != nil {
		return nil, err
	}
	if len(threads) == 0 {
		return nil, errors.BadRequestf("fastmail plugin: no thread %q", threadID)
	}
	members, err := c.emailGet(ctx, threads[0].EmailIDs, emailFacetProps)
	if err != nil {
		return nil, err
	}
	return orderByIDs(members, threads[0].EmailIDs), nil
}

// createdMailbox pairs a planned creation id with the full name-path its
// mailbox will occupy, so PatchNode can report `mailbox:<full/path>` without
// re-reading the tree after the apply.
type createdMailbox struct {
	creationID string
	path       string
}

// threadPatchPlan is planThreadPatch's output: the ONE JMAP request the apply
// issues, plus the mailboxes it creates in creation-id order.
type threadPatchPlan struct {
	request   threadPatchRequest
	creations []createdMailbox
}

// empty reports that the plan changes nothing, so no request is issued.
func (p threadPatchPlan) empty() bool {
	return len(p.request.Creates) == 0 && len(p.request.Updates) == 0
}

// planThreadPatch is the PURE write planner: given a thread's live members,
// the live mailbox tree, and the COMPLETE tag set the caller wants, it
// produces the single JMAP request realizing the difference (fastmail tags
// slice 1, D2/D3/D4). It performs no I/O, so every rule below is unit-testable
// without a server.
//
// The difference is taken against the presented set (threadTags — label tags
// joined through the mailbox tree, plus the state tags), and each tag in it
// fans out to EVERY member message (D3):
//
//   - a label tag added → its mailbox id on all members, or, when no mailbox
//     realizes it, a `Mailbox/set` create placed by placeNewTag and referenced
//     as `mailboxIds/#<creationId>` (D4);
//   - a label tag removed → every member mailbox joining to that tag cleared
//     (which also removes BOTH of two mailboxes that legally join to one name,
//     the inverse of the union the read side presents);
//   - `_inbox` added → the inbox role mailbox on all members, and Archive
//     cleared from any member in it; removed → the inbox mailbox cleared from
//     the members that are in it;
//   - `_trash` added/removed → the trash role mailbox, same shape;
//   - `_unread` removed → `$seen` on all members, added → `$seen` cleared;
//     `_flagged` mirrors it through `$flagged`. Keyword writes are
//     unconditional across members (D3's "fans out to every member"), so a
//     thread whose members disagreed converges.
//
// Archive invariant (D3): JMAP requires a message to be in at least one
// mailbox, so a member this patch would leave with none gets the archive role
// mailbox. A member acquiring a to-be-created mailbox is not empty, so the
// invariant does not fire for it.
//
// Loud refusals, all bad requests: a reserved `_` tag (D2), a malformed tag
// (empty, or hyphen-leading — not a dodder-hyphen literal) — PatchNode has
// already settled both before fetching anything, and the check repeats here
// only so the planner stands alone for its table tests — a state tag whose
// role mailbox the account does not have, an archive-invariant member with no
// archive role mailbox to land in, and a thread with no members (nothing to
// fan out to, and creating mailboxes for it would strand them).
func planThreadPatch(
	current threadView, tree *mailboxTree, newTags []string,
) (threadPatchPlan, error) {
	var plan threadPatchPlan

	want, err := validateRequestedTags(newTags)
	if err != nil {
		return plan, err
	}
	if len(current.members) == 0 {
		return plan, errors.BadRequestf(
			"fastmail plugin: thread %q has no messages to tag", current.threadID,
		)
	}

	have := map[string]bool{}
	for _, tag := range threadTags(current.members, tree) {
		have[tag] = true
	}
	adds, removes := difference(want, have), difference(have, want)
	if len(adds) == 0 && len(removes) == 0 {
		return plan, nil
	}

	addLabels, addStates := partitionStateTags(adds)
	removeLabels, removeStates := partitionStateTags(removes)
	removedTag := map[string]bool{}
	for _, tag := range removeLabels {
		removedTag[tag] = true
	}

	roles, err := requiredRoleIDs(tree, addStates, removeStates)
	if err != nil {
		return plan, err
	}

	plan.request = threadPatchRequest{
		Creates: map[string]MailboxCreate{},
		Updates: map[string]emailPatch{},
	}
	// Resolve every added label ONCE: an existing mailbox id, or a creation
	// reference the whole request shares. addLabels is sorted, so creation ids
	// are assigned deterministically.
	addRefs := make([]string, 0, len(addLabels))
	for _, tag := range addLabels {
		if id, ok := tree.tagIndex[tag]; ok {
			addRefs = append(addRefs, id)
			continue
		}
		creationID := fmt.Sprintf("c%d", len(plan.request.Creates)+1)
		parentID, name := tree.placeNewTag(tag)
		plan.request.Creates[creationID] = MailboxCreate{Name: name, ParentID: parentID}
		plan.creations = append(plan.creations, createdMailbox{
			creationID: creationID,
			path:       tree.pathUnder(parentID, name),
		})
		addRefs = append(addRefs, "#"+creationID)
	}

	for _, member := range current.members {
		patch, err := planMemberPatch(
			member, tree, roles, addRefs, removedTag, addStates, removeStates,
		)
		if err != nil {
			return plan, err
		}
		if len(patch) > 0 {
			plan.request.Updates[member.ID] = patch
		}
	}
	return plan, nil
}

// roleIDs carries the role mailbox ids the write path resolves once per plan.
// A missing role is the empty string; requiredRoleIDs has already refused the
// cases where one is actually needed.
type roleIDs struct {
	inbox   string
	archive string
	trash   string
}

// requiredRoleIDs resolves the role mailboxes this plan needs and refuses up
// front when a state tag names a role the account does not have — better than
// discovering it per member, half-way through building the patch.
func requiredRoleIDs(
	tree *mailboxTree, addStates, removeStates map[string]bool,
) (roleIDs, error) {
	var ids roleIDs
	ids.inbox, _ = tree.roleID(roleInbox)
	ids.archive, _ = tree.roleID(roleArchive)
	ids.trash, _ = tree.roleID(roleTrash)

	for _, required := range []struct {
		tag  string
		role string
		id   string
	}{
		{stateTagInbox, roleInbox, ids.inbox},
		{stateTagTrash, roleTrash, ids.trash},
	} {
		if (addStates[required.tag] || removeStates[required.tag]) && required.id == "" {
			return ids, errors.BadRequestf(
				"fastmail plugin: cannot write %s: the account has no %s role mailbox",
				required.tag, required.role,
			)
		}
	}
	return ids, nil
}

// planMemberPatch builds ONE member message's PatchObject. It tracks the
// member's RESULTING mailbox set as it goes so the archive invariant can see
// whether the patch would strand it (a creation reference counts — the
// mailbox exists by the time the Email/set in the same request applies).
func planMemberPatch(
	member Email,
	tree *mailboxTree,
	roles roleIDs,
	addRefs []string,
	removedTag map[string]bool,
	addStates, removeStates map[string]bool,
) (emailPatch, error) {
	patch := emailPatch{}
	result := map[string]bool{}
	for id, on := range member.MailboxIDs {
		if on {
			result[id] = true
		}
	}
	pendingCreations := 0
	removedMailbox := false

	clear := func(id string) {
		patch["mailboxIds/"+id] = nil
		delete(result, id)
		removedMailbox = true
	}

	// Label removals: every mailbox of this member joining to a removed tag.
	for _, id := range sortedSetKeys(result) {
		if tag, ok := tree.tagOf(id); ok && removedTag[tag] {
			clear(id)
		}
	}
	if removeStates[stateTagInbox] && result[roles.inbox] {
		clear(roles.inbox)
	}
	if removeStates[stateTagTrash] && result[roles.trash] {
		clear(roles.trash)
	}

	for _, ref := range addRefs {
		if strings.HasPrefix(ref, "#") {
			patch["mailboxIds/"+ref] = true
			pendingCreations++
			continue
		}
		if result[ref] {
			continue
		}
		patch["mailboxIds/"+ref] = true
		result[ref] = true
	}
	if addStates[stateTagInbox] {
		if !result[roles.inbox] {
			patch["mailboxIds/"+roles.inbox] = true
			result[roles.inbox] = true
		}
		// Landing in the inbox is the inverse of archiving, so a member
		// brought back out of Archive leaves it (D3).
		if roles.archive != "" && result[roles.archive] {
			patch["mailboxIds/"+roles.archive] = nil
			delete(result, roles.archive)
		}
	}
	if addStates[stateTagTrash] && !result[roles.trash] {
		patch["mailboxIds/"+roles.trash] = true
		result[roles.trash] = true
	}

	if removeStates[stateTagUnread] {
		patch["keywords/"+keywordSeen] = true
	}
	if addStates[stateTagUnread] {
		patch["keywords/"+keywordSeen] = nil
	}
	if addStates[stateTagFlagged] {
		patch["keywords/"+keywordFlagged] = true
	}
	if removeStates[stateTagFlagged] {
		patch["keywords/"+keywordFlagged] = nil
	}

	// The archive invariant: JMAP refuses an update leaving a message in no
	// mailbox, so a member this patch emptied lands in Archive instead.
	if removedMailbox && len(result) == 0 && pendingCreations == 0 {
		if roles.archive == "" {
			return nil, errors.BadRequestf(
				"fastmail plugin: removing that tag would leave message %q in no"+
					" mailbox, and the account has no %s role mailbox to file it in",
				member.ID, roleArchive,
			)
		}
		patch["mailboxIds/"+roles.archive] = true
	}
	return patch, nil
}

// validateRequestedTags turns the requested tag list into a set, refusing the
// tags that can never be written: a reserved `_` tag (D2 — `_sent`, `_junk`,
// `_archive`, anything else in the namespace), the empty tag, and a
// hyphen-leading one (no dodder-hyphen literal starts a segment empty, and no
// mailbox chain could ever join back to it).
func validateRequestedTags(tags []string) (map[string]bool, error) {
	want := make(map[string]bool, len(tags))
	for _, tag := range tags {
		switch {
		case tag == "":
			return nil, errors.BadRequestf("fastmail plugin: the empty tag cannot be written")
		case isReservedTag(tag):
			return nil, errors.BadRequestf(
				"fastmail plugin: %q is reserved and cannot be written;"+
					" the writable state tags are %s, %s, %s and %s",
				tag, stateTagFlagged, stateTagInbox, stateTagTrash, stateTagUnread,
			)
		case isContinuationSegment(tag):
			return nil, errors.BadRequestf(
				"fastmail plugin: tag %q cannot be written: a tag may not start with %q",
				tag, "-",
			)
		}
		want[tag] = true
	}
	return want, nil
}

// partitionStateTags splits a sorted tag list into its label tags (order
// preserved) and the set of state tags it names.
func partitionStateTags(tags []string) (labels []string, states map[string]bool) {
	states = map[string]bool{}
	for _, tag := range tags {
		if isStateTag(tag) {
			states[tag] = true
			continue
		}
		labels = append(labels, tag)
	}
	return labels, states
}

// difference returns the sorted members of a that b does not hold.
func difference(a, b map[string]bool) []string {
	var out []string
	for key := range a {
		if !b[key] {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// sortedSetKeys returns a set's keys sorted, so iteration over a member's
// mailbox ids is deterministic.
func sortedSetKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
