package cutting_garden_plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// ApplyBulkOp applies one op via nm's matching NodeMutator verb and records
// the outcome into result: AppliedNodes on success, PatchedNothing when a
// patch was ACCEPTED but landed no recognized field (#182), Failed
// otherwise. It never returns an error — a per-op failure is a BulkFailure,
// the best-effort contract (RFC 0017). This is the single dispatch loop
// caldav, the testpeer, and jira share (cutting-garden#197): its per-node
// result-collection — the #182 patchedNothing-vs-applied distinction most of
// all — lives here so a subtle bug cannot be fixed inconsistently across
// plugins.
//
// Temp ids (cutting-garden#315) resolve against result.Created, which is why
// ops MUST run in request order: a TempID create goes through nm's
// ContainerCreator.CreateChild and records the URI it chose, and a later
// op's Ref / InRef reads it back. An op whose temp id did not resolve —
// its create failed — fails without running.
func ApplyBulkOp(
	ctx context.Context, nm NodeMutator, op BulkOp, result *BulkResult,
) {
	if op.TempID != "" {
		applyTempCreate(ctx, nm, op, result)
		return
	}

	if op.Ref != "" {
		target, ok := result.createdURI(op.Ref)
		if !ok {
			result.Failed = append(result.Failed, BulkFailure{
				Ref: op.Ref,
				Err: dependentFailure(op.Ref),
			})
			return
		}
		op.URI = target
	}

	var (
		patchedNothing bool
		err            error
	)

	switch op.Kind {
	case BulkCreate:
		err = nm.CreateNode(ctx, op.URI, bytes.NewReader(op.Body), op.Type)
	case BulkPut:
		err = nm.PutNode(ctx, op.URI, bytes.NewReader(op.Body))
	case BulkPatch:
		var applied []string
		applied, err = nm.PatchNode(ctx, op.URI, bytes.NewReader(op.Body))
		// A nil applied means the plugin does not report fields, not that
		// none landed; only an authoritative empty one is patchedNothing.
		patchedNothing = err == nil && applied != nil && len(applied) == 0
	case BulkDelete:
		err = nm.DeleteNode(ctx, op.URI)
	default:
		err = errors.BadRequestf("bulk_mutate: unknown op kind %q", op.Kind)
	}

	switch {
	case err != nil:
		result.Failed = append(
			result.Failed,
			BulkFailure{URI: op.URI, Ref: op.Ref, Err: err.Error()},
		)
	case patchedNothing:
		result.PatchedNothing = append(result.PatchedNothing, op.URI)
	default:
		result.AppliedNodes = append(result.AppliedNodes, op.URI)
	}
}

// applyTempCreate runs a server-assigned create: the container is In, or the
// node an earlier create made (InRef), and the source names the child.
func applyTempCreate(
	ctx context.Context, nm NodeMutator, op BulkOp, result *BulkResult,
) {
	fail := func(msg string) {
		result.Failed = append(
			result.Failed, BulkFailure{Ref: op.TempID, Err: msg},
		)
	}

	creator, ok := nm.(ContainerCreator)
	if !ok {
		fail("this source does not create server-assigned children")
		return
	}

	container := op.In
	if op.InRef != "" {
		if container, ok = result.createdURI(op.InRef); !ok {
			fail(dependentFailure(op.InRef))
			return
		}
	}

	created, err := creator.CreateChild(
		ctx, container, bytes.NewReader(op.Body), op.Type,
	)
	switch {
	case err != nil:
		fail(err.Error())
	case created == nil:
		fail("the source reported no created URI")
	default:
		result.Created = append(
			result.Created, BulkCreated{TempID: op.TempID, URI: created},
		)
		result.AppliedNodes = append(result.AppliedNodes, created)
	}
}

// createdURI looks up the node a TempID create in this request made.
func (r *BulkResult) createdURI(tempID string) (*url.URL, bool) {
	for _, c := range r.Created {
		if c.TempID == tempID {
			return c.URI, true
		}
	}
	return nil, false
}

func dependentFailure(tempID string) string {
	return "depends on the create of temp id " + tempID + ", which failed"
}

// BulkBestEffortOps applies an explicit changeset best-effort via nm,
// returning the partitioned result. It never errors: each op's failure is a
// BulkFailure entry, never a returned error.
func BulkBestEffortOps(
	ctx context.Context, nm NodeMutator, ops []BulkOp,
) BulkResult {
	var result BulkResult
	for _, op := range ops {
		ApplyBulkOp(ctx, nm, op, &result)
	}
	return result
}

// BulkBestEffortSweep resolves a sweep's match set via el.ListEnriched and
// applies the op template to each match best-effort (the op template's URI
// is ignored; each matched node's URI is filled in). On an el DECLINE
// (ok=false) it REFUSES with a bad request rather than proceeding: a
// mutation must never silently widen its scope from "matching" to "every
// child" — the write-safety contract caldav established (cutting-garden#191).
// A read may degrade a decline to an unfiltered listing; a write may not.
func BulkBestEffortSweep(
	ctx context.Context, el EnrichedLister, nm NodeMutator, sweep *BulkSweep,
) (BulkResult, error) {
	matches, ok, err := el.ListEnriched(ctx, sweep.Root, sweep.Filter)
	if err != nil {
		return BulkResult{}, err
	}
	if !ok {
		return BulkResult{}, errors.BadRequestf(
			"bulk_mutate: cannot sweep %q: the enriched listing declined at"+
				" this level (its children are containers, not the nodes a"+
				" filter selects) — sweep each child container instead",
			sweep.Root,
		)
	}

	var result BulkResult
	for _, match := range matches {
		op := sweep.Op
		op.URI = match.URI
		ApplyBulkOp(ctx, nm, op, &result)
	}
	return result, nil
}

// BestEffortBulkMutate is the whole best-effort BulkMutate for a plugin that
// is both a NodeMutator and an EnrichedLister — the common shape every
// current implementer takes (caldav, the testpeer, jira, newsblur are all
// best-effort-only). It REJECTS an atomic request with
// ErrBulkAtomicUnsupported (the reject-never-downgrade floor for a plugin
// that does not advertise bulk-atomic), and otherwise dispatches the
// changeset or the sweep best-effort. A plugin's whole BulkMutate collapses
// to one delegating line: `return BestEffortBulkMutate(ctx, p, p, req)`.
//
// A plugin that CAN transact atomically implements AtomicBulkMutator and its
// own atomic path instead, reusing BulkBestEffortOps/Sweep for its
// best-effort branch. A plugin without an EnrichedLister cannot use this
// (a sweep has no way to resolve its match set) and dispatches the pieces
// itself.
func BestEffortBulkMutate(
	ctx context.Context, nm NodeMutator, el EnrichedLister, req BulkRequest,
) (BulkResult, error) {
	if req.Atomicity == BulkAtomic {
		return BulkResult{}, ErrBulkAtomicUnsupported
	}
	if req.Sweep != nil {
		return BulkBestEffortSweep(ctx, el, nm, req.Sweep)
	}
	return BulkBestEffortOps(ctx, nm, req.Ops), nil
}

// RecognizedPatchFields returns the subset of supported keys present in
// fields, preserving supported's order — callers pass a SORTED supported
// list so the result, and the PatchNode applied report built from it, is
// deterministic. Always non-nil, so an empty result is the authoritative
// "nothing recognized" the #182 present-empty applied contract requires,
// never a nil "did not report". The shared single-node #182 helper caldav
// and jira use (cutting-garden#197).
func RecognizedPatchFields(
	fields map[string]json.RawMessage, supported []string,
) []string {
	recognized := make([]string, 0, min(len(fields), len(supported)))
	for _, key := range supported {
		if _, ok := fields[key]; ok {
			recognized = append(recognized, key)
		}
	}
	return recognized
}
