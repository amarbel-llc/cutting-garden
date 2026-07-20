package traversal_serve

// This file is the plugin side of an RFC 0013 session: the Serve a Go
// wire plugin's `traversal-serve` subcommand calls (and the test peer's
// core). Serve derives the initialize declaration from the plugin value
// itself by capability type assertion — the wire capabilities ARE the
// Go SDK's narrow interfaces (RFC 0013 §Method set) — then dispatches
// the method set sequentially over a Peer, mapping each method onto the
// corresponding in-process contract and preserving the ok=false-is-a-
// RESULT distinction (§Errors).

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"slices"
	"sync"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// ServeConfig configures Serve, the plugin side of an RFC 0013 session.
type ServeConfig struct {
	// Plugin is the served backend. REQUIRED, and it MUST implement
	// RootLister — nodes.list is mandatory (RFC 0013 §Method set: a
	// plugin with nothing to enumerate does not belong on this
	// transport). Every optional capability is probed by type assertion
	// and advertised as the corresponding token.
	Plugin cutting_garden_plugins.Plugin
	// Info names the plugin binary in the initialize response
	// (diagnostic only, RFC 0013 §Handshake).
	Info PluginInfo
	// ConfigApply, when non-nil, receives InitializeParams.ConfigTOML
	// before the initialize response; an error fails initialize with
	// CodeInvalidConfig (RFC 0013 §Handshake).
	ConfigApply func(configTOML string) error
}

// Serve runs one RFC 0013 session over rw. It builds the initialize
// declaration once from cfg.Plugin, then serves the §Method set until
// the session ends. Returns nil for a graceful end — the shutdown
// notification (after the request queue drains) or stream EOF after
// shutdown — and the terminal reason otherwise: ctx cancellation, EOF
// before shutdown (host death is cancellation, §Session lifecycle), or
// a stream error.
func Serve(
	ctx context.Context, rw io.ReadWriteCloser, cfg ServeConfig,
) error {
	srv, err := newServer(cfg)
	if err != nil {
		return err
	}

	peer := NewPeer(rw, WithHandler(srv))

	select {
	case <-ctx.Done():
		_ = peer.Close()
		<-peer.ServeDone()
		return errors.Wrapf(ctx.Err(), "traversal serve")

	case <-srv.shutdownCh:
		// The serve loop is sequential, so by the time the shutdown
		// notification's handler ran, every request pipelined before it
		// has been answered — the drain RFC 0013 §Session lifecycle
		// asks for.
		_ = peer.Close()
		<-peer.ServeDone()
		return nil

	case <-peer.Done():
		<-peer.ServeDone()

		select {
		case <-srv.shutdownCh:
			// EOF after shutdown: the host's stdin-close half of the
			// graceful sequence (RFC 0013 §Session lifecycle).
			return nil
		default:
		}

		// Identity comparison, not errors.Is: dewey's errors.Is PANICS
		// on an io.EOF target (its EOF-hygiene guard), and the peer's
		// read loop records clean end-of-stream as bare io.EOF.
		terminal := peer.Err()
		if terminal == io.EOF {
			// EOF before shutdown is host-side cancellation (RFC 0013
			// §Session lifecycle), surfaced as an error. Formatted with
			// %s because dewey's errors refuses to wrap bare io.EOF.
			return errors.ErrorWithStackf(
				"traversal serve: stream closed before shutdown: %s",
				terminal,
			)
		}

		return terminal
	}
}

// server is the Handler behind Serve: the capability surface probed
// from one plugin, the prebuilt initialize declaration, and the
// session's handshake and shutdown state. Handle runs on the peer's
// sequential serve loop, so the un-mutexed initialized flag is safe —
// only that one goroutine touches it.
type server struct {
	lister cutting_garden_plugins.RootLister

	// Optional capabilities; nil when the plugin does not implement
	// the interface, in which case the corresponding method fails with
	// CodeMethodNotFound (RFC 0013 §Method set: the host MUST NOT call
	// an unadvertised method).
	provider  cutting_garden_plugins.RootProvider
	leaves    cutting_garden_plugins.LeafReader
	counter   cutting_garden_plugins.FacetCounter
	versioner cutting_garden_plugins.FacetVersioner
	labeler   cutting_garden_plugins.FacetLabeler
	mutator   cutting_garden_plugins.NodeMutator
	creator   cutting_garden_plugins.ContainerCreator

	// schemes indexes the plugin's advertised URI schemes for the
	// nodes.list scheme gate (RFC 0013 §Traversal).
	schemes map[string]struct{}

	// init is the declaration built once at construction — all three
	// blocks are stable for the plugin's lifetime (RFC 0013 §Handshake).
	init InitializeResult

	configApply func(string) error

	// initialized flips once initialize succeeds; requests before that
	// are rejected (see Handle).
	initialized bool

	shutdownOnce sync.Once
	// shutdownCh closes when the shutdown notification arrives; Serve
	// selects on it.
	shutdownCh chan struct{}
}

// newServer probes cfg.Plugin's capabilities and prebuilds the
// initialize declaration. A plugin without RootLister is refused here,
// before any byte is served.
func newServer(cfg ServeConfig) (*server, error) {
	if cfg.Plugin == nil {
		return nil, errors.ErrorWithStackf(
			"traversal serve: ServeConfig.Plugin is required",
		)
	}

	lister, ok := cfg.Plugin.(cutting_garden_plugins.RootLister)
	if !ok {
		return nil, errors.ErrorWithStackf(
			"traversal serve: plugin %q does not implement RootLister"+
				" — nodes.list is mandatory (RFC 0013 §Method set)",
			cfg.Info.Name,
		)
	}

	srv := &server{
		lister:      lister,
		configApply: cfg.ConfigApply,
		shutdownCh:  make(chan struct{}),
	}

	schemes := cfg.Plugin.Schemes()
	srv.schemes = make(map[string]struct{}, len(schemes))
	for _, scheme := range schemes {
		srv.schemes[scheme] = struct{}{}
	}

	srv.init = InitializeResult{
		Schema:  SchemaV1,
		Plugin:  cfg.Info,
		Schemes: schemes,
		TypeTag: cfg.Plugin.TypeTag(),
	}

	nodeTypes := lister.Types()
	srv.init.NodeTypes = make([]NodeTypeView, len(nodeTypes))
	for i, nodeType := range nodeTypes {
		srv.init.NodeTypes[i] = NodeTypeViewFrom(nodeType)
	}

	capabilities := []string{}

	if provider, ok := cfg.Plugin.(cutting_garden_plugins.RootProvider); ok {
		srv.provider = provider
		capabilities = append(capabilities, CapRoots)
	}

	if leaves, ok := cfg.Plugin.(cutting_garden_plugins.LeafReader); ok {
		srv.leaves = leaves
		capabilities = append(capabilities, CapLeafRead)
	}

	if counter, ok := cfg.Plugin.(cutting_garden_plugins.FacetCounter); ok {
		srv.counter = counter
		capabilities = append(capabilities, CapFacetCounts)
	}

	if versioner, ok := cfg.Plugin.(cutting_garden_plugins.FacetVersioner); ok {
		srv.versioner = versioner
		capabilities = append(capabilities, CapFacetVersion)
	}

	if labeler, ok := cfg.Plugin.(cutting_garden_plugins.FacetLabeler); ok {
		srv.labeler = labeler
		capabilities = append(capabilities, CapFacetLabels)
	}

	if mutator, ok := cfg.Plugin.(cutting_garden_plugins.NodeMutator); ok {
		srv.mutator = mutator
		capabilities = append(capabilities, CapMutate)
	}

	if creator, ok := cfg.Plugin.(cutting_garden_plugins.ContainerCreator); ok {
		srv.creator = creator
		capabilities = append(capabilities, CapContainerCreate)
	}

	srv.init.Capabilities = capabilities

	if describer, ok := cfg.Plugin.(cutting_garden_plugins.FacetDescriber); ok {
		declared := describer.DescribeFacets()
		srv.init.Facets = make([]NodeTypeFacetsView, len(declared))
		for i, facets := range declared {
			srv.init.Facets[i] = NodeTypeFacetsViewFrom(facets)
		}
	}

	if describer, ok := cfg.Plugin.(cutting_garden_plugins.BodyDescriber); ok {
		declared := describer.DescribeBodies()
		srv.init.Bodies = make([]NodeTypeBodyView, len(declared))
		for i, body := range declared {
			srv.init.Bodies[i] = NodeTypeBodyViewFrom(body)
		}
	}

	return srv, nil
}

// Handle dispatches one method to the corresponding capability. Domain
// errors (non-*RPCError) propagate as-is — the peer maps them to -32603
// internal (RFC 0013 §Errors); where the RFC pins a specific code the
// handlers below wrap in *RPCError themselves.
func (s *server) Handle(
	ctx context.Context, method string, params json.RawMessage,
) (any, error) {
	switch method {
	case MethodInitialize:
		return s.handleInitialize(params)

	case MethodShutdown:
		// Notification: mark and let Serve unwind; the peer discards
		// the return values (there is no response to carry them).
		s.shutdownOnce.Do(func() { close(s.shutdownCh) })
		return nil, nil
	}

	// RFC 0013 §Handshake obliges the HOST to initialize first and
	// await the response; rejecting a premature request here is
	// defensive — it keeps a non-conformant host from driving an
	// unconfigured plugin.
	if !s.initialized {
		return nil, &RPCError{
			Code:    CodeInvalidParams,
			Message: "initialize required",
		}
	}

	switch method {
	case MethodNodesList:
		return s.handleNodesList(ctx, params)

	case MethodRootsList:
		if s.provider == nil {
			return nil, methodNotAdvertised(method)
		}
		return s.handleRootsList(ctx)

	case MethodLeafRead:
		if s.leaves == nil {
			return nil, methodNotAdvertised(method)
		}
		return s.handleLeafRead(ctx, params)

	case MethodFacetCounts:
		if s.counter == nil {
			return nil, methodNotAdvertised(method)
		}
		return s.handleFacetCounts(ctx, params)

	case MethodFacetVersion:
		if s.versioner == nil {
			return nil, methodNotAdvertised(method)
		}
		return s.handleFacetVersion(ctx, params)

	case MethodLabelsResolve:
		if s.labeler == nil {
			return nil, methodNotAdvertised(method)
		}
		return s.handleLabelsResolve(ctx, params)

	case MethodNodeCreate:
		if s.mutator == nil {
			return nil, methodNotAdvertised(method)
		}
		return s.handleNodeCreate(ctx, params)

	case MethodNodeCreateChild:
		if s.creator == nil {
			return nil, methodNotAdvertised(method)
		}
		return s.handleNodeCreateChild(ctx, params)

	case MethodNodePut:
		if s.mutator == nil {
			return nil, methodNotAdvertised(method)
		}
		return s.handleNodePut(ctx, params)

	case MethodNodePatch:
		if s.mutator == nil {
			return nil, methodNotAdvertised(method)
		}
		return s.handleNodePatch(ctx, params)

	case MethodNodeDelete:
		if s.mutator == nil {
			return nil, methodNotAdvertised(method)
		}
		return s.handleNodeDelete(ctx, params)

	default:
		return nil, &RPCError{
			Code:    CodeMethodNotFound,
			Message: fmt.Sprintf("method %q not found", method),
		}
	}
}

func (s *server) handleInitialize(params json.RawMessage) (any, error) {
	var initParams InitializeParams
	if rpcErr := unmarshalParams(params, &initParams); rpcErr != nil {
		return nil, rpcErr
	}

	if !slices.Contains(initParams.ProtocolVersions, SchemaV1) {
		return nil, &RPCError{
			Code: CodeUnsupportedVersion,
			Message: fmt.Sprintf(
				"none of %v is supported (plugin speaks %s)",
				initParams.ProtocolVersions, SchemaV1,
			),
		}
	}

	if s.configApply != nil {
		if err := s.configApply(initParams.ConfigTOML); err != nil {
			return nil, &RPCError{
				Code:    CodeInvalidConfig,
				Message: err.Error(),
			}
		}
	}

	s.initialized = true

	return s.init, nil
}

func (s *server) handleNodesList(
	ctx context.Context, params json.RawMessage,
) (any, error) {
	var listParams NodesListParams
	if rpcErr := unmarshalParams(params, &listParams); rpcErr != nil {
		return nil, rpcErr
	}

	uri, rpcErr := parseURIParam(listParams.URI)
	if rpcErr != nil {
		return nil, rpcErr
	}

	// The one scheme-gated method (RFC 0013 §Traversal): a URI whose
	// scheme the plugin did not advertise is a host dispatch bug.
	if _, ok := s.schemes[uri.Scheme]; !ok {
		return nil, &RPCError{
			Code: CodeInvalidParams,
			Message: fmt.Sprintf(
				"scheme %q not served (schemes: %v)",
				uri.Scheme, s.init.Schemes,
			),
		}
	}

	nodes, err := s.lister.ListRoots(ctx, uri)
	if err != nil {
		return nil, err
	}

	// A non-nil (possibly empty) slice: a leaf or empty container
	// answers { "nodes": [] }, never null (RFC 0013 §Traversal).
	result := NodesListResult{Nodes: make([]NodeView, len(nodes))}
	for i, node := range nodes {
		result.Nodes[i] = NodeViewFrom(node)
	}

	return result, nil
}

func (s *server) handleRootsList(ctx context.Context) (any, error) {
	roots, err := s.provider.Roots(ctx)
	if err != nil {
		return nil, err
	}

	result := RootsListResult{Roots: make([]string, len(roots))}
	for i, root := range roots {
		result.Roots[i] = root.String()
	}

	return result, nil
}

func (s *server) handleLeafRead(
	ctx context.Context, params json.RawMessage,
) (any, error) {
	var readParams LeafReadParams
	if rpcErr := unmarshalParams(params, &readParams); rpcErr != nil {
		return nil, rpcErr
	}

	uri, rpcErr := parseURIParam(readParams.URI)
	if rpcErr != nil {
		return nil, rpcErr
	}

	content, ok, err := s.leaves.ReadLeaf(ctx, uri)
	if err != nil {
		return nil, err
	}

	if !ok {
		// Not a fetchable leaf: a RESULT, never an error (RFC 0013
		// §Leaf content).
		return LeafReadResult{}, nil
	}

	result := LeafReadResult{OK: true}

	if content.Structured != nil {
		structured, err := json.Marshal(content.Structured)
		if err != nil {
			return nil, errors.Wrapf(err, "marshal structured leaf content")
		}

		result.Structured = structured
	}

	if len(content.Raw) > 0 {
		result.RawBase64 = base64.StdEncoding.EncodeToString(content.Raw)
		result.RawMimeType = content.RawMimeType
	}

	return result, nil
}

func (s *server) handleFacetCounts(
	ctx context.Context, params json.RawMessage,
) (any, error) {
	var countParams FacetCountsParams
	if rpcErr := unmarshalParams(params, &countParams); rpcErr != nil {
		return nil, rpcErr
	}

	uri, rpcErr := parseURIParam(countParams.URI)
	if rpcErr != nil {
		return nil, rpcErr
	}

	facetResult, ok, err := s.counter.FacetCounts(
		ctx, uri, FacetFilterFrom(countParams.Filter),
	)
	if err != nil {
		return nil, err
	}

	if !ok {
		// "I do not summarize this node" — a RESULT selecting the
		// framework-fold fallback host-side (RFC 0013 §Facets).
		return FacetCountsResult{}, nil
	}

	return FacetCountsResult{
		OK:       true,
		Summary:  facetResult.Summary,
		Complete: facetResult.Complete,
	}, nil
}

func (s *server) handleFacetVersion(
	ctx context.Context, params json.RawMessage,
) (any, error) {
	var versionParams FacetVersionParams
	if rpcErr := unmarshalParams(params, &versionParams); rpcErr != nil {
		return nil, rpcErr
	}

	uri, rpcErr := parseURIParam(versionParams.URI)
	if rpcErr != nil {
		return nil, rpcErr
	}

	token, ok, err := s.versioner.FacetVersion(ctx, uri)
	if err != nil {
		return nil, err
	}

	if !ok {
		return FacetVersionResult{}, nil
	}

	return FacetVersionResult{OK: true, Token: token}, nil
}

func (s *server) handleLabelsResolve(
	ctx context.Context, params json.RawMessage,
) (any, error) {
	var labelParams LabelsResolveParams
	if rpcErr := unmarshalParams(params, &labelParams); rpcErr != nil {
		return nil, rpcErr
	}

	labels, err := s.labeler.ResolveFacetLabels(
		ctx, labelParams.Dimension, labelParams.Keys,
	)
	if err != nil {
		return nil, err
	}

	if labels == nil {
		labels = map[string]string{}
	}

	return LabelsResolveResult{Labels: labels}, nil
}

func (s *server) handleNodeCreate(
	ctx context.Context, params json.RawMessage,
) (any, error) {
	var createParams NodeCreateParams
	if rpcErr := unmarshalParams(params, &createParams); rpcErr != nil {
		return nil, rpcErr
	}

	uri, rpcErr := parseURIParam(createParams.URI)
	if rpcErr != nil {
		return nil, rpcErr
	}

	// An absent body decodes to the empty reader — a bodyless container
	// create is valid (FDR 0020).
	body, rpcErr := decodeBodyBase64(createParams.BodyBase64)
	if rpcErr != nil {
		return nil, rpcErr
	}

	err := s.mutator.CreateNode(
		ctx, uri, bytes.NewReader(body), createParams.Type,
	)
	if err != nil {
		return nil, err
	}

	return struct{}{}, nil
}

func (s *server) handleNodeCreateChild(
	ctx context.Context, params json.RawMessage,
) (any, error) {
	var createParams NodeCreateChildParams
	if rpcErr := unmarshalParams(params, &createParams); rpcErr != nil {
		return nil, rpcErr
	}

	container, rpcErr := parseURIParam(createParams.Container)
	if rpcErr != nil {
		return nil, rpcErr
	}

	body, rpcErr := decodeBodyBase64(createParams.BodyBase64)
	if rpcErr != nil {
		return nil, rpcErr
	}

	created, err := s.creator.CreateChild(
		ctx, container, bytes.NewReader(body), createParams.Type,
	)
	if err != nil {
		return nil, err
	}
	if created == nil {
		return nil, errors.ErrorWithStackf(
			"CreateChild returned no created URI",
		)
	}

	return NodeCreateChildResult{Created: created.String()}, nil
}

func (s *server) handleNodePut(
	ctx context.Context, params json.RawMessage,
) (any, error) {
	var putParams NodePutParams
	if rpcErr := unmarshalParams(params, &putParams); rpcErr != nil {
		return nil, rpcErr
	}

	uri, rpcErr := parseURIParam(putParams.URI)
	if rpcErr != nil {
		return nil, rpcErr
	}

	body, rpcErr := decodeBodyBase64(putParams.BodyBase64)
	if rpcErr != nil {
		return nil, rpcErr
	}

	if err := s.mutator.PutNode(ctx, uri, bytes.NewReader(body)); err != nil {
		return nil, err
	}

	return struct{}{}, nil
}

func (s *server) handleNodePatch(
	ctx context.Context, params json.RawMessage,
) (any, error) {
	var patchParams NodePatchParams
	if rpcErr := unmarshalParams(params, &patchParams); rpcErr != nil {
		return nil, rpcErr
	}

	uri, rpcErr := parseURIParam(patchParams.URI)
	if rpcErr != nil {
		return nil, rpcErr
	}

	body, rpcErr := decodeBodyBase64(patchParams.BodyBase64)
	if rpcErr != nil {
		return nil, rpcErr
	}

	// An empty patch body is a bad request by the NodeMutator contract
	// ("only touch what is explicitly named in the body" — an empty body
	// names nothing), enforced here uniformly for every wire plugin.
	if len(body) == 0 {
		return nil, &RPCError{
			Code:    CodeInvalidParams,
			Message: "node.patch requires a non-empty body",
		}
	}

	if err := s.mutator.PatchNode(ctx, uri, bytes.NewReader(body)); err != nil {
		return nil, err
	}

	return struct{}{}, nil
}

func (s *server) handleNodeDelete(
	ctx context.Context, params json.RawMessage,
) (any, error) {
	var deleteParams NodeDeleteParams
	if rpcErr := unmarshalParams(params, &deleteParams); rpcErr != nil {
		return nil, rpcErr
	}

	uri, rpcErr := parseURIParam(deleteParams.URI)
	if rpcErr != nil {
		return nil, rpcErr
	}

	if err := s.mutator.DeleteNode(ctx, uri); err != nil {
		return nil, err
	}

	return struct{}{}, nil
}

// methodNotAdvertised is the RFC 0013 §Method set rejection for a
// method whose capability token the plugin did not advertise.
func methodNotAdvertised(method string) *RPCError {
	return &RPCError{
		Code: CodeMethodNotFound,
		Message: fmt.Sprintf(
			"method %q not advertised by this plugin", method,
		),
	}
}

// unmarshalParams decodes a request's params into into; absent params
// leave the zero value. A malformed payload is the caller's fault:
// CodeInvalidParams.
func unmarshalParams(params json.RawMessage, into any) *RPCError {
	if len(params) == 0 {
		return nil
	}

	if err := json.Unmarshal(params, into); err != nil {
		return &RPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("params: %s", err),
		}
	}

	return nil
}

// parseURIParam validates and parses a method's uri param: non-empty
// and RFC 3986-parseable, else CodeInvalidParams.
func parseURIParam(raw string) (*url.URL, *RPCError) {
	if raw == "" {
		return nil, &RPCError{
			Code:    CodeInvalidParams,
			Message: "uri required",
		}
	}

	uri, err := url.Parse(raw)
	if err != nil {
		return nil, &RPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("uri %q: %s", raw, err),
		}
	}

	return uri, nil
}

// decodeBodyBase64 decodes a mutation's base64 body; the empty string
// (an absent body) decodes to nil. A malformed encoding is the caller's
// fault: CodeInvalidParams.
func decodeBodyBase64(encoded string) ([]byte, *RPCError) {
	if encoded == "" {
		return nil, nil
	}

	body, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, &RPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("body_base64: %s", err),
		}
	}

	return body, nil
}
