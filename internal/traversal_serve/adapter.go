package traversal_serve

// This file is the host-side dispatch core (RFC 0013 §Host
// integration): WirePlugin adapts a lazily-launched session into the
// cutting_garden_plugins capability surface, so `list`, `mcp`, and the
// facet cache render a wire plugin identically to a linked one — the
// RFC's conformance bar.

import (
	"context"
	"encoding/base64"
	"io"
	"net/url"
	"slices"
	"sync"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// WirePlugin adapts a launched session into the full capability
// surface. It implements EVERY optional interface; unadvertised
// capabilities return the contract's decline value (ok=false / empty
// roots / nil declarations) WITHOUT a wire call, so type-assertion
// probing over-matches harmlessly — the decline paths are exactly the
// "plugin omits the interface" fallbacks. Mutations on a plugin that
// did not advertise mutate return an error (writes never silently
// decline).
type WirePlugin struct {
	spec PluginSpec

	// dial launches one session; NewWirePlugin binds it to Launch,
	// tests inject an in-process maker via newWirePluginWithDialer.
	dial func(ctx context.Context) (*Session, error)

	// mu guards the lazy session and the persistent fatal error. It
	// is held across dial, so concurrent first operations spawn exactly
	// one child (later ones find the cached session).
	mu      sync.Mutex
	session *Session

	// fatalErr, once set, fails every subsequent operation fast and is
	// never cleared: a schemes-echo mismatch is a misconfiguration, and
	// a spawn/handshake failure (missing command, the child crashing or
	// exiting before it announces, a bad config section, initialize
	// erroring out) means this plugin is not viable in this process —
	// no amount of respawning fixes either, so a dead plugin degrades to
	// "unavailable" for the rest of the process's life rather than
	// re-dialing (and potentially re-crashing) on every enumeration
	// (cutting-garden#165: a single wire plugin's startup failure used
	// to be fatal to the WHOLE host process — see liveSession).
	fatalErr error
}

var (
	_ cutting_garden_plugins.RootProvider     = (*WirePlugin)(nil)
	_ cutting_garden_plugins.EnrichedLister   = (*WirePlugin)(nil)
	_ cutting_garden_plugins.LeafReader       = (*WirePlugin)(nil)
	_ cutting_garden_plugins.FacetDescriber   = (*WirePlugin)(nil)
	_ cutting_garden_plugins.FacetCounter     = (*WirePlugin)(nil)
	_ cutting_garden_plugins.FacetVersioner   = (*WirePlugin)(nil)
	_ cutting_garden_plugins.FacetLabeler     = (*WirePlugin)(nil)
	_ cutting_garden_plugins.NodeMutator      = (*WirePlugin)(nil)
	_ cutting_garden_plugins.BodyDescriber    = (*WirePlugin)(nil)
	_ cutting_garden_plugins.ContainerCreator = (*WirePlugin)(nil)
)

// NewWirePlugin returns the adapter for spec. It does NOT spawn — the
// session is lazy (the first call that needs the wire launches it), so
// registration stays cheap and offline.
func NewWirePlugin(spec PluginSpec) *WirePlugin {
	return newWirePluginWithDialer(
		spec,
		func(ctx context.Context) (*Session, error) {
			return Launch(ctx, spec.Command, spec.ConfigTOML)
		},
	)
}

// newWirePluginWithDialer is the test seam: NewWirePlugin with the
// session-maker injected, so tests drive the adapter against an
// in-process Serve instead of a subprocess.
func newWirePluginWithDialer(
	spec PluginSpec, dial func(ctx context.Context) (*Session, error),
) *WirePlugin {
	return &WirePlugin{spec: spec, dial: dial}
}

// Close tears down the cached session, if any — the graceful shutdown
// sequence plus child reap of Session.Close. It does not disable the
// adapter: a later operation lazily respawns. Callers that own the
// plugin's lifetime (host shutdown, test teardown) use it to avoid
// leaking the child until process exit.
func (w *WirePlugin) Close() error {
	w.mu.Lock()
	sess := w.session
	w.session = nil
	w.mu.Unlock()

	if sess == nil {
		return nil
	}

	return sess.Close()
}

// liveSession returns the cached session, replacing a dead one (its
// Done channel closed) with at most one fresh launch — the
// respawn-once-per-operation allowance of RFC 0013 §Session lifecycle.
// Replacement happens only here, BEFORE any wire call is issued, so a
// mutation may ride a respawned session but is never retried after its
// call was sent: a call that dies mid-flight surfaces the transport
// error (the host cannot know whether the mutation applied).
//
// A dial failure (missing/bad command, the child crashing or exiting
// before it announces, a bad config section, initialize erroring) is
// recorded as the persistent fatalErr, exactly like a schemes-echo
// mismatch: cutting-garden#165 found this plugin taking the WHOLE host
// process down when its first (eager, enumeration-triggered) spawn
// crashed, because the caller propagated the error instead of isolating
// it. Caching it here means every later operation on this plugin fails
// fast and locally — "this scheme is unavailable" — instead of
// re-dialing (and potentially re-crashing) the plugin on every touch. A
// warning naming the plugin is NOT logged here: liveSession has no
// writer, and every caller either already tolerates a nil/zero decline
// (TypeTag, Types, DescribeFacets, DescribeBodies) or is a fallible
// operation the CALLER logs and degrades around (Roots, in
// AggregateRoots) — logging here too would double-log the same failure
// on the plugin's first touch.
//
// The spawn deliberately uses context.Background(), not the operation
// ctx: Launch's exec.CommandContext ties the child's lifetime to the
// spawn ctx, and the session must outlive the operation that happened
// to trigger it (RFC 0013 §Session lifecycle: the host SHOULD keep the
// session alive for the host process's lifetime). Bring-up is still
// bounded by Launch's internal announce/initialize deadlines.
func (w *WirePlugin) liveSession() (*Session, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.fatalErr != nil {
		return nil, w.fatalErr
	}

	if w.session != nil {
		select {
		case <-w.session.Done():
			// Dead (child exit or stream error): reap it and fall
			// through to the one respawn this operation is allowed.
			_ = w.session.Close()
			w.session = nil
		default:
			return w.session, nil
		}
	}

	sess, err := w.dial(context.Background())
	if err != nil {
		// ErrorWithStackf, not Wrapf: Wrapf's *ErrorTree.Error() returns
		// only the wrapped error's own message (dewey's stack-tree
		// convention — the format string is metadata for a stack-trace
		// print, not part of Error()'s text), so it would silently drop
		// the plugin name and scheme list a caller logging this error
		// needs. ErrorWithStackf's message IS the formatted string, so
		// %s below folds the underlying cause in verbatim.
		w.fatalErr = errors.ErrorWithStackf(
			"wire plugin %q: spawn/initialize failed; scheme(s) %v"+
				" unavailable for the rest of this process: %s",
			w.spec.Name, w.spec.Schemes, err,
		)

		return nil, w.fatalErr
	}

	// Schemes-echo validation (RFC 0013 §Host integration): the
	// initialize echo must cover every scheme the configuration routed
	// here. A miss is recorded as the persistent fatal error so every
	// subsequent operation fails fast instead of respawning a plugin
	// that can never serve the claim.
	for _, scheme := range w.spec.Schemes {
		if !slices.Contains(sess.Init.Schemes, scheme) {
			w.fatalErr = errors.ErrorWithStackf(
				"wire plugin %q: configured scheme %q missing from"+
					" initialize echo %v",
				w.spec.Name, scheme, sess.Init.Schemes,
			)

			_ = sess.Close()

			return nil, w.fatalErr
		}
	}

	w.session = sess

	return sess, nil
}

// liveSessionWithCap ensures a live session and reports whether its
// initialize declaration advertises capability; err covers only
// spawn/config failures, never the capability miss (the caller picks
// its contract's decline value).
func (w *WirePlugin) liveSessionWithCap(
	capability string,
) (*Session, bool, error) {
	sess, err := w.liveSession()
	if err != nil {
		return nil, false, err
	}

	return sess, slices.Contains(sess.Init.Capabilities, capability), nil
}

// call issues one wire method, wrapping any failure — a JSON-RPC error
// response (*RPCError stays extractable via CodeOf / errors.As through
// the wrap) or a transport-death error — with the plugin's name.
// Neither is ever bare io.EOF (the peer formats a clean remote close
// into a fresh error), so wrapping is safe under dewey's EOF hygiene.
func (w *WirePlugin) call(
	ctx context.Context, sess *Session, method string, params, result any,
) error {
	if err := sess.Call(ctx, method, params, result); err != nil {
		return errors.Wrapf(err, "wire plugin %q: %s", w.spec.Name, method)
	}

	return nil
}

// Schemes returns the configured routing claim WITHOUT spawning:
// registration must be cheap and offline, so the answer is the config's
// schemes, not the plugin's. The initialize echo is validated against
// this claim at first spawn — a misconfigured plugin still registers,
// and its first real operation surfaces the failure.
func (w *WirePlugin) Schemes() []string { return w.spec.Schemes }

// TypeTag spawns the session on first need — the wire declaration is
// the only source of the tag. The interface has no error channel, so a
// launch failure here degrades to "": the failure is not lost — the
// next operation with an error return (Roots, ListRoots, ReadLeaf, ...)
// surfaces it, and it is now cached (see liveSession's fatalErr), so
// every such operation reports the SAME failure rather than re-dialing
// a plugin that already proved unviable — but a caller consulting
// TypeTag alone sees an empty tag. That is the tradeoff of
// lazily-spawned identity: the alternative, spawning at registration,
// would make constructing the registry block on every configured
// plugin. No ctx in the interface; liveSession spawns under
// context.Background() with Launch's own deadlines.
func (w *WirePlugin) TypeTag() string {
	sess, err := w.liveSession()
	if err != nil {
		return ""
	}

	return sess.Init.TypeTag
}

// Types answers from the cached initialize declaration, spawning on
// first need. On launch failure it returns nil — an empty declaration —
// for the same no-error-channel reason as TypeTag. A NodeTypeView that
// arrived without mime_type stays empty in the domain NodeType: the
// consumer applies the leaf default via NodeType.BodyMimeType, exactly
// as with a linked plugin (RFC 0013 §Wire encodings).
func (w *WirePlugin) Types() []cutting_garden_plugins.NodeType {
	sess, err := w.liveSession()
	if err != nil {
		return nil
	}

	types := make(
		[]cutting_garden_plugins.NodeType, len(sess.Init.NodeTypes),
	)
	for i, view := range sess.Init.NodeTypes {
		types[i] = view.ToNodeType()
	}

	return types
}

// DescribeFacets answers from the cached initialize declaration,
// spawning on first need. An absent facets block IS the absent
// FacetDescriber capability (RFC 0013 §Handshake), so nil here is the
// same "plugin omits the interface" outcome probing a linked plugin
// yields; a launch failure also degrades to nil (no error channel).
func (w *WirePlugin) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	sess, err := w.liveSession()
	if err != nil || sess.Init.Facets == nil {
		return nil
	}

	declared := make(
		[]cutting_garden_plugins.NodeTypeFacets, len(sess.Init.Facets),
	)
	for i, view := range sess.Init.Facets {
		declared[i] = view.ToNodeTypeFacets()
	}

	return declared
}

// DescribeBodies answers from the cached initialize declaration,
// spawning on first need; an absent bodies block or a launch failure
// degrades to nil, exactly as DescribeFacets.
func (w *WirePlugin) DescribeBodies() []cutting_garden_plugins.NodeTypeBody {
	sess, err := w.liveSession()
	if err != nil || sess.Init.Bodies == nil {
		return nil
	}

	declared := make(
		[]cutting_garden_plugins.NodeTypeBody, len(sess.Init.Bodies),
	)
	for i, view := range sess.Init.Bodies {
		declared[i] = view.ToNodeTypeBody()
	}

	return declared
}

// ListRoots enumerates node's immediate children over the wire —
// nodes.list, the mandatory method every wire plugin serves (no
// capability gate).
func (w *WirePlugin) ListRoots(
	ctx context.Context, node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	sess, err := w.liveSession()
	if err != nil {
		return nil, err
	}

	var result NodesListResult
	err = w.call(
		ctx, sess, MethodNodesList,
		NodesListParams{URI: node.String()}, &result,
	)
	if err != nil {
		return nil, err
	}

	return w.nodesFrom(result.Nodes)
}

// nodesFrom converts wire node views to domain Nodes and enforces the
// credential-free URI invariant on plugin output (RFC 0007/0013
// §Security — the host enforces it rather than trusting the plugin).
// Shared by ListRoots and ListEnriched (cutting-garden#193).
func (w *WirePlugin) nodesFrom(
	views []NodeView,
) ([]cutting_garden_plugins.Node, error) {
	nodes := make([]cutting_garden_plugins.Node, len(views))
	for i, view := range views {
		node, err := view.ToNode()
		if err != nil {
			return nil, errors.Wrapf(err, "wire plugin %q", w.spec.Name)
		}

		if node.URI != nil && node.URI.User != nil {
			return nil, errors.ErrorWithStackf(
				"wire plugin %q: child %q carries userinfo — traversal"+
					" URIs MUST be credential-free",
				w.spec.Name, node.URI.Redacted(),
			)
		}

		nodes[i] = node
	}

	return nodes, nil
}

// ListEnriched is the wire exposure of EnrichedLister (cutting-garden#193,
// #160): it pushes an RFC 0012 §6 filter down to the plugin over
// nodes.list. Gated on CapFilteredList — an unadvertised plugin declines
// (nil, false, nil), so the host's enrichedListing folds host-side
// instead, exactly as when a linked plugin omits EnrichedLister. The
// wire result's ok bit is the plugin's handled report: an explicit false
// (a per-node decline) maps to the same host-side fallback; absent from a
// cap-advertising peer means it returned the narrowed set (handled).
func (w *WirePlugin) ListEnriched(
	ctx context.Context,
	node *url.URL,
	filter cutting_garden_plugins.FacetFilter,
) ([]cutting_garden_plugins.Node, bool, error) {
	sess, advertised, err := w.liveSessionWithCap(CapFilteredList)
	if err != nil {
		return nil, false, err
	}
	if !advertised {
		return nil, false, nil
	}

	var result NodesListResult
	err = w.call(
		ctx, sess, MethodNodesList,
		NodesListParams{
			URI:    node.String(),
			Filter: PredicateViewsFrom(filter),
		},
		&result,
	)
	if err != nil {
		return nil, false, err
	}

	if result.OK != nil && !*result.OK {
		return nil, false, nil
	}

	nodes, err := w.nodesFrom(result.Nodes)
	if err != nil {
		return nil, false, err
	}

	return nodes, true, nil
}

// Roots is gated on CapRoots: unadvertised means (nil, nil) — the
// plugin contributes nothing to root aggregation, with no wire call.
// Advertised roots are checked for the credential-free invariant
// (the RootProvider contract; RFC 0013 §Security obliges the host to
// enforce it on plugin output, not merely trust it).
func (w *WirePlugin) Roots(ctx context.Context) ([]*url.URL, error) {
	sess, advertised, err := w.liveSessionWithCap(CapRoots)
	if err != nil {
		return nil, err
	}

	if !advertised {
		return nil, nil
	}

	var result RootsListResult
	err = w.call(ctx, sess, MethodRootsList, struct{}{}, &result)
	if err != nil {
		return nil, err
	}

	roots := make([]*url.URL, len(result.Roots))
	for i, raw := range result.Roots {
		uri, err := url.Parse(raw)
		if err != nil {
			return nil, errors.Wrapf(
				err, "wire plugin %q: parse root %q", w.spec.Name, raw,
			)
		}

		if uri.User != nil {
			return nil, errors.ErrorWithStackf(
				"wire plugin %q: root %q carries userinfo — roots MUST"+
					" be credential-free",
				w.spec.Name, uri.Redacted(),
			)
		}

		roots[i] = uri
	}

	return roots, nil
}

// ReadLeaf is gated on CapLeafRead: unadvertised means ok=false — the
// not-a-fetchable-leaf decline, so consumers fall back to the child
// listing exactly as when a linked plugin omits LeafReader — with no
// wire call. The wire's ok=false result maps to the same decline.
func (w *WirePlugin) ReadLeaf(
	ctx context.Context, node *url.URL,
) (content cutting_garden_plugins.LeafContent, ok bool, err error) {
	sess, advertised, err := w.liveSessionWithCap(CapLeafRead)
	if err != nil {
		return content, false, err
	}

	if !advertised {
		return content, false, nil
	}

	var result LeafReadResult
	err = w.call(
		ctx, sess, MethodLeafRead, LeafReadParams{URI: node.String()},
		&result,
	)
	if err != nil {
		return content, false, err
	}

	if !result.OK {
		return content, false, nil
	}

	if len(result.Structured) > 0 {
		// json.RawMessage is itself JSON-marshalable, so the plugin's
		// structured bytes reach the consumer unreinterpreted.
		content.Structured = result.Structured
	}

	if result.RawBase64 != "" {
		raw, err := base64.StdEncoding.DecodeString(result.RawBase64)
		if err != nil {
			return cutting_garden_plugins.LeafContent{}, false,
				errors.Wrapf(
					err, "wire plugin %q: leaf raw_base64", w.spec.Name,
				)
		}

		content.Raw = raw
		content.RawMimeType = result.RawMimeType
	}

	return content, true, nil
}

// FacetCounts is gated on CapFacetCounts: unadvertised means ok=false —
// "fall back to the framework fold" (RFC 0012 §4), the same outcome as
// a linked plugin omitting FacetCounter — with no wire call.
func (w *WirePlugin) FacetCounts(
	ctx context.Context, node *url.URL,
	filter cutting_garden_plugins.FacetFilter,
) (result cutting_garden_plugins.FacetResult, ok bool, err error) {
	sess, advertised, err := w.liveSessionWithCap(CapFacetCounts)
	if err != nil {
		return result, false, err
	}

	if !advertised {
		return result, false, nil
	}

	var wireResult FacetCountsResult
	err = w.call(
		ctx, sess, MethodFacetCounts,
		FacetCountsParams{
			URI:    node.String(),
			Filter: PredicateViewsFrom(filter),
		},
		&wireResult,
	)
	if err != nil {
		return result, false, err
	}

	if !wireResult.OK {
		return result, false, nil
	}

	// An OK summary is non-nil by the linked convention: a summarizable
	// node returns a possibly-EMPTY FacetSummary (caldav does exactly this
	// for a task-free calendar). The wire drops an empty map via omitempty
	// (FacetCountsResult.Summary), so it decodes back to nil here;
	// normalize it to an empty non-nil map so a wire plugin is
	// indistinguishable from a linked one for an empty-but-OK summary
	// (cutting-garden#192). Absent normalization, a consumer's
	// nil-vs-empty check — or the indistinguishability e2e's DeepEqual —
	// would tell the two apart.
	result.Summary = wireResult.Summary
	if result.Summary == nil {
		result.Summary = cutting_garden_plugins.FacetSummary{}
	}
	result.Complete = wireResult.Complete

	// The RFC 0012 §13 invariants (only Count > 0 entries; capped at
	// FacetContainerBreakdownLimit) are the PLUGIN's obligations, but the
	// host does not trust a peer to have honored them: a non-conformant
	// breakdown is normalized here with the same shared helper a linked
	// plugin uses, so a consumer cannot tell the difference
	// (cutting-garden#173). Truncation imposed here is reported as
	// truncation — OR-ed with the peer's own flag, never replacing it.
	breakdown := ToFacetContainerBreakdowns(wireResult.ByContainer)
	if breakdown != nil {
		nonEmpty := breakdown[:0]
		for _, entry := range breakdown {
			if entry.Count > 0 {
				nonEmpty = append(nonEmpty, entry)
			}
		}
		limited, truncated := cutting_garden_plugins.
			SortAndLimitContainerBreakdown(nonEmpty)
		result.ByContainer = limited
		result.ByContainerTruncated = wireResult.ByContainerTruncated ||
			truncated
	}

	return result, true, nil
}

// FacetVersion is gated on CapFacetVersion: unadvertised means
// ok=false — no token, the framework's cache falls back to its TTL —
// with no wire call.
func (w *WirePlugin) FacetVersion(
	ctx context.Context, node *url.URL,
) (token string, ok bool, err error) {
	sess, advertised, err := w.liveSessionWithCap(CapFacetVersion)
	if err != nil {
		return "", false, err
	}

	if !advertised {
		return "", false, nil
	}

	var result FacetVersionResult
	err = w.call(
		ctx, sess, MethodFacetVersion,
		FacetVersionParams{URI: node.String()}, &result,
	)
	if err != nil {
		return "", false, err
	}

	if !result.OK {
		return "", false, nil
	}

	return result.Token, true, nil
}

// ResolveFacetLabels is gated on CapFacetLabels: unadvertised means
// (nil, nil) — labels absent, consumers fall back to the keys; the
// whole method is presentation-only and non-fatal (RFC 0012 §7) — with
// no wire call.
func (w *WirePlugin) ResolveFacetLabels(
	ctx context.Context, dimension string, keys []string,
) (map[string]string, error) {
	sess, advertised, err := w.liveSessionWithCap(CapFacetLabels)
	if err != nil {
		return nil, err
	}

	if !advertised {
		return nil, nil
	}

	var result LabelsResolveResult
	err = w.call(
		ctx, sess, MethodLabelsResolve,
		LabelsResolveParams{Dimension: dimension, Keys: keys}, &result,
	)
	if err != nil {
		return nil, err
	}

	return result.Labels, nil
}

// mutationSession ensures a live session and requires CapMutate: a
// write on a plugin that did not advertise mutate is a REAL error,
// never a silent decline, and nothing is sent on the wire. The respawn
// allowance was consumed inside liveSession — before the wire call —
// so no mutation is ever retried after being issued (RFC 0013 §Session
// lifecycle).
func (w *WirePlugin) mutationSession() (*Session, error) {
	sess, advertised, err := w.liveSessionWithCap(CapMutate)
	if err != nil {
		return nil, err
	}

	if !advertised {
		return nil, errors.ErrorWithStackf(
			"wire plugin %q does not advertise the %q capability;"+
				" refusing the mutation",
			w.spec.Name, CapMutate,
		)
	}

	return sess, nil
}

// encodeBody drains a mutation body into the wire's base64 form; a nil
// or empty body encodes as the absent field.
func encodeBody(body io.Reader) (string, error) {
	if body == nil {
		return "", nil
	}

	data, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}

	if len(data) == 0 {
		return "", nil
	}

	return base64.StdEncoding.EncodeToString(data), nil
}

// CreateNode issues node.create — strict create, no upsert (the
// NodeMutator contract; the plugin errors on an existing URI).
func (w *WirePlugin) CreateNode(
	ctx context.Context, uri *url.URL, body io.Reader, typ string,
) error {
	sess, err := w.mutationSession()
	if err != nil {
		return err
	}

	encoded, err := encodeBody(body)
	if err != nil {
		return errors.Wrapf(
			err, "wire plugin %q: read create body", w.spec.Name,
		)
	}

	return w.call(
		ctx, sess, MethodNodeCreate,
		NodeCreateParams{
			URI:        uri.String(),
			Type:       typ,
			BodyBase64: encoded,
		},
		nil,
	)
}

// CreateChild issues node.create_child (ContainerCreator,
// cutting-garden#143): create under container with the source assigning
// the created node's identity, reported back as the result URI. Writes
// never silently decline: an unadvertised capability is an error, and
// the returned URI is held to the same credential-free invariant as
// every other plugin-emitted URI.
func (w *WirePlugin) CreateChild(
	ctx context.Context, container *url.URL, body io.Reader, typ string,
) (*url.URL, error) {
	sess, advertised, err := w.liveSessionWithCap(CapContainerCreate)
	if err != nil {
		return nil, err
	}
	if !advertised {
		return nil, errors.ErrorWithStackf(
			"wire plugin %q does not advertise the %q capability;"+
				" refusing the mutation",
			w.spec.Name, CapContainerCreate,
		)
	}

	encoded, err := encodeBody(body)
	if err != nil {
		return nil, errors.Wrapf(
			err, "wire plugin %q: read create_child body", w.spec.Name,
		)
	}

	var result NodeCreateChildResult
	if err := w.call(
		ctx, sess, MethodNodeCreateChild,
		NodeCreateChildParams{
			Container:  container.String(),
			Type:       typ,
			BodyBase64: encoded,
		},
		&result,
	); err != nil {
		return nil, err
	}

	created, err := url.Parse(result.Created)
	if err != nil || result.Created == "" {
		return nil, errors.ErrorWithStackf(
			"wire plugin %q: create_child returned an unusable URI %q",
			w.spec.Name, result.Created,
		)
	}
	if created.User != nil {
		return nil, errors.ErrorWithStackf(
			"wire plugin %q: created %q carries userinfo — URIs MUST be"+
				" credential-free",
			w.spec.Name, created.Redacted(),
		)
	}

	return created, nil
}

// PutNode issues node.put — full-replace of an existing leaf's body.
func (w *WirePlugin) PutNode(
	ctx context.Context, uri *url.URL, body io.Reader,
) error {
	sess, err := w.mutationSession()
	if err != nil {
		return err
	}

	encoded, err := encodeBody(body)
	if err != nil {
		return errors.Wrapf(
			err, "wire plugin %q: read put body", w.spec.Name,
		)
	}

	return w.call(
		ctx, sess, MethodNodePut,
		NodePutParams{URI: uri.String(), BodyBase64: encoded}, nil,
	)
}

// PatchNode issues node.patch — a partial-field update. The empty-body
// bad-request rule is enforced wire-side (the plugin answers
// CodeInvalidParams), so the adapter passes the body through untouched.
//
// A peer that omits the result's applied key does not report applied
// fields; that absence is forwarded as a nil applied rather than an empty
// one, so a linked consumer cannot mistake it for "nothing was applied"
// (cutting-garden#182).
func (w *WirePlugin) PatchNode(
	ctx context.Context, uri *url.URL, body io.Reader,
) ([]string, error) {
	sess, err := w.mutationSession()
	if err != nil {
		return nil, err
	}

	encoded, err := encodeBody(body)
	if err != nil {
		return nil, errors.Wrapf(
			err, "wire plugin %q: read patch body", w.spec.Name,
		)
	}

	var result NodePatchResult
	if err := w.call(
		ctx, sess, MethodNodePatch,
		NodePatchParams{URI: uri.String(), BodyBase64: encoded},
		&result,
	); err != nil {
		return nil, err
	}

	// A nil pointer covers BOTH shapes that mean "does not report": the key
	// omitted, and an explicit JSON null (which encoding/json resolves to a
	// nil pointer, not a pointer-to-nil-slice — pinned by
	// TestNodePatchResultDecodeStates). Anything else is a real list the
	// peer sent, empty or not, and is returned as-is.
	if result.Applied == nil {
		return nil, nil
	}

	return *result.Applied, nil
}

// DeleteNode issues node.delete.
func (w *WirePlugin) DeleteNode(ctx context.Context, uri *url.URL) error {
	sess, err := w.mutationSession()
	if err != nil {
		return err
	}

	return w.call(
		ctx, sess, MethodNodeDelete,
		NodeDeleteParams{URI: uri.String()}, nil,
	)
}
