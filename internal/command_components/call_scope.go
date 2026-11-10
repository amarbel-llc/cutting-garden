package command_components

import (
	"context"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// CallScope marks one host call on a command's errors.Context
// (cutting-garden#311, #341): plugin calls made through the returned context
// share per-call confirmations (an org file's ETag, a caldav calendar-home's
// discovery), so a command that walks many nodes confirms each source once.
// It is the errors.Context form of cutting_garden_plugins.WithCallScope, the
// one way a command opens a scope, and like it is idempotent.
//
// errors.Context cannot be re-wrapped the way a context.Context is: the
// returned value embeds ctx and changes only Value, so Cancel, After, Run and
// the rest still act on ctx itself.
func CallScope(ctx errors.Context) errors.Context {
	if cutting_garden_plugins.InCallScope(ctx) {
		return ctx
	}
	return scopedContext{Context: ctx, scope: cutting_garden_plugins.WithCallScope(ctx)}
}

// scopedContext is an errors.Context whose Value also answers the call scope.
type scopedContext struct {
	errors.Context
	scope context.Context
}

// Value answers from the scope context, which falls back to the embedded
// context for every other key.
func (c scopedContext) Value(key any) any { return c.scope.Value(key) }
