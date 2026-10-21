package cutting_garden_plugins

import (
	"context"
	"sync"
	"testing"
)

// Outside a call scope nothing is confirmed, whatever was recorded: a plugin
// re-validates every time, the safe default.
func TestCallScope_OutsideAScopeNothingIsConfirmed(t *testing.T) {
	ctx := context.Background()
	if InCallScope(ctx) {
		t.Fatal("a bare context is not in a call scope")
	}
	ConfirmForThisCall(ctx, "k") // must not panic, must not stick
	if ConfirmedThisCall(ctx, "k") {
		t.Error("a confirmation outside a scope must not be remembered")
	}
	UnconfirmForThisCall(ctx, "k") // a no-op
}

func TestCallScope_ConfirmationLastsForTheCall(t *testing.T) {
	ctx := WithCallScope(context.Background())
	if !InCallScope(ctx) {
		t.Fatal("WithCallScope did not mark the context")
	}
	if ConfirmedThisCall(ctx, "org:a:f.org") {
		t.Error("nothing is confirmed yet")
	}
	ConfirmForThisCall(ctx, "org:a:f.org")
	if !ConfirmedThisCall(ctx, "org:a:f.org") {
		t.Error("the confirmation was not remembered within the call")
	}
	if ConfirmedThisCall(ctx, "org:a:g.org") {
		t.Error("a different key must not be confirmed")
	}
}

// A confirmation does not outlive its call: a new call starts empty, which is
// the whole point against a time window.
func TestCallScope_ASecondCallStartsEmpty(t *testing.T) {
	first := WithCallScope(context.Background())
	ConfirmForThisCall(first, "k")
	second := WithCallScope(context.Background())
	if ConfirmedThisCall(second, "k") {
		t.Error("a new call inherited the previous call's confirmation")
	}
}

// A plugin's own write withdraws the confirmation the write invalidated.
func TestCallScope_UnconfirmWithdraws(t *testing.T) {
	ctx := WithCallScope(context.Background())
	ConfirmForThisCall(ctx, "k")
	UnconfirmForThisCall(ctx, "k")
	if ConfirmedThisCall(ctx, "k") {
		t.Error("the confirmation survived Unconfirm")
	}
}

// Wrapping an already-scoped context keeps the outer scope: a nested host layer
// (a tool that reads a resource) must not shorten the outer call, or its
// confirmations would be lost between the layers.
func TestCallScope_WrappingIsIdempotent(t *testing.T) {
	outer := WithCallScope(context.Background())
	ConfirmForThisCall(outer, "k")
	inner := WithCallScope(outer)
	if !ConfirmedThisCall(inner, "k") {
		t.Error("the nested wrap dropped the outer call's confirmation")
	}
	ConfirmForThisCall(inner, "j")
	if !ConfirmedThisCall(outer, "j") {
		t.Error("a confirmation made in the nested layer is not visible to the outer call")
	}
}

// Concurrent plugins within one call share the scope safely.
func TestCallScope_IsSafeForConcurrentUse(t *testing.T) {
	ctx := WithCallScope(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ConfirmForThisCall(ctx, "k")
			_ = ConfirmedThisCall(ctx, "k")
			UnconfirmForThisCall(ctx, "j")
		}()
	}
	wg.Wait()
	if !ConfirmedThisCall(ctx, "k") {
		t.Error("the confirmation was lost under concurrent use")
	}
}
