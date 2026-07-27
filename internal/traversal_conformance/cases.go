package traversal_conformance

// The slice-1 case list (docs/plans/2026-07-23-traversal-conformance-
// driver.md §Case list). Each case asserts against the RAW JSON-RPC
// result bytes: key PRESENCE is load-bearing twice over — node.patch's
// applied distinguishes present-empty from omitted (#182), and
// facets.counts' by_container distinguishes an honest omission from an
// empty list (#173) — and only the raw object shows the difference.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/traversal_serve"
)

// Test point names, fixed so a wrapping lane (the bats portable case,
// an external peer's CI) can grep for specific verdicts.
const (
	nameInitialize    = "initialize: schema echo, schemes, capabilities"
	nameUnknownCode   = "error code: unknown method is -32601"
	nameBadURICode    = "error code: malformed nodes.list uri is -32602"
	namePatchCreate   = "node.create: probe node"
	namePatchApplied  = "node.patch: recognized fields reported applied"
	namePatchEmpty    = "node.patch: unrecognized-only body reports applied present-empty"
	namePatchWrong    = "node.patch: wrong-typed body is -32602"
	namePatchDelete   = "node.delete: probe cleanup"
	nameByContainer   = "facets.counts: by_container raw invariants"
	nameDescend       = "facets.counts: descend targets reachable"
	nameContainerBody = "leaf.read: container returns its own body beside children"
	nameURITemplate   = "uri template: container resolves to its body-declaring type"
)

// malformedURI violates RFC 3986 (an unterminated IP-literal in the
// authority), so ANY conformant URI parser — not just Go's — must
// refuse it; nodes.list must classify that refusal as the caller's
// mistake, -32602, not a plugin failure (cutting-garden#185).
const malformedURI = "cg-conformance://[unterminated-ip-literal"

// unknownMethod is a name no RFC 0013 revision defines, so it is
// unadvertised by construction; JSON-RPC 2.0 requires -32601.
const unknownMethod = "conformance/no-such-method"

// probeName is the deterministic leaf name mutation probes use under
// the manifest's writable container.
const probeName = "cg-conformance-probe"

// caseInitialize issues initialize AS a test case (the reason the
// driver launches via LaunchWithoutInitialize: production Launch
// consumes the result in bring-up validation, but here the raw result
// IS the assertion subject). A non-nil return means there is no
// initialized session to run further cases against.
func (r *runner) caseInitialize(ctx context.Context) error {
	var raw json.RawMessage
	err := r.session.Call(
		ctx, traversal_serve.MethodInitialize,
		traversal_serve.InitializeParams{
			ProtocolVersions: []string{traversal_serve.SchemaV1},
			ConfigTOML:       r.manifest.ConfigTOML,
		}, &raw,
	)
	if err != nil {
		r.tap.NotOk(nameInitialize, map[string]string{"error": err.Error()})
		return err
	}

	problems := map[string]string{}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		problems["result"] = fmt.Sprintf("not a JSON object: %s", err)
		r.tap.NotOk(nameInitialize, problems)

		return errors.ErrorWithStackf("initialize result is not an object")
	}

	// Schema echo: the single selected version token (RFC 0013
	// §Handshake).
	var schema string
	if schemaRaw, present := fields["schema"]; !present {
		problems["schema"] = "missing"
	} else if err := json.Unmarshal(schemaRaw, &schema); err != nil {
		problems["schema"] = fmt.Sprintf("not a string: %s", err)
	} else if schema != traversal_serve.SchemaV1 {
		problems["schema"] = fmt.Sprintf(
			"%q, want %q", schema, traversal_serve.SchemaV1,
		)
	}

	// Schemes echo vs the manifest (order-insensitive: the wire order
	// is the plugin's own; the SET is the contract).
	var schemes []string
	if schemesRaw, present := fields["schemes"]; !present {
		problems["schemes"] = "missing"
	} else if err := json.Unmarshal(schemesRaw, &schemes); err != nil {
		problems["schemes"] = fmt.Sprintf("not a string array: %s", err)
	} else if !sameStringSet(schemes, r.manifest.Schemes) {
		problems["schemes"] = fmt.Sprintf(
			"%v, manifest expects %v", schemes, r.manifest.Schemes,
		)
	}

	// Capabilities well-formed: an array of non-empty strings with no
	// duplicates. UNKNOWN tokens are tolerated (RFC 0013 §Method set:
	// the host MUST ignore tokens it does not know — that is how the
	// protocol grows), so token values are not validated against the
	// Cap* vocabulary. An absent key reads as "no optional capability".
	if capsRaw, present := fields["capabilities"]; present {
		var items []json.RawMessage
		if err := json.Unmarshal(capsRaw, &items); err != nil {
			problems["capabilities"] = fmt.Sprintf("not an array: %s", err)
		} else {
			seen := map[string]bool{}
			for i, item := range items {
				var token string
				if err := json.Unmarshal(item, &token); err != nil {
					problems["capabilities"] = fmt.Sprintf(
						"entry %d is not a string: %s", i, item,
					)
					break
				}
				if token == "" {
					problems["capabilities"] = fmt.Sprintf(
						"entry %d is empty", i,
					)
					break
				}
				if seen[token] {
					problems["capabilities"] = fmt.Sprintf(
						"duplicate token %q", token,
					)
					break
				}
				seen[token] = true
			}
		}
	}

	// The decoded form is kept ONLY for capability gating of later
	// cases; assertions above read the raw fields.
	if err := json.Unmarshal(raw, &r.init); err != nil &&
		len(problems) == 0 {
		problems["result"] = fmt.Sprintf("does not decode: %s", err)
	}

	if len(problems) > 0 {
		r.tap.NotOk(nameInitialize, problems)
		return nil
	}

	r.tap.Ok(nameInitialize)

	return nil
}

// caseErrorCodes probes the two caller-fault codes slice 1 can
// construct generically (a plugin-fault probe is NOT generically
// constructible — the plan's documented limitation, left to each
// peer's own tests): an unadvertised method must answer -32601, and a
// malformed uri param must answer -32602. The codes mean opposite
// things operationally (cutting-garden#185), so a peer collapsing them
// converts caller mistakes into unretryable retry loops.
func (r *runner) caseErrorCodes(ctx context.Context) {
	err := r.session.Call(ctx, unknownMethod, struct{}{}, nil)
	r.assertRPCCode(
		nameUnknownCode, err, traversal_serve.CodeMethodNotFound,
	)

	err = r.session.Call(
		ctx, traversal_serve.MethodNodesList,
		traversal_serve.NodesListParams{URI: malformedURI}, nil,
	)
	r.assertRPCCode(nameBadURICode, err, traversal_serve.CodeInvalidParams)
}

// casePatchTriState pins node.patch's applied tri-state
// (cutting-garden#182) on a probe node: a recognized body reports the
// exact applied set, an unrecognized-only body reports applied PRESENT
// and empty (never omitted), and a wrong-typed body is refused as
// -32602. This is fj-cg's known-wrong case — the driver MUST fail
// their pre-76d80b4 build, which is the driver's own acceptance test.
// A read-only peer passes with every point SKIPped: RFC 0013 §Method
// set forbids calling an unadvertised method at all.
func (r *runner) casePatchTriState(ctx context.Context) {
	mutationPoints := []string{
		namePatchCreate, namePatchApplied, namePatchEmpty, namePatchWrong,
		namePatchDelete,
	}

	if !r.hasCapability(traversal_serve.CapMutate) {
		for _, name := range mutationPoints {
			r.tap.Skip(name, "mutate not advertised")
		}

		return
	}

	// A mutate-capable peer whose manifest omits the mutation
	// parameterization cannot be ratified — that is a loud failure, not
	// a skip, because silently narrowing the case list is the false
	// ratification a conformance tool must not hand out.
	if r.manifest.WritableContainer == "" || r.manifest.Create.Type == "" {
		for _, name := range mutationPoints {
			r.tap.NotOk(name, map[string]string{
				"manifest": "peer advertises mutate but the manifest" +
					" omits writable_container and/or create.type",
			})
		}

		return
	}

	probe := strings.TrimRight(r.manifest.WritableContainer, "/") +
		"/" + probeName

	// Probe hygiene (the #180 arc): a persistent peer may still hold
	// the probe from an earlier aborted run, and node.create is strict
	// (no upsert). Best-effort delete first; on a fresh peer this
	// fails "does not exist", which is exactly the state wanted.
	_ = r.session.Call(
		ctx, traversal_serve.MethodNodeDelete,
		traversal_serve.NodeDeleteParams{URI: probe}, nil,
	)

	err := r.session.Call(
		ctx, traversal_serve.MethodNodeCreate,
		traversal_serve.NodeCreateParams{
			URI:        probe,
			Type:       r.manifest.Create.Type,
			BodyBase64: encodeBody(r.manifest.Create.Body),
		}, nil,
	)
	if err != nil {
		r.tap.NotOk(namePatchCreate, map[string]string{
			"uri": probe, "error": err.Error(),
		})
		// The dependent patch points cannot run without the probe;
		// failing them (rather than skipping) keeps the verdict honest.
		for _, name := range []string{
			namePatchApplied, namePatchEmpty, namePatchWrong,
		} {
			r.tap.NotOk(name, map[string]string{
				"error": "probe create failed",
			})
		}
		r.tap.Skip(namePatchDelete, "probe was never created")

		return
	}
	r.tap.Ok(namePatchCreate)

	r.patchRecognized(ctx, probe)
	r.patchUnrecognizedOnly(ctx, probe)
	r.patchWrongTyped(ctx, probe)

	// Cleanup runs even when the points above failed — they only emit
	// verdicts, never abort — honoring "delete the probe node in
	// cleanup even on failure".
	err = r.session.Call(
		ctx, traversal_serve.MethodNodeDelete,
		traversal_serve.NodeDeleteParams{URI: probe}, nil,
	)
	if err != nil {
		r.tap.NotOk(namePatchDelete, map[string]string{
			"uri": probe, "error": err.Error(),
		})

		return
	}
	r.tap.Ok(namePatchDelete)
}

// patchRecognized asserts a recognized-fields patch reports EXACTLY the
// manifest's applied set, read from the raw result so an omitted key is
// distinguishable from a present one (#182: a plain decode through the
// wire struct would conflate omitted with nil).
func (r *runner) patchRecognized(ctx context.Context, probe string) {
	applied, _, ok := r.decodeApplied(
		ctx, namePatchApplied, probe, r.manifest.PatchRecognized.Body,
	)
	if !ok {
		return
	}

	if !sameStringSet(applied, r.manifest.PatchRecognized.ExpectApplied) {
		r.tap.NotOk(namePatchApplied, map[string]string{
			"applied": fmt.Sprintf(
				"%v, manifest expects %v",
				applied, r.manifest.PatchRecognized.ExpectApplied,
			),
		})

		return
	}

	r.tap.Ok(namePatchApplied)
}

// patchUnrecognizedOnly asserts an unrecognized-only patch reports
// applied PRESENT and empty — `[]` on the wire, not an omitted key and
// not null (#182's middle state: "I applied nothing" is an
// authoritative report, distinct from "I do not report"). A manifest
// with an empty body declares the state unconstructible for this peer
// (it recognizes every key, like the cgtest testpeer's merge-anything
// format) and the point SKIPs.
func (r *runner) patchUnrecognizedOnly(ctx context.Context, probe string) {
	if r.manifest.PatchUnrecognizedOnly.Body == "" {
		r.tap.Skip(
			namePatchEmpty,
			"peer tolerates every patch key"+
				" (manifest omits patch_unrecognized_only body)",
		)

		return
	}

	applied, raw, ok := r.decodeApplied(
		ctx, namePatchEmpty, probe, r.manifest.PatchUnrecognizedOnly.Body,
	)
	if !ok {
		return
	}

	// Unmarshal keeps null and [] apart: [] yields a non-nil empty
	// slice, null leaves it nil. null is omission in disguise, so the
	// raw bytes are the honest diagnostic (a nil slice prints as `[]`).
	if applied == nil || len(applied) != 0 {
		r.tap.NotOk(namePatchEmpty, map[string]string{
			"applied": fmt.Sprintf("%s, want []", raw),
		})

		return
	}

	r.tap.Ok(namePatchEmpty)
}

// decodeApplied issues one patch and decodes its raw `applied` field,
// emitting under `name` the NotOk for the three failure modes the two
// applied cases share — the call errored, the key was OMITTED (a peer
// that accepted a patch MUST report applied, #182), or it was not a
// string array — and returning ok == false when it did. On ok == true
// the caller validates applied's VALUE, the part that differs (an exact
// set vs. present-empty). raw is the undecoded bytes, so a caller can
// tell null from [] in its own diagnostic, which the decoded slice
// cannot.
func (r *runner) decodeApplied(
	ctx context.Context, name, probe, body string,
) (applied []string, raw json.RawMessage, ok bool) {
	appliedRaw, present, err := r.patchRaw(ctx, probe, body)
	if err != nil {
		r.tap.NotOk(name, map[string]string{"error": err.Error()})

		return nil, nil, false
	}

	if !present {
		r.tap.NotOk(name, map[string]string{
			"applied": "key omitted — a peer that accepted the patch must" +
				" report applied (cutting-garden#182)",
		})

		return nil, nil, false
	}

	if err := json.Unmarshal(appliedRaw, &applied); err != nil {
		r.tap.NotOk(name, map[string]string{
			"applied": fmt.Sprintf("not a string array: %s", appliedRaw),
		})

		return nil, nil, false
	}

	return applied, appliedRaw, true
}

// patchWrongTyped asserts a wrong-typed patch body is refused with
// -32602: the peer knows the body is unusable, and that verdict is the
// caller-fault classification #185 obliges it to put on the wire.
func (r *runner) patchWrongTyped(ctx context.Context, probe string) {
	err := r.session.Call(
		ctx, traversal_serve.MethodNodePatch,
		traversal_serve.NodePatchParams{
			URI:        probe,
			BodyBase64: encodeBody(r.manifest.PatchWrongTyped.Body),
		}, nil,
	)
	r.assertRPCCode(namePatchWrong, err, traversal_serve.CodeInvalidParams)
}

// breakdownEntry is the driver's raw-decode shape of one by_container
// entry (mirroring FacetContainerBreakdownView, but decoded here from
// the raw bytes so no wire-struct defaulting intervenes).
type breakdownEntry struct {
	URI   string `json:"uri"`
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

// caseByContainer checks the RFC 0012 §13 invariants on a RAW
// by_container breakdown (cutting-garden#173): every count positive,
// entries sorted by (count desc, uri asc), at most
// FacetContainerBreakdownLimit entries. This is THE case that justifies
// the raw-wire architecture — the host normalizes a non-conformant
// breakdown at the adapter boundary, so only a pre-normalization reader
// can see the violation. An ABSENT breakdown passes: omission is honest
// (the §13 union-narrowing note means a conformant peer may simply not
// compute one). Returns the decoded entries and parsed filter for the
// descend case, plus a non-empty skip reason when that case should not
// run.
func (r *runner) caseByContainer(
	ctx context.Context,
) (entries []breakdownEntry, filter []traversal_serve.PredicateView, descendSkip string) {
	spec := r.manifest.FacetContainer
	if spec == nil {
		const reason = "manifest declares no facet_container"
		r.tap.Skip(nameByContainer, reason)

		return nil, nil, reason
	}

	if !r.hasCapability(traversal_serve.CapFacetCounts) {
		const reason = "facet-counts not advertised"
		r.tap.Skip(nameByContainer, reason)

		return nil, nil, reason
	}

	filter, err := parseFilter(spec.Filter)
	if err != nil {
		r.tap.NotOk(nameByContainer, map[string]string{
			"manifest": err.Error(),
		})

		return nil, nil, "facet_container filter did not parse"
	}

	var raw json.RawMessage
	err = r.session.Call(
		ctx, traversal_serve.MethodFacetCounts,
		traversal_serve.FacetCountsParams{URI: spec.URI, Filter: filter},
		&raw,
	)
	if err != nil {
		r.tap.NotOk(nameByContainer, map[string]string{
			"uri": spec.URI, "error": err.Error(),
		})

		return nil, nil, "facets.counts failed"
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		r.tap.NotOk(nameByContainer, map[string]string{
			"result": fmt.Sprintf("not a JSON object: %s", err),
		})

		return nil, nil, "facets.counts result was not an object"
	}

	entriesRaw, present := fields["by_container"]
	if !present {
		r.tap.Ok(nameByContainer)
		r.tap.Comment(
			"by_container absent — omission is honest (RFC 0012 §13)",
		)

		return nil, filter, "peer emits no by_container breakdown"
	}

	if err := json.Unmarshal(entriesRaw, &entries); err != nil {
		r.tap.NotOk(nameByContainer, map[string]string{
			"by_container": fmt.Sprintf("does not decode: %s", err),
		})

		return nil, nil, "by_container did not decode"
	}

	problems := map[string]string{}

	if len(entries) > cutting_garden_plugins.FacetContainerBreakdownLimit {
		problems["length"] = fmt.Sprintf(
			"%d entries, cap is %d (RFC 0012 §13)",
			len(entries),
			cutting_garden_plugins.FacetContainerBreakdownLimit,
		)
	}

	for i, entry := range entries {
		if entry.Count <= 0 {
			problems[fmt.Sprintf("entry[%d]", i)] = fmt.Sprintf(
				"%s: count %d — only contributing containers may be"+
					" listed (count > 0)", entry.URI, entry.Count,
			)
		}

		if i == 0 {
			continue
		}

		previous := entries[i-1]
		ordered := previous.Count > entry.Count ||
			(previous.Count == entry.Count && previous.URI < entry.URI)
		if !ordered {
			problems[fmt.Sprintf("order[%d]", i)] = fmt.Sprintf(
				"(%s, %d) after (%s, %d) — want count desc, uri asc",
				entry.URI, entry.Count, previous.URI, previous.Count,
			)
		}
	}

	if len(problems) > 0 {
		r.tap.NotOk(nameByContainer, problems)

		return entries, filter, ""
	}

	r.tap.Ok(nameByContainer)

	return entries, filter, ""
}

// caseDescendTargets is the RFC 0012 §13 attribution ruling's
// descend-target property: a breakdown entry is a promise that
// descending into the entry URI reaches the attributed items.
//
// NodesListParams carries no filter on the wire (only facets.counts
// takes one — see wire.go), so the plan's "re-issue with the SAME
// filter" cannot be expressed over nodes.list; slice 1 therefore
// asserts plain REACHABILITY: the listing call succeeds and returns at
// least one immediate child whenever the entry attributes >= 1 match
// (a matching descendant necessarily lives under some immediate
// child). Filter-narrowed membership is the deferred class-B
// strengthening.
func (r *runner) caseDescendTargets(
	ctx context.Context,
	entries []breakdownEntry,
	filter []traversal_serve.PredicateView,
	descendSkip string,
) {
	_ = filter // held for the class-B same-filter re-issue (see above)

	if descendSkip != "" {
		r.tap.Skip(nameDescend, descendSkip)

		return
	}

	problems := map[string]string{}

	for i, entry := range entries {
		if entry.Count < 1 {
			// Already failed the by_container invariants; nothing is
			// promised reachable.
			continue
		}

		var raw json.RawMessage
		err := r.session.Call(
			ctx, traversal_serve.MethodNodesList,
			traversal_serve.NodesListParams{URI: entry.URI}, &raw,
		)
		if err != nil {
			problems[fmt.Sprintf("entry[%d]", i)] = fmt.Sprintf(
				"nodes.list %s: %s — an attributed container must be"+
					" listable", entry.URI, err,
			)

			continue
		}

		var result struct {
			Nodes []json.RawMessage `json:"nodes"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			problems[fmt.Sprintf("entry[%d]", i)] = fmt.Sprintf(
				"nodes.list %s result does not decode: %s", entry.URI, err,
			)

			continue
		}

		if len(result.Nodes) < 1 {
			problems[fmt.Sprintf("entry[%d]", i)] = fmt.Sprintf(
				"nodes.list %s returned no children, but the breakdown"+
					" attributes %d match(es) beneath it",
				entry.URI, entry.Count,
			)
		}
	}

	if len(problems) > 0 {
		r.tap.NotOk(nameDescend, problems)

		return
	}

	r.tap.Ok(nameDescend)
}

// caseContainerBody exercises the RFC 0018 §7 / cutting-garden#168
// container-body path over the wire, from the manifest's container_body
// URI: a node that is a container (nodes.list returns children) AND
// carries its own body (leaf.read returns it despite the children —
// §7.1), which resolves through its declared uri_template to a
// body-declaring type (§5, §7.2). A manifest without container_body SKIPs
// both points (a peer need not model this).
func (r *runner) caseContainerBody(ctx context.Context) {
	spec := r.manifest.ContainerBody
	if spec == nil {
		r.tap.Skip(nameContainerBody, "manifest declares no container_body")
		r.tap.Skip(nameURITemplate, "manifest declares no container_body")

		return
	}

	children, listOK := r.listChildURIs(ctx, spec.URI)
	hasBody, readOK := r.readOwnBody(ctx, spec.URI)

	// §7.1: a container WITH children also returns its own body.
	switch {
	case !listOK:
		r.tap.NotOk(nameContainerBody, map[string]string{
			"nodes.list": spec.URI + " did not list as a container",
		})
	case len(children) == 0:
		r.tap.NotOk(nameContainerBody, map[string]string{
			"nodes.list": "no children — container_body must have children" +
				" (else it is an ordinary leaf, not the #168 case)",
		})
	case !readOK:
		r.tap.NotOk(nameContainerBody, map[string]string{
			"leaf.read": "ok=false — a container that declares a body MUST" +
				" return it even with children (RFC 0018 §7.1)",
		})
	case !hasBody:
		r.tap.NotOk(nameContainerBody, map[string]string{
			"leaf.read": "ok=true but neither structured nor raw_base64" +
				" — no body was returned",
		})
	default:
		r.tap.Ok(nameContainerBody)
	}

	r.caseURITemplateResolves(spec.URI, children)
}

// caseURITemplateResolves pins RFC 0018 URI→type resolution over the
// peer's declared templates and emitted URIs: the container's own URI
// resolves (§5.1 emission — its minted URI matches its type's template)
// to a type that declares a body (§7.2, the host read gate's basis), and
// a leaf child does NOT resolve to that same type (§5.2 disjointness). It
// runs the REAL host resolver (ResolveNodeTypeByURI) over the declared
// node types, so it fails a peer whose emitted URIs disagree with its own
// templates.
func (r *runner) caseURITemplateResolves(
	containerURI string, childURIs []string,
) {
	lister := r.declaredLister()

	resolved, ok := cutting_garden_plugins.ResolveNodeTypeByURI(
		lister, containerURI,
	)
	if !ok {
		r.tap.NotOk(nameURITemplate, map[string]string{
			"resolve": containerURI + " resolved to no declared type — a" +
				" templated container must round-trip (RFC 0018 §5.1)",
		})

		return
	}

	if !r.typeDeclaresBody(resolved.Type.Tag) {
		r.tap.NotOk(nameURITemplate, map[string]string{
			"resolve": fmt.Sprintf(
				"%s resolved to %q, which declares no body — the host read"+
					" gate needs the declaration (RFC 0018 §7.2)",
				containerURI, resolved.Type.Tag,
			),
		})

		return
	}

	for _, child := range childURIs {
		childResolved, childOK := cutting_garden_plugins.ResolveNodeTypeByURI(
			lister, child,
		)
		if childOK && childResolved.Type.Tag == resolved.Type.Tag {
			r.tap.NotOk(nameURITemplate, map[string]string{
				"resolve": fmt.Sprintf(
					"child %s wrongly resolved to the container type %q"+
						" (RFC 0018 §5.2 disjointness)",
					child, resolved.Type.Tag,
				),
			})

			return
		}
	}

	r.tap.Ok(nameURITemplate)
}

// listChildURIs issues nodes.list and returns the child URIs; ok is false
// on a call or decode failure.
func (r *runner) listChildURIs(
	ctx context.Context, uri string,
) (uris []string, ok bool) {
	var raw json.RawMessage
	if err := r.session.Call(
		ctx, traversal_serve.MethodNodesList,
		traversal_serve.NodesListParams{URI: uri}, &raw,
	); err != nil {
		return nil, false
	}

	var result struct {
		Nodes []struct {
			URI string `json:"uri"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, false
	}

	uris = make([]string, 0, len(result.Nodes))
	for _, node := range result.Nodes {
		uris = append(uris, node.URI)
	}

	return uris, true
}

// readOwnBody issues leaf.read and reports whether the node returned a
// body (structured or raw) and the leaf.read ok bit — read from the raw
// wire result so an empty structured object is not conflated with an
// absent one.
func (r *runner) readOwnBody(
	ctx context.Context, uri string,
) (hasBody bool, ok bool) {
	var raw json.RawMessage
	if err := r.session.Call(
		ctx, traversal_serve.MethodLeafRead,
		traversal_serve.LeafReadParams{URI: uri}, &raw,
	); err != nil {
		return false, false
	}

	var result struct {
		OK         bool            `json:"ok"`
		Structured json.RawMessage `json:"structured"`
		RawBase64  string          `json:"raw_base64"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return false, false
	}

	hasBody = len(result.Structured) > 0 || result.RawBase64 != ""

	return hasBody, result.OK
}

// declaredLister wraps the peer's initialize node_types (with their
// uri_templates) in a minimal RootLister so the driver can run the real
// host resolver, ResolveNodeTypeByURI, against them.
func (r *runner) declaredLister() declaredTypesLister {
	types := make(
		[]cutting_garden_plugins.NodeType, 0, len(r.init.NodeTypes),
	)
	for _, view := range r.init.NodeTypes {
		types = append(types, view.ToNodeType())
	}

	return declaredTypesLister{types: types}
}

// typeDeclaresBody reports whether the peer's initialize bodies block
// declares a body for tag — the declaration the host read gate consults
// (RFC 0018 §7.2).
func (r *runner) typeDeclaresBody(tag string) bool {
	for _, body := range r.init.Bodies {
		if body.Tag == tag {
			return true
		}
	}

	return false
}

// declaredTypesLister is a RootLister that serves only Types() — the peer's
// declared node types — so ResolveNodeTypeByURI (which reads only Types)
// runs against them. Every other method is an inert stub.
type declaredTypesLister struct {
	types []cutting_garden_plugins.NodeType
}

func (declaredTypesLister) Schemes() []string                     { return nil }
func (declaredTypesLister) TypeTag() string                       { return "" }
func (declaredTypesLister) ValidateSource(*url.URL, string) error { return nil }
func (declaredTypesLister) CaptureRoot(
	cutting_garden_plugins.CaptureRootRequest,
) cutting_garden_plugins.CaptureRootResult {
	return cutting_garden_plugins.CaptureRootResult{}
}

func (l declaredTypesLister) Types() []cutting_garden_plugins.NodeType {
	return l.types
}

func (declaredTypesLister) ListRoots(
	context.Context, *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	return nil, nil
}

// patchRaw issues node.patch with body and returns the raw applied
// value and whether the key was present at all — the presence bit the
// wire struct's pointer encodes and the raw object states directly.
func (r *runner) patchRaw(
	ctx context.Context, probe, body string,
) (appliedRaw json.RawMessage, present bool, err error) {
	var raw json.RawMessage
	err = r.session.Call(
		ctx, traversal_serve.MethodNodePatch,
		traversal_serve.NodePatchParams{
			URI:        probe,
			BodyBase64: encodeBody(body),
		}, &raw,
	)
	if err != nil {
		return nil, false, err
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, false, errors.Wrapf(
			err, "node.patch result is not a JSON object",
		)
	}

	appliedRaw, present = fields["applied"]

	return appliedRaw, present, nil
}

// assertRPCCode emits one test point asserting err is a JSON-RPC error
// response with exactly wantCode. A transport-level failure (no
// *RPCError in the chain) and a plain success both fail the point.
func (r *runner) assertRPCCode(name string, err error, wantCode int) {
	if err == nil {
		r.tap.NotOk(name, map[string]string{
			"error": fmt.Sprintf("call succeeded, want code %d", wantCode),
		})

		return
	}

	code, ok := traversal_serve.CodeOf(err)
	if !ok {
		r.tap.NotOk(name, map[string]string{
			"error": fmt.Sprintf(
				"no JSON-RPC error code in %q, want %d", err, wantCode,
			),
		})

		return
	}

	if code != wantCode {
		r.tap.NotOk(name, map[string]string{
			"code": fmt.Sprintf("%d, want %d", code, wantCode),
		})

		return
	}

	r.tap.Ok(name)
}

// parseFilter parses the manifest's comma-separated dimension=value
// filter grammar (the same surface `list --filter` speaks) into the
// wire predicates.
func parseFilter(text string) ([]traversal_serve.PredicateView, error) {
	if text == "" {
		return nil, nil
	}

	parts := strings.Split(text, ",")
	predicates := make([]traversal_serve.PredicateView, 0, len(parts))

	for _, part := range parts {
		dimension, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found || dimension == "" {
			return nil, errors.BadRequestf(
				"filter %q: want comma-separated dimension=value", text,
			)
		}

		predicates = append(predicates, traversal_serve.PredicateView{
			Dimension: dimension,
			Value:     value,
		})
	}

	return predicates, nil
}

// sameStringSet reports set equality ignoring order (and, per set
// semantics, multiplicity is respected: both sides are sorted and
// compared element-wise, so a duplicated entry on one side mismatches).
func sameStringSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}

	gotSorted := append([]string(nil), got...)
	wantSorted := append([]string(nil), want...)
	sort.Strings(gotSorted)
	sort.Strings(wantSorted)

	for i := range gotSorted {
		if gotSorted[i] != wantSorted[i] {
			return false
		}
	}

	return true
}

// encodeBody projects a manifest body onto the wire's base64 field; the
// empty body stays the absent field (an empty BodyBase64 marshals away
// via omitempty, matching a bodyless mutation).
func encodeBody(body string) string {
	if body == "" {
		return ""
	}

	return base64.StdEncoding.EncodeToString([]byte(body))
}
