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

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"

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

	// mu guards the lazy session and the persistent config error. It
	// is held across dial, so concurrent first operations spawn exactly
	// one child (later ones find the cached session).
	mu      sync.Mutex
	session *Session

	// configErr, once set, fails every subsequent operation fast: a
	// schemes-echo mismatch is a misconfiguration, not a transient
	// fault — no amount of respawning can make the plugin serve a
	// scheme it does not speak.
	configErr error
}

var (
	_ cutting_garden_plugins.RootProvider     = (*WirePlugin)(nil)
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
// The spawn deliberately uses context.Background(), not the operation
// ctx: Launch's exec.CommandContext ties the child's lifetime to the
// spawn ctx, and the session must outlive the operation that happened
// to trigger it (RFC 0013 §Session lifecycle: the host SHOULD keep the
// session alive for the host process's lifetime). Bring-up is still
// bounded by Launch's internal announce/initialize deadlines.
func (w *WirePlugin) liveSession() (*Session, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.configErr != nil {
		return nil, w.configErr
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
		return nil, errors.Wrapf(err, "wire plugin %q", w.spec.Name)
	}

	// Schemes-echo validation (RFC 0013 §Host integration): the
	// initialize echo must cover every scheme the configuration routed
	// here. A miss is recorded as the persistent config error so every
	// subsequent operation fails fast instead of respawning a plugin
	// that can never serve the claim.
	for _, scheme := range w.spec.Schemes {
		if !slices.Contains(sess.Init.Schemes, scheme) {
			w.configErr = errors.ErrorWithStackf(
				"wire plugin %q: configured scheme %q missing from"+
					" initialize echo %v",
				w.spec.Name, scheme, sess.Init.Schemes,
			)

			_ = sess.Close()

			return nil, w.configErr
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
// next operation with an error return re-attempts the launch and
// surfaces it — but a caller consulting TypeTag alone sees an empty
// tag. That is the tradeoff of lazily-spawned identity: the
// alternative, spawning at registration, would make constructing the
// registry block on every configured plugin. No ctx in the interface;
// liveSession spawns under context.Background() with Launch's own
// deadlines.
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

	nodes := make([]cutting_garden_plugins.Node, len(result.Nodes))
	for i, view := range result.Nodes {
		if nodes[i], err = view.ToNode(); err != nil {
			return nil, errors.Wrapf(err, "wire plugin %q", w.spec.Name)
		}

		// RFC 0007/0013 §Security: every traversal-emitted URI is
		// credential-free, and the host enforces it on plugin output
		// rather than trusting it — same check Roots applies.
		if nodes[i].URI != nil && nodes[i].URI.User != nil {
			return nil, errors.ErrorWithStackf(
				"wire plugin %q: child %q carries userinfo — traversal"+
					" URIs MUST be credential-free",
				w.spec.Name, nodes[i].URI.Redacted(),
			)
		}
	}

	return nodes, nil
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

	result.Summary = wireResult.Summary
	result.Complete = wireResult.Complete

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
func (w *WirePlugin) PatchNode(
	ctx context.Context, uri *url.URL, body io.Reader,
) error {
	sess, err := w.mutationSession()
	if err != nil {
		return err
	}

	encoded, err := encodeBody(body)
	if err != nil {
		return errors.Wrapf(
			err, "wire plugin %q: read patch body", w.spec.Name,
		)
	}

	return w.call(
		ctx, sess, MethodNodePatch,
		NodePatchParams{URI: uri.String(), BodyBase64: encoded}, nil,
	)
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
