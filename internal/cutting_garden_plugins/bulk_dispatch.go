package cutting_garden_plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"slices"

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
			msg := dependentFailure(op.Ref)
			result.Failed = append(result.Failed, BulkFailure{Ref: op.Ref, Err: msg})
			result.Outcomes = append(result.Outcomes, BulkOutcome{
				Ref: op.Ref, Status: OutcomeFailed, Err: msg,
			})
			return
		}
		op.URI = target
	}

	var (
		patchedNothing bool
		notFound       bool
		err            error
	)
	outcome := BulkOutcome{URI: op.URI, Ref: op.Ref}

	switch op.Kind {
	case BulkCreate:
		err = nm.CreateNode(ctx, op.URI, bytes.NewReader(op.Body), op.Type)
	case BulkPut:
		err = nm.PutNode(ctx, op.URI, bytes.NewReader(op.Body))
	case BulkPatch:
		var detail PatchDetail
		detail, err = applyPatch(ctx, nm, op)
		// A nil applied means the plugin does not report fields, not that
		// none landed; only an authoritative empty one is patchedNothing.
		patchedNothing = err == nil && detail.Applied != nil && len(detail.Applied) == 0
		outcome.Fields = detail.Applied
		outcome.Ignored = detail.Ignored
		if err == nil && !patchedNothing && detail.NewURI != nil &&
			detail.NewURI.String() != op.URI.String() {
			outcome.NewURI = detail.NewURI
		}
	case BulkDelete:
		var detail DeleteDetail
		detail, err = applyDelete(ctx, nm, op)
		outcome.Name, notFound = detail.Name, detail.NotFound
	default:
		err = errors.BadRequestf("bulk_mutate: unknown op kind %q", op.Kind)
	}

	switch {
	case err != nil:
		result.Failed = append(
			result.Failed,
			BulkFailure{URI: op.URI, Ref: op.Ref, Err: err.Error()},
		)
		outcome.Status, outcome.Err = OutcomeFailed, err.Error()
		if notFound {
			outcome.Status = OutcomeNotFound
		}
	case patchedNothing:
		result.PatchedNothing = append(result.PatchedNothing, op.URI)
		outcome.Status = OutcomePatchedNothing
	default:
		result.AppliedNodes = append(result.AppliedNodes, op.URI)
		outcome.Status = OutcomeApplied
		if op.Kind == BulkDelete {
			outcome.Status = OutcomeDeleted
		}
	}
	result.Outcomes = append(result.Outcomes, outcome)
}

// PatchDetail is what a patch reports beyond success: the field keys it
// applied (nil when it does not report them, like PatchNode's applied), the
// body keys it tolerated but did not apply (cutting-garden#376; nil when none
// or not reported), and the node's URI afterwards when it changed.
type PatchDetail struct {
	Applied []string
	Ignored []string
	NewURI  *url.URL
}

// NodePatcherDetail is the optional patch that also reports the node's URI
// afterwards. A source implements it beside NodeMutator.PatchNode (which stays
// the same call); it is asserted structurally, so a plugin implements the
// method without importing this type.
type NodePatcherDetail interface {
	PatchNodeDetail(ctx context.Context, uri *url.URL, body io.Reader) (PatchDetail, error)
}

// DeleteDetail is what a delete reports beyond success: the removed node's
// name when the source knows it. NotFound is true, with a non-nil error, when
// there was no such node to delete.
type DeleteDetail struct {
	Name     string
	NotFound bool
}

// NodeDeleterDetail is the optional delete that reports the removed node's
// name and tells "no such node" from another failure. It takes the op's body
// like NodeDeleterWith (empty when the op had none) and is asserted
// structurally.
type NodeDeleterDetail interface {
	DeleteNodeDetail(ctx context.Context, uri *url.URL, body io.Reader) (DeleteDetail, error)
}

// applyPatch runs a patch op through NodePatcherDetail when the source has
// it, else NodeMutator.PatchNode with no detail beyond the applied fields.
func applyPatch(ctx context.Context, nm NodeMutator, op BulkOp) (PatchDetail, error) {
	if p, ok := nm.(NodePatcherDetail); ok {
		return p.PatchNodeDetail(ctx, op.URI, bytes.NewReader(op.Body))
	}
	applied, err := nm.PatchNode(ctx, op.URI, bytes.NewReader(op.Body))
	return PatchDetail{Applied: applied}, err
}

// NodeDeleterWith is the optional delete that takes options: a source whose
// delete has a knob (org's {"subtree": true}) implements it beside
// NodeMutator.DeleteNode, and says in its describe_node_types text what the
// body means. It is asserted structurally: a plugin implements the method
// without importing this type.
type NodeDeleterWith interface {
	DeleteNodeWith(ctx context.Context, uri *url.URL, body io.Reader) error
}

// applyDelete runs a delete op. A source with NodeDeleterDetail gets the op
// whole. Otherwise, with no body it is NodeMutator.DeleteNode, as always; a
// body goes to NodeDeleterWith, and a source without one refuses it rather
// than dropping the options a caller sent (a delete with a body used to run
// without it).
func applyDelete(ctx context.Context, nm NodeMutator, op BulkOp) (DeleteDetail, error) {
	if d, ok := nm.(NodeDeleterDetail); ok {
		return d.DeleteNodeDetail(ctx, op.URI, bytes.NewReader(op.Body))
	}
	if len(op.Body) == 0 {
		return DeleteDetail{}, nm.DeleteNode(ctx, op.URI)
	}
	d, ok := nm.(NodeDeleterWith)
	if !ok {
		return DeleteDetail{}, errors.BadRequestf("this source's delete takes no body")
	}
	return DeleteDetail{}, d.DeleteNodeWith(ctx, op.URI, bytes.NewReader(op.Body))
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
		result.Outcomes = append(result.Outcomes, BulkOutcome{
			Ref: op.TempID, Status: OutcomeFailed, Err: msg,
		})
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
		result.Outcomes = append(result.Outcomes, BulkOutcome{
			URI: created, Ref: op.TempID, Status: OutcomeApplied,
		})
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
		applyBulkOpInOrder(ctx, nm, op, &result)
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
		applyBulkOpInOrder(ctx, nm, op, &result)
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

// IgnoredPatchFields returns the keys of fields outside supported, sorted:
// the fields a patch tolerates but does not apply, which a plugin reports as
// PatchDetail.Ignored (cutting-garden#376). nil when there are none.
func IgnoredPatchFields(
	fields map[string]json.RawMessage, supported []string,
) []string {
	var ignored []string
	for key := range fields {
		if !slices.Contains(supported, key) {
			ignored = append(ignored, key)
		}
	}
	slices.Sort(ignored)
	return ignored
}
