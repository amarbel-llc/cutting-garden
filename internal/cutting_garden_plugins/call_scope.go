package cutting_garden_plugins

import (
	"context"
	"sync"
)

// Per-call freshness (cutting-garden#311). A plugin that caches what it fetched
// has to decide when the cache may be trusted. A time window answers that with
// a guess: it can serve data up to N seconds stale across separate calls, and it
// does not express the real boundary, which is ONE HOST CALL — an mcp
// tools/call or resources/read, one cg invocation. Within a call, a plugin that
// has just confirmed a cached entry against its source (an ETag, a ctag, a
// state token) can safely trust that confirmation for the rest of the call: the
// caller is reasoning about one coherent snapshot, and re-confirming per node of
// a walk is pure cost. Across calls it must confirm again.
//
// The host marks a call by wrapping the context it serves it with
// WithCallScope; a plugin records "I confirmed key" with ConfirmForThisCall and
// asks with ConfirmedThisCall. Outside a call scope (a test, a direct caller)
// nothing is ever confirmed, so a plugin re-validates every time — the safe
// default.
//
// This is about CORRECTNESS-NEUTRAL dedupe within a call. It is not a
// rate-limit cache: a backend that charges per request (ynab's 200/hour) wants
// to reuse a fetch ACROSS calls too, which is a staleness policy a plugin owns,
// not a freshness proof this scope can supply.

type callScopeKey struct{}

// callScope is the set of keys a plugin has confirmed during one call.
type callScope struct {
	mu        sync.Mutex
	confirmed map[string]bool
}

// WithCallScope returns a context that marks one host call. It is idempotent: a
// context already inside a call scope is returned unchanged, so a nested host
// layer (a tool that reads a resource) can wrap defensively without shortening
// the outer call's scope.
func WithCallScope(ctx context.Context) context.Context {
	if _, ok := ctx.Value(callScopeKey{}).(*callScope); ok {
		return ctx
	}
	return context.WithValue(ctx, callScopeKey{}, &callScope{confirmed: map[string]bool{}})
}

// InCallScope reports whether ctx is inside a host call scope.
func InCallScope(ctx context.Context) bool {
	_, ok := ctx.Value(callScopeKey{}).(*callScope)
	return ok
}

// ConfirmedThisCall reports whether key was confirmed during the current call.
// It is always false outside a call scope. Keys are plugin-chosen and SHOULD be
// prefixed with the plugin's scheme and the account, so two plugins (or two
// accounts) never confirm each other's entries.
func ConfirmedThisCall(ctx context.Context, key string) bool {
	scope, ok := ctx.Value(callScopeKey{}).(*callScope)
	if !ok {
		return false
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	return scope.confirmed[key]
}

// ConfirmForThisCall records that key was just confirmed against its source, so
// ConfirmedThisCall reports true for the rest of the call. A no-op outside a
// call scope.
func ConfirmForThisCall(ctx context.Context, key string) {
	scope, ok := ctx.Value(callScopeKey{}).(*callScope)
	if !ok {
		return
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	scope.confirmed[key] = true
}

// UnconfirmForThisCall withdraws a confirmation: a plugin calls it after its OWN
// write to the entry, which the confirmation no longer describes. A no-op
// outside a call scope.
func UnconfirmForThisCall(ctx context.Context, key string) {
	scope, ok := ctx.Value(callScopeKey{}).(*callScope)
	if !ok {
		return
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	delete(scope.confirmed, key)
}
