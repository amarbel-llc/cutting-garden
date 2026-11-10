package fastmail

import (
	"context"
	"net/url"
	"sort"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

var _ cutting_garden_plugins.BulkMutator = (*Plugin)(nil)

// BulkMutate is fastmail's best-effort BulkMutator (RFC 0017,
// cutting-garden#292). N thread patches, moves and tag-set writes alike, cost
// one request per account for the writes: every op's Mailbox/set creates (one
// per new tag, however many ops name it) and ONE Email/set carrying every
// op's member updates. The reads before it are shared too: the mailbox tree,
// one Thread/get and one Email/get for every targeted thread.
//
// Each op is planned by the single-patch planners (planThreadMove,
// planThreadPatch), so it keeps PatchNode's refusals, and it gets its own
// outcome:
//   - applied, with fields ["mailbox"] and the new URI for a move, or
//     ["tags"] plus "mailbox:<path>" for each mailbox its tags create;
//   - patched_nothing for a body naming no recognized key or asking for what
//     the thread already is;
//   - failed when its plan is refused, when a create it needs is in
//     notCreated, or when one of its messages is in notUpdated: the error
//     names the record and the server's SetError, and says which of the
//     thread's other messages were updated anyway (JMAP applies records one by
//     one, RFC 8620 §5.3, so a thread can land partly).
//
// The Email/set carries ifInState, the Email state the batch was read at. If
// the account's mail changed in between, the server refuses the whole call
// and every batched op fails with a "re-plan and retry" error. The creates in
// the same request ran first and are not rolled back, so a retry finds those
// tags existing and creates nothing new.
//
// Two ops on one thread merge when they ask for the same write; otherwise the
// later one fails, since one Email/set has no order between two patches of
// one message. An op that is not a thread patch (another verb, another node
// kind) takes the single-node path, which reports its own refusal. Outcomes
// are in request order across both paths.
//
// Atomic requests are refused (ErrBulkAtomicUnsupported): a JMAP /set applies
// per record and a refused update rolls back nothing, so the batch cannot be
// all-or-nothing.
func (p Plugin) BulkMutate(
	ctx context.Context, req cutting_garden_plugins.BulkRequest,
) (cutting_garden_plugins.BulkResult, error) {
	if err := req.Validate(); err != nil {
		return cutting_garden_plugins.BulkResult{}, err
	}
	if req.Atomicity == cutting_garden_plugins.BulkAtomic {
		return cutting_garden_plugins.BulkResult{}, cutting_garden_plugins.ErrBulkAtomicUnsupported
	}
	if req.Sweep != nil {
		return p.bulkSweep(ctx, req.Sweep)
	}
	return p.bulkOps(ctx, req.Ops), nil
}

// bulkSweep resolves a sweep's threads with ListEnriched and batches the op
// template over them; the child mailboxes the listing keeps as navigation are
// not matches. It REFUSES when the listing declines at this level rather than
// widening "matching" to "every child" (cutting-garden#191).
func (p Plugin) bulkSweep(
	ctx context.Context, sweep *cutting_garden_plugins.BulkSweep,
) (cutting_garden_plugins.BulkResult, error) {
	nodes, ok, err := p.ListEnriched(ctx, sweep.Root, sweep.Filter)
	if err != nil {
		return cutting_garden_plugins.BulkResult{}, err
	}
	if !ok {
		return cutting_garden_plugins.BulkResult{}, errors.BadRequestf(
			"bulk_mutate: cannot sweep %q: the enriched listing declined at this level "+
				"(it is not a mailbox) — sweep a mailbox", sweep.Root,
		)
	}
	var ops []cutting_garden_plugins.BulkOp
	for _, node := range nodes {
		if node.Type != typeThread {
			continue
		}
		op := sweep.Op
		op.URI = node.URI
		ops = append(ops, op)
	}
	if len(ops) == 0 {
		return cutting_garden_plugins.BulkResult{}, nil
	}
	return p.bulkOps(ctx, ops), nil
}

// bulkMember is one thread patch queued for its account's batch. merged
// holds the later ops on the same thread that asked for the same write; they
// share its result.
type bulkMember struct {
	index  int
	op     cutting_garden_plugins.BulkOp
	ref    nodeRef
	patch  threadPatch
	merged []bulkMember
}

// bulkGroup is the thread patches of one account.
type bulkGroup struct {
	ref     nodeRef // any member's ref: it names the account
	members []bulkMember
}

func (p Plugin) bulkOps(
	ctx context.Context, ops []cutting_garden_plugins.BulkOp,
) cutting_garden_plugins.BulkResult {
	r := newBulkRecorder(len(ops))
	groups := map[string]*bulkGroup{}
	var order []string

	for i, op := range ops {
		if op.Kind != cutting_garden_plugins.BulkPatch || op.URI == nil {
			r.single(ctx, p, i, op)
			continue
		}
		ref, err := classifyURI(op.URI)
		if err != nil {
			r.fail(bulkMember{index: i, op: op}, err)
			continue
		}
		if ref.kind != kindThread {
			r.single(ctx, p, i, op)
			continue
		}
		patch, err := parseThreadPatch(op.Body)
		member := bulkMember{index: i, op: op, ref: ref, patch: patch}
		switch {
		case err != nil:
			r.fail(member, err)
			continue
		case patch.empty():
			r.nothing(member)
			continue
		}
		group, ok := groups[ref.account]
		if !ok {
			group = &bulkGroup{ref: ref}
			groups[ref.account] = group
			order = append(order, ref.account)
		}
		group.members = append(group.members, member)
	}

	for _, account := range order {
		applyBulkGroup(ctx, groups[account], r)
	}
	return r.finish()
}

// plannedMember is one op's share of the batch: the member updates it put in
// the Email/set, the creates it needs, and what it reports when it lands.
type plannedMember struct {
	member    bulkMember
	emailIDs  []string
	creations []createdMailbox
	fields    []string
	newURI    *url.URL
}

// applyBulkGroup reads what one account's batch needs, plans every op, sends
// the ONE request, and attributes its answer to each op.
func applyBulkGroup(ctx context.Context, group *bulkGroup, r *bulkRecorder) {
	members := mergeThreadOps(group.members, r.fail)
	failEach := func(members []bulkMember, err error) {
		for _, m := range members {
			r.fail(m, err)
		}
	}

	c, err := resolveClient(group.ref)
	if err != nil {
		failEach(members, err)
		return
	}
	tree, err := c.loadMailboxTree(ctx)
	if err != nil {
		failEach(members, err)
		return
	}
	threadIDs := make([]string, 0, len(members))
	for _, m := range members {
		threadIDs = append(threadIDs, m.ref.threadID)
	}
	byThread, state, err := c.threadMembersByThread(ctx, threadIDs)
	if err != nil {
		failEach(members, err)
		return
	}

	creations := newMailboxCreations()
	updates := map[string]emailPatch{}
	var planned []plannedMember
	for _, m := range members {
		emails, ok := byThread[m.ref.threadID]
		if !ok {
			r.fail(m, errors.BadRequestf("fastmail plugin: no thread %q", m.ref.threadID))
			continue
		}
		mark := creations.mark()
		pm, memberUpdates, err := planBulkMember(m, emails, tree, creations)
		if err != nil {
			r.fail(m, err)
			continue
		}
		if len(memberUpdates) == 0 {
			// Believed unreachable: a plan that allocates a create also adds it
			// to every member, and an empty thread is refused. Should one ever
			// plan creates with no updates, drop them, so the shared request
			// never creates a mailbox no op uses.
			creations.rollback(mark)
			r.nothing(m)
			continue
		}
		for id, patch := range memberUpdates {
			updates[id] = patch
			pm.emailIDs = append(pm.emailIDs, id)
		}
		sort.Strings(pm.emailIDs)
		planned = append(planned, pm)
	}
	if len(planned) == 0 {
		return
	}
	failPlanned := func(err error) {
		for _, pm := range planned {
			r.fail(pm.member, err)
		}
	}

	sent, err := c.sendThreadPatch(
		ctx, threadPatchRequest{Creates: creations.creates, Updates: updates}, state,
	)
	if err != nil {
		failPlanned(err)
		return
	}
	if sent.updateError != nil {
		failPlanned(batchRefusal(*sent.updateError, creations, sent.createdIDs))
		return
	}
	for _, pm := range planned {
		if err := memberRefusal(pm, sent); err != nil {
			r.fail(pm.member, err)
			continue
		}
		r.applied(pm.member, pm.fields, pm.newURI)
	}
}

// planBulkMember plans one op against the batch's shared reads: a move by
// planThreadMove, a tag-set write by planThreadPatchWith drawing its creates
// from the batch's shared table.
func planBulkMember(
	m bulkMember, emails []Email, tree *mailboxTree, creations *mailboxCreations,
) (plannedMember, map[string]emailPatch, error) {
	pm := plannedMember{member: m}
	if m.patch.moveTo != "" {
		source, dest, err := resolveMove(tree, m.ref, m.patch.moveTo)
		if err != nil {
			return pm, nil, err
		}
		pm.fields = []string{patchFieldMailbox}
		pm.newURI = threadURI(m.ref.account, tree.path(dest.ID), m.ref.threadID)
		return pm, planThreadMove(emails, source.ID, dest.ID), nil
	}
	plan, err := planThreadPatchWith(
		threadView{threadID: m.ref.threadID, members: emails}, tree, m.patch.tags, creations,
	)
	if err != nil {
		return pm, nil, err
	}
	pm.creations = plan.creations
	pm.fields = []string{listingFieldTags}
	for _, created := range plan.creations {
		pm.fields = append(pm.fields, "mailbox:"+created.path)
	}
	return pm, plan.request.Updates, nil
}

// memberRefusal is why one op did not land, or nil: a create it needs that
// the server refused, else any of its messages the server refused.
func memberRefusal(pm plannedMember, sent sentThreadPatch) error {
	refusedCreates := map[string]setError{}
	for _, created := range pm.creations {
		if refusal, ok := sent.notCreated[created.creationID]; ok {
			refusedCreates[created.creationID+" ("+created.path+")"] = refusal
		}
	}
	if err := refusedRecords("Mailbox/set", refusedCreates); err != nil {
		return err
	}

	refusedUpdates := map[string]setError{}
	var landed []string
	for _, id := range pm.emailIDs {
		if refusal, ok := sent.notUpdated[id]; ok {
			refusedUpdates[id] = refusal
			continue
		}
		landed = append(landed, id)
	}
	err := refusedRecords("Email/set", refusedUpdates)
	if err == nil || len(landed) == 0 {
		return err
	}
	return errors.BadRequestf(
		"%s; this thread's other messages were updated: %s", err, strings.Join(landed, ", "),
	)
}

// batchRefusal is the error every batched op gets when the server refused
// the whole Email/set. A stateMismatch means the mail changed after it was
// read; nothing in the Email/set was written, so the caller re-plans and
// retries. Mailboxes the same request created stay, and are named.
func batchRefusal(
	refusal methodError, creations *mailboxCreations, createdIDs map[string]string,
) error {
	var stay []string
	for _, tag := range creations.order {
		created := creations.byTag[tag]
		if _, ok := createdIDs[created.creationID]; ok {
			stay = append(stay, created.path)
		}
	}
	created := ""
	if len(stay) > 0 {
		created = "; the mailboxes it created stay: " + strings.Join(stay, ", ")
	}
	if refusal.Type == "stateMismatch" {
		return errors.BadRequestf(
			"fastmail plugin: Email/set refused the batch (stateMismatch): the account's "+
				"mail changed after it was read, so no message was changed; re-plan and retry%s",
			created,
		)
	}
	return errors.ErrorWithStackf(
		"fastmail plugin: Email/set: server error: %s%s", refusal.raw, created,
	)
}

// mergeThreadOps folds ops on one thread into the first of them when they ask
// for the same write from the same mailbox; a later op asking for something
// else is reported through fail, leaving the thread with the first write. The
// result keeps the order of each thread's first appearance.
func mergeThreadOps(
	members []bulkMember, fail func(bulkMember, error),
) []bulkMember {
	var merged []bulkMember
	index := map[string]int{}
	for _, m := range members {
		i, seen := index[m.ref.threadID]
		if !seen {
			index[m.ref.threadID] = len(merged)
			merged = append(merged, m)
			continue
		}
		first := merged[i]
		if pathString(first.ref.mailboxPath) != pathString(m.ref.mailboxPath) ||
			!first.patch.sameWrite(m.patch) {
			fail(m, errors.BadRequestf(
				"fastmail plugin: thread %q is patched twice in one bulk request with "+
					"different writes; one Email/set has no order between two patches of a "+
					"message, so send one patch", m.ref.threadID,
			))
			continue
		}
		merged[i].merged = append(merged[i].merged, m)
	}
	return merged
}

// threadMembersByThread fetches the members of every named thread, in each
// thread's own order, with one Thread/get and one Email/get, and returns the
// Email state they were read at. A thread the server does not know is absent
// from the map.
func (c *client) threadMembersByThread(
	ctx context.Context, threadIDs []string,
) (map[string][]Email, string, error) {
	threads, err := c.threadGet(ctx, threadIDs)
	if err != nil {
		return nil, "", err
	}
	var emailIDs []string
	for _, thread := range threads {
		emailIDs = append(emailIDs, thread.EmailIDs...)
	}
	emails, state, err := c.emailGetWithState(ctx, emailIDs, emailFacetProps)
	if err != nil {
		return nil, "", err
	}
	out := make(map[string][]Email, len(threads))
	for _, thread := range threads {
		out[thread.ID] = orderByIDs(emails, thread.EmailIDs)
	}
	return out, state, nil
}

// bulkRecorder collects a bulk call's result with each outcome in its op's
// request slot, so outcomes stay in request order however the ops were
// batched.
type bulkRecorder struct {
	result   cutting_garden_plugins.BulkResult
	outcomes []cutting_garden_plugins.BulkOutcome
}

func newBulkRecorder(n int) *bulkRecorder {
	return &bulkRecorder{outcomes: make([]cutting_garden_plugins.BulkOutcome, n)}
}

// eachOp calls record for m and every op merged into it.
func eachOp(m bulkMember, record func(bulkMember)) {
	record(m)
	for _, other := range m.merged {
		record(other)
	}
}

func (r *bulkRecorder) outcome(
	m bulkMember, status cutting_garden_plugins.BulkOutcomeStatus,
) *cutting_garden_plugins.BulkOutcome {
	r.outcomes[m.index] = cutting_garden_plugins.BulkOutcome{
		URI: m.op.URI, Ref: m.op.Ref, Status: status, Ignored: m.patch.ignored,
	}
	return &r.outcomes[m.index]
}

func (r *bulkRecorder) applied(m bulkMember, fields []string, newURI *url.URL) {
	eachOp(m, func(op bulkMember) {
		r.result.AppliedNodes = append(r.result.AppliedNodes, op.op.URI)
		outcome := r.outcome(op, cutting_garden_plugins.OutcomeApplied)
		outcome.Fields = fields
		if newURI != nil && newURI.String() != op.op.URI.String() {
			outcome.NewURI = newURI
		}
	})
}

func (r *bulkRecorder) nothing(m bulkMember) {
	eachOp(m, func(op bulkMember) {
		r.result.PatchedNothing = append(r.result.PatchedNothing, op.op.URI)
		r.outcome(op, cutting_garden_plugins.OutcomePatchedNothing).Fields = []string{}
	})
}

func (r *bulkRecorder) fail(m bulkMember, err error) {
	eachOp(m, func(op bulkMember) {
		r.result.Failed = append(r.result.Failed, cutting_garden_plugins.BulkFailure{
			URI: op.op.URI, Ref: op.op.Ref, Err: err.Error(),
		})
		r.outcome(op, cutting_garden_plugins.OutcomeFailed).Err = err.Error()
	})
}

// single runs an op that is not a thread patch through the SDK's single-node
// dispatch and files its result in the op's slot.
func (r *bulkRecorder) single(
	ctx context.Context, p Plugin, i int, op cutting_garden_plugins.BulkOp,
) {
	var one cutting_garden_plugins.BulkResult
	cutting_garden_plugins.ApplyBulkOp(ctx, p, op, &one)
	r.result.AppliedNodes = append(r.result.AppliedNodes, one.AppliedNodes...)
	r.result.PatchedNothing = append(r.result.PatchedNothing, one.PatchedNothing...)
	r.result.Failed = append(r.result.Failed, one.Failed...)
	r.result.Created = append(r.result.Created, one.Created...)
	if len(one.Outcomes) > 0 {
		r.outcomes[i] = one.Outcomes[len(one.Outcomes)-1]
	}
}

func (r *bulkRecorder) finish() cutting_garden_plugins.BulkResult {
	r.result.Outcomes = r.outcomes
	return r.result
}
