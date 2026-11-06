package cutting_garden_plugins

import "context"

// BulkOutcomeObserver is told each op's outcome as the best-effort loop
// records it: i is the outcome's index in BulkResult.Outcomes. A host that
// can lose its reply (an MCP server whose client hangs up) logs these, so
// what applied stays recoverable (cutting-garden#392).
type BulkOutcomeObserver func(i int, outcome BulkOutcome)

type bulkObserverKey struct{}

// WithBulkOutcomeObserver returns ctx carrying observe for the best-effort
// loops (BulkBestEffortOps, BulkBestEffortSweep) to call.
func WithBulkOutcomeObserver(ctx context.Context, observe BulkOutcomeObserver) context.Context {
	return context.WithValue(ctx, bulkObserverKey{}, observe)
}

// notAttemptedReason is the failure an op gets when the call was cancelled
// before it started.
const notAttemptedReason = "not attempted: the call was cancelled before this op started; send it again"

// applyBulkOpInOrder runs one op of a best-effort loop: refused without
// running once ctx is cancelled (so a batch cut off partway reports exactly
// which ops never started, rather than half-starting one), then reported to
// ctx's observer.
func applyBulkOpInOrder(ctx context.Context, nm NodeMutator, op BulkOp, result *BulkResult) {
	before := len(result.Outcomes)

	if ctx.Err() != nil {
		ref := op.Ref
		if op.TempID != "" {
			ref = op.TempID
		}
		result.Failed = append(result.Failed, BulkFailure{URI: op.URI, Ref: ref, Err: notAttemptedReason})
		result.Outcomes = append(result.Outcomes, BulkOutcome{
			URI: op.URI, Ref: ref, Status: OutcomeFailed, Err: notAttemptedReason,
		})
	} else {
		ApplyBulkOp(ctx, nm, op, result)
	}

	observe, _ := ctx.Value(bulkObserverKey{}).(BulkOutcomeObserver)
	if observe == nil {
		return
	}
	for i := before; i < len(result.Outcomes); i++ {
		observe(i, result.Outcomes[i])
	}
}
