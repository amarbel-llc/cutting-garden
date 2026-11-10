package command_components

import (
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/interfaces"
)

// CallScope opens a call scope on an errors.Context, idempotently, and
// confirmations made through it hold for the rest of the call.
func TestCallScope_OpensOneScope(t *testing.T) {
	ctx := errors.MakeContextDefault()
	if cutting_garden_plugins.InCallScope(ctx) {
		t.Fatal("a fresh context is already in a call scope")
	}
	scoped := CallScope(ctx)
	if !cutting_garden_plugins.InCallScope(scoped) {
		t.Fatal("CallScope did not open a call scope")
	}
	if again := CallScope(scoped); again != scoped {
		t.Error("CallScope on a scoped context opened a second scope")
	}
	cutting_garden_plugins.ConfirmForThisCall(scoped, "k")
	if !cutting_garden_plugins.ConfirmedThisCall(CallScope(scoped), "k") {
		t.Error("a confirmation did not hold for the rest of the call")
	}
}

// The wrapper changes only Value: cancelling through it cancels the embedded
// context, and an After hook registered through it runs when the embedded
// context's Run returns.
func TestCallScope_CancelAndAfterReachTheEmbeddedContext(t *testing.T) {
	cause := errors.BadRequestf("cancelled through the scope")
	err := errors.MakeContextDefault().Run(func(c errors.Context) {
		CallScope(c).Cancel(cause)
	})
	if err == nil || !errors.Is(err, cause) {
		t.Errorf("Run = %v, want the cause cancelled through the scope", err)
	}

	ran := false
	if err := errors.MakeContextDefault().Run(func(c errors.Context) {
		CallScope(c).After(func(interfaces.ActiveContext) error {
			ran = true
			return nil
		})
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !ran {
		t.Error("an After hook registered through the scope did not run")
	}
}
