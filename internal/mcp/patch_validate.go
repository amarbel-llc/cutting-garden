package mcp

import (
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Host-side validation of patch bodies against a declared per-type writable
// key set (cutting-garden#404, docs/plans/2026-11-15-strict-ignored-patch-
// fields-options.md, Ruling). Tolerant (the default) strips the undeclared keys
// before the plugin sees them and reports them as ignored; strict refuses the
// op before anything is written. Plugins never see an unknown key or the flag.

// patchKeyResolver is the seam through which the host learns, for a patch
// target, the node type's tag and its declared writable keys. ok is false when
// the plugin declares nothing for that target: tolerant passes the body
// through untouched, strict refuses. Production uses the plugin's
// PatchKeysDescriber (patchKeysFor); tests may plug in anything.
type patchKeyResolver interface {
	PatchKeys(uri *url.URL) (tag string, keys []string, ok bool)
}

// describedPatchKeys resolves through a lister's PatchKeysDescriber.
type describedPatchKeys struct {
	lister cutting_garden_plugins.RootLister
}

func (d describedPatchKeys) PatchKeys(uri *url.URL) (string, []string, bool) {
	return cutting_garden_plugins.ResolvePatchKeys(d.lister, uri)
}

// patchKeysFor is the resolver for lister's plugin; nil when it declares no
// patch keys at all.
func patchKeysFor(lister cutting_garden_plugins.RootLister) patchKeyResolver {
	if _, ok := lister.(cutting_garden_plugins.PatchKeysDescriber); !ok {
		return nil
	}

	return describedPatchKeys{lister: lister}
}

// patchVerdict is one patch body checked against its declared keys.
type patchVerdict struct {
	// Body is what to dispatch: the original bytes when nothing was stripped.
	Body []byte
	// Ignored are the body's undeclared keys, sorted; nil when there are none.
	Ignored []string
	// Empty is true when the body named keys but none was declared, so
	// stripping leaves nothing to dispatch.
	Empty bool
}

// validatePatchBody checks body against the declared keys. A body that is not
// a JSON object is passed through: its rejection belongs to the plugin, which
// owns the body's grammar.
func validatePatchBody(body []byte, declared []string) patchVerdict {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || len(fields) == 0 {
		return patchVerdict{Body: body}
	}

	ignored := cutting_garden_plugins.IgnoredPatchFields(fields, declared)
	if len(ignored) == 0 {
		return patchVerdict{Body: body}
	}

	if len(ignored) == len(fields) {
		return patchVerdict{Body: body, Ignored: ignored, Empty: true}
	}

	for _, key := range ignored {
		delete(fields, key)
	}

	kept, err := json.Marshal(fields)
	if err != nil {
		// Unreachable for a map of RawMessages that just unmarshalled; send
		// the original rather than invent a body.
		return patchVerdict{Body: body, Ignored: ignored}
	}

	return patchVerdict{Body: kept, Ignored: ignored}
}

// hostResolvedOp is an op the host settled itself, without dispatching it.
type hostResolvedOp struct {
	status  cutting_garden_plugins.BulkOutcomeStatus
	ignored []string
	err     string
}

// patchScreen is the result of screening a request's patch ops: the ops to
// dispatch (their original indices in dispatched), the ops the host settled,
// and the keys the host stripped from dispatched ops.
type patchScreen struct {
	dispatched []int
	resolved   map[int]hostResolvedOp
	stripped   map[int][]string
}

// screenPatches checks each patch op of ops against resolver. strict refuses
// an op naming an undeclared key, or one whose target declares nothing;
// tolerant strips. In atomic mode a strict refusal fails the call before any
// write, and an op left empty by stripping keeps its original body (the plugin
// decides what an all-ignored body means inside a transaction).
//
// ops is rewritten in place for the dispatched ones; the returned screen maps
// results back to request positions.
func screenPatches(
	ops []cutting_garden_plugins.BulkOp,
	resolver patchKeyResolver,
	strict, atomic bool,
) (patchScreen, error) {
	screen := patchScreen{
		resolved: map[int]hostResolvedOp{},
		stripped: map[int][]string{},
	}

	var refusals []string
	for i := range ops {
		op := &ops[i]
		if op.Kind != cutting_garden_plugins.BulkPatch {
			screen.dispatched = append(screen.dispatched, i)
			continue
		}

		var keys []string
		var declared bool
		if resolver != nil && op.URI != nil {
			_, keys, declared = resolver.PatchKeys(op.URI)
		}

		if !declared {
			if strict {
				refusals = append(refusals, strictUndeclaredMessage(i, op))
				screen.resolved[i] = hostResolvedOp{
					status: cutting_garden_plugins.OutcomeFailed,
					err:    strictUndeclaredMessage(i, op),
				}
				continue
			}

			screen.dispatched = append(screen.dispatched, i)
			continue
		}

		verdict := validatePatchBody(op.Body, keys)
		switch {
		case len(verdict.Ignored) == 0:
			screen.dispatched = append(screen.dispatched, i)

		case strict:
			msg := strictIgnoredMessage(i, op, verdict.Ignored)
			refusals = append(refusals, msg)
			screen.resolved[i] = hostResolvedOp{
				status:  cutting_garden_plugins.OutcomeFailed,
				ignored: verdict.Ignored,
				err:     msg,
			}

		case verdict.Empty && atomic:
			screen.dispatched = append(screen.dispatched, i)

		case verdict.Empty:
			screen.resolved[i] = hostResolvedOp{
				status:  cutting_garden_plugins.OutcomePatchedNothing,
				ignored: verdict.Ignored,
			}

		default:
			op.Body = verdict.Body
			screen.stripped[i] = verdict.Ignored
			screen.dispatched = append(screen.dispatched, i)
		}
	}

	if atomic && len(refusals) > 0 {
		return patchScreen{}, errors.BadRequestf(
			"mutate_nodes: strict: nothing was written: %s",
			strings.Join(refusals, "; "),
		)
	}

	return screen, nil
}

func opLabel(i int, op *cutting_garden_plugins.BulkOp) string {
	if op.URI != nil {
		return op.URI.String()
	}

	return "ops[" + strconv.Itoa(i) + "]"
}

func strictIgnoredMessage(
	i int, op *cutting_garden_plugins.BulkOp, ignored []string,
) string {
	return "strict: " + opLabel(i, op) + " names keys the node type does not" +
		" accept (" + strings.Join(ignored, ", ") + "); nothing was written for it"
}

func strictUndeclaredMessage(i int, op *cutting_garden_plugins.BulkOp) string {
	return "strict: " + opLabel(i, op) + " cannot be checked: its plugin does" +
		" not declare the keys a patch of this node accepts; nothing was written for it"
}

// mergeScreen folds the host-settled ops and the keys the host stripped into
// the plugin's result for the dispatched ops, in request order. ops is the
// full request (for URIs); result describes only the dispatched subset.
//
// Per-op outcomes are rebuilt only when the plugin reported them for every
// dispatched op (or when nothing was dispatched): an empty Outcomes means "not
// reported", and inventing statuses for the plugin's ops would be a guess, so
// the four lists carry the host-settled ops alone then.
func mergeScreen(
	result *cutting_garden_plugins.BulkResult,
	ops []cutting_garden_plugins.BulkOp,
	screen patchScreen,
) {
	reported := len(screen.dispatched) == 0 ||
		len(result.Outcomes) == len(screen.dispatched)

	for k, original := range screen.dispatched {
		extra := screen.stripped[original]
		if len(extra) == 0 || !reported || k >= len(result.Outcomes) {
			continue
		}

		result.Outcomes[k].Ignored = unionSorted(result.Outcomes[k].Ignored, extra)
	}

	order := make([]int, 0, len(screen.resolved))
	for i := range screen.resolved {
		order = append(order, i)
	}
	slices.Sort(order)

	for _, i := range order {
		settled := screen.resolved[i]
		switch settled.status {
		case cutting_garden_plugins.OutcomePatchedNothing:
			result.PatchedNothing = append(result.PatchedNothing, ops[i].URI)
		default:
			result.Failed = append(result.Failed, cutting_garden_plugins.BulkFailure{
				URI: ops[i].URI, Err: settled.err,
			})
		}
	}

	if !reported {
		return
	}

	merged := make([]cutting_garden_plugins.BulkOutcome, 0, len(ops))
	next := 0
	for i := range ops {
		if settled, ok := screen.resolved[i]; ok {
			merged = append(merged, cutting_garden_plugins.BulkOutcome{
				URI: ops[i].URI, Status: settled.status,
				Ignored: settled.ignored, Err: settled.err,
			})
			continue
		}

		merged = append(merged, result.Outcomes[next])
		next++
	}
	result.Outcomes = merged
}

// unionSorted is the sorted union of two key lists.
func unionSorted(a, b []string) []string {
	out := slices.Clone(a)
	for _, key := range b {
		if !slices.Contains(out, key) {
			out = append(out, key)
		}
	}
	slices.Sort(out)

	return out
}
