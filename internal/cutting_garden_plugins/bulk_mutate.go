package cutting_garden_plugins

import (
	"context"
	"net/url"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// BulkAtomicity selects how a BulkMutate call is expected to complete
// (RFC 0017 §Atomicity semantics). The zero value is NOT a valid request
// value — a caller MUST set one of the two constants; a plugin receiving
// neither MUST reject the request as a bad request. The best-effort
// DEFAULT for a caller that omits it lives at the tool/parameter layer,
// never in this Go zero value.
type BulkAtomicity string

const (
	// BulkBestEffort applies each targeted node independently; partial
	// completion is expected and reported via BulkResult. REQUIRED of
	// every BulkMutator for every request shape it accepts — the floor.
	BulkBestEffort BulkAtomicity = "best-effort"
	// BulkAtomic requires all-or-nothing completion. A plugin that cannot
	// honor it — for this request or at all — MUST reject the request with
	// an error; it MUST NOT silently downgrade to BulkBestEffort.
	BulkAtomic BulkAtomicity = "atomic"
)

// BulkOpKind is the mutation verb of one op — the same four verbs
// NodeMutator defines, applied per targeted node.
type BulkOpKind string

const (
	BulkCreate BulkOpKind = "create"
	BulkPut    BulkOpKind = "put"
	BulkPatch  BulkOpKind = "patch"
	BulkDelete BulkOpKind = "delete"
)

// BulkOp is one mutation, addressed exactly as a NodeMutator call is. Its
// per-verb Body/Type meaning is NodeMutator's verbatim (FDR 0020) — this
// type batches those semantics, it does not redefine them.
type BulkOp struct {
	Kind BulkOpKind
	// URI is the target node. In an explicit changeset (BulkRequest.Ops) it
	// MUST be set. Inside a BulkSweep.Op template it is IGNORED (the plugin
	// fills in each matched node's URI) and SHOULD be left nil.
	URI *url.URL
	// Body carries the create/put/patch payload — NodeMutator's io.Reader
	// body, materialized to bytes because a BulkRequest holds many ops as
	// data (a stream is single-use, and an atomic request MAY validate
	// every op before applying any). Unused for BulkDelete.
	Body []byte
	// Type is a NodeType.Tag, meaningful only for Kind == BulkCreate
	// (identical to NodeMutator.CreateNode's typ parameter).
	Type string
}

// BulkSweep selects a node set by predicate — a container Root plus a
// FacetFilter (RFC 0012 §6, reused verbatim) — and applies one Op
// template to every match. The plugin owns the enumeration: the framework
// MUST NOT resolve the match set and hand over a URI list (RFC 0017
// §Selection), which is what lets a plugin that expresses "match and
// apply" as one backend operation genuinely promise bulk-atomic.
type BulkSweep struct {
	// Root is the subtree matches are resolved under. MUST be non-nil.
	Root *url.URL
	// Filter narrows the match set by RFC 0012 §6 conjunctive equality
	// predicates; an empty filter matches every node reachable under Root.
	// A repeated or undeclared dimension MUST be rejected exactly as for a
	// facet read.
	Filter FacetFilter
	// Op is the per-match operation template (Op.URI ignored). Op.Kind is
	// typically BulkPatch or BulkDelete — a sweep targets EXISTING matched
	// nodes, so BulkCreate is invalid here and MUST be rejected; BulkPut is
	// permitted (full-replace every match with the same body).
	Op BulkOp
}

// BulkRequest is one bulk-mutate call. EXACTLY ONE of Ops or Sweep MUST be
// set (both, or neither, is a bad request); Ops, when set, MUST be
// non-empty.
type BulkRequest struct {
	// Ops is the explicit changeset: a heterogeneous list of operations on
	// distinct nodes, applied together — for a caller that already knows
	// which nodes to touch and how.
	Ops []BulkOp
	// Sweep is the predicate form: resolve a match set under Root by
	// Filter, then apply one Op to each — for "every node matching this
	// condition" without enumerating URIs.
	Sweep *BulkSweep
	// Atomicity is the caller-requested completion mode. MUST be one of
	// BulkBestEffort or BulkAtomic.
	Atomicity BulkAtomicity
}

// BulkFailure records one targeted node's failure inside a best-effort
// result.
type BulkFailure struct {
	URI *url.URL
	Err string
}

// BulkResult is a BulkMutate call's outcome; its shape differs by
// Atomicity (RFC 0017 §Atomicity semantics). AppliedNodes is named for
// its unit (NODES) to avoid colliding with PatchNode's field-level applied
// (#182/#184).
type BulkResult struct {
	// AppliedNodes lists every node the operation was successfully applied
	// to (credential-free). For a BulkPatch, membership means PatchNode's
	// non-empty applied: at least one recognized field landed — a target
	// whose patch applied NOTHING goes in PatchedNothing, never here, so
	// bulk patch cannot resurrect #180's silent false success at scale.
	AppliedNodes []*url.URL
	// PatchedNothing lists every BulkPatch target whose patch was ACCEPTED
	// but applied zero recognized fields — the bulk carrier of PatchNode's
	// authoritative-empty applied. Neither success nor failure; the caller
	// judges it. Empty for the other three verbs.
	PatchedNothing []*url.URL
	// Failed lists every targeted node whose operation did not apply, with
	// a diagnostic. In best-effort mode AppliedNodes + PatchedNothing +
	// Failed together are exactly the request's targeted node set.
	Failed []BulkFailure
	// Atomic reports whether the call committed atomically — true ONLY on
	// an atomic-mode success; false in every best-effort result and the
	// zero value.
	Atomic bool
}

// BulkMutator is the OPTIONAL bulk write capability (RFC 0017). Probed by
// type assertion exactly as NodeMutator. It deliberately does NOT embed
// NodeMutator: the two are independent narrow capabilities (RFC 0009's
// growth-by-new-interfaces policy), with BulkMutator's per-op semantics
// defined as NodeMutator's verbatim regardless of whether NodeMutator is
// also implemented.
type BulkMutator interface {
	RootLister

	// BulkMutate applies req's operations per its Atomicity (RFC 0017
	// §Atomicity semantics). A validation failure — neither/both of
	// Ops/Sweep set, empty Ops, an unsupported Atomicity, a BulkCreate
	// inside a Sweep — MUST return a non-nil error and MUST NOT partially
	// apply anything. In best-effort mode a per-NODE failure is a
	// BulkFailure in the result, never a non-nil error. In atomic mode any
	// failure fails the WHOLE call (non-nil error, nothing applied); a
	// plugin that cannot honor atomic for this request MUST reject it, never
	// downgrade to best-effort.
	BulkMutate(ctx context.Context, req BulkRequest) (BulkResult, error)
}

// AtomicBulkMutator marks a BulkMutator whose backend CAN honor BulkAtomic
// for at least some request shapes — the Go-side signal the server
// type-asserts to advertise the bulk-atomic capability token (RFC 0017
// §Atomicity). It is probed by type assertion exactly as BulkMutator and
// NodeMutator (RFC 0009's growth-by-new-interfaces policy).
//
// A BulkMutator WITHOUT this marker MUST reject every atomic request (the
// reject-never-downgrade floor). WITH it, the plugin advertises bulk-atomic
// but MAY still reject a SPECIFIC request it cannot transact, returning
// ErrBulkAtomicUnsupported — the marker is a "can, for some shapes" claim,
// not "can, for all". BulkAtomicCapable gates the advertisement on runtime
// state (a backend whose transaction support depends on the account or
// server); a plugin that can always transact simply returns true, and one
// that computes it once may cache it — the server calls it when building
// the initialize capability list, not per request.
type AtomicBulkMutator interface {
	BulkMutator

	// BulkAtomicCapable reports whether this plugin can currently honor a
	// BulkAtomic request for at least some shapes. False makes the plugin
	// indistinguishable from a plain BulkMutator (bulk-atomic not
	// advertised); true advertises bulk-atomic.
	BulkAtomicCapable() bool
}

// valid reports whether k is one of the four defined verbs.
func (k BulkOpKind) valid() bool {
	switch k {
	case BulkCreate, BulkPut, BulkPatch, BulkDelete:
		return true
	default:
		return false
	}
}

// Validate checks req's SHAPE against RFC 0017's request rules — the
// bad-request cases a plugin MUST reject before applying anything: an
// unsupported atomicity, neither/both of Ops/Sweep set, an empty Ops, an
// op with no URI or an unknown kind, a sweep with no Root, and a
// BulkCreate inside a Sweep. It does NOT validate per-op bodies (those are
// per-verb, per-node, and plugin-defined). Every violation is an
// errors.Is400BadRequest error, so the wire transport maps it to a
// caller-fault code. The server calls this before dispatch, and a linked
// plugin MAY call it too.
func (r BulkRequest) Validate() error {
	switch r.Atomicity {
	case BulkBestEffort, BulkAtomic:
	default:
		return errors.BadRequestf(
			"bulk_mutate: atomicity must be %q or %q, got %q",
			BulkBestEffort, BulkAtomic, r.Atomicity,
		)
	}

	hasOps := len(r.Ops) > 0
	hasSweep := r.Sweep != nil

	switch {
	case hasOps && hasSweep:
		return errors.BadRequestf(
			"bulk_mutate: exactly one of ops or sweep may be set, got both",
		)
	case !hasOps && !hasSweep:
		return errors.BadRequestf(
			"bulk_mutate: exactly one of ops (non-empty) or sweep must be set",
		)
	}

	if hasOps {
		for i, op := range r.Ops {
			if op.URI == nil {
				return errors.BadRequestf("bulk_mutate: ops[%d] has no uri", i)
			}
			if !op.Kind.valid() {
				return errors.BadRequestf(
					"bulk_mutate: ops[%d] kind %q is not"+
						" create/put/patch/delete", i, op.Kind,
				)
			}
		}

		return nil
	}

	if r.Sweep.Root == nil {
		return errors.BadRequestf("bulk_mutate: sweep.root is required")
	}
	if !r.Sweep.Op.Kind.valid() {
		return errors.BadRequestf(
			"bulk_mutate: sweep.op.kind %q is not create/put/patch/delete",
			r.Sweep.Op.Kind,
		)
	}
	if r.Sweep.Op.Kind == BulkCreate {
		return errors.BadRequestf(
			"bulk_mutate: sweep.op.kind create is invalid — a sweep targets" +
				" existing matched nodes",
		)
	}

	return nil
}
