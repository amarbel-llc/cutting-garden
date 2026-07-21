// Package traversal_serve implements the RFC 0013 traversal-plugin
// transport: a persistent JSON-RPC 2.0 session over an AF_UNIX
// SOCK_STREAM connection through which an out-of-process plugin serves
// the in-process traversal capability surface — RootLister/RootProvider
// enumeration (FDR 0014), LeafReader content fetch (#85), facet
// summaries (RFC 0012), and node mutation (FDR 0020) — so `list`,
// `mcp`, and the facet cache render a wire plugin identically to a
// linked one.
//
// This file defines the wire shapes: the method names, the v1 schema
// and capability tokens, the RFC-defined error codes, the param/result
// structs each method carries, and the view types projecting the
// cutting_garden_plugins domain types onto the wire (RFC 0013 §Wire
// encodings). The JSON-RPC envelope itself is go-mcp's jsonrpc.Message,
// as in capture_serve; the newline-delimited stream peer that frames
// one message per line lives in peer.go.
package traversal_serve

import (
	"encoding/json"
	"net/url"
	"time"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// SchemaV1 is the protocol version token negotiated by initialize
// (RFC 0013 §Handshake).
const SchemaV1 = "traversal-plugin/v1"

// Method names (RFC 0013 §Method set). The host is the only request
// initiator in v1: it calls the requests below and notifies shutdown;
// the plugin only responds. initialize, shutdown, and nodes.list are
// mandatory; every other method is gated on the corresponding
// capability token appearing in the plugin's initialize response.
const (
	MethodInitialize      = "initialize"
	MethodShutdown        = "shutdown"
	MethodNodesList       = "nodes.list"
	MethodRootsList       = "roots.list"
	MethodLeafRead        = "leaf.read"
	MethodFacetCounts     = "facets.counts"
	MethodFacetVersion    = "facets.version"
	MethodLabelsResolve   = "labels.resolve"
	MethodNodeCreate      = "node.create"
	MethodNodeCreateChild = "node.create_child"
	MethodNodePut         = "node.put"
	MethodNodePatch       = "node.patch"
	MethodNodeDelete      = "node.delete"
)

// JSON-RPC error codes (RFC 0013 §Errors). The first two are RFC-defined
// and initialize-only; the rest are JSON-RPC 2.0 standard codes.
//
// The CALLER-FAULT vs PLUGIN-FAULT split — CodeInvalidParams against
// CodeInternalError — is load-bearing, not descriptive
// (cutting-garden#185). They mean opposite things operationally: an
// internal error says "this plugin failed", which invites a retry, and a
// retried malformed request fails identically forever; invalid-params says
// "your request is wrong", which the caller can act on. A plugin that maps
// every failure to CodeInternalError therefore does not merely report
// imprecisely, it converts every caller mistake into an unretryable retry
// loop. Classifying is the plugin's job precisely because only it knows
// which of the two a given failure is.
//
// Domain outcomes the Go contracts express as ok == false are RESULTS,
// never JSON-RPC errors — that distinction selects fallback behavior
// host-side, not failure.
const (
	CodeUnsupportedVersion = -32000
	CodeInvalidConfig      = -32002
	CodeMethodNotFound     = -32601
	CodeInvalidParams      = -32602
	CodeInternalError      = -32603
)

// Capability tokens (RFC 0013 §Method set), advertised in
// InitializeResult.Capabilities. Each gates the like-named method(s);
// the host MUST NOT call an unadvertised method, and MUST ignore
// unknown tokens (forward compatibility, mirroring how the Go SDK
// grows by new narrow interfaces).
const (
	CapRoots        = "roots"
	CapLeafRead     = "leaf-read"
	CapFacetCounts  = "facet-counts"
	CapFacetVersion = "facet-version"
	CapFacetLabels  = "facet-labels"
	CapMutate       = "mutate"
	// CapContainerCreate gates node.create_child — server-assigned
	// identity creation (ContainerCreator, cutting-garden#143). An
	// additive capability token under RFC 0013 §Compatibility.
	CapContainerCreate = "container-create"
)

// InitializeParams is the host→plugin initialize request payload
// (RFC 0013 §Handshake).
type InitializeParams struct {
	// ProtocolVersions are the schema tokens the host speaks, in
	// preference order.
	ProtocolVersions []string `json:"protocol_versions"`
	// ConfigTOML is the raw TOML text of the plugin's own config
	// section (RFC 0007 §Plugin-Owned Sections), verbatim; absent when
	// no section is configured. Secrets are indirections (env-var
	// names) — credential material MUST NOT appear here.
	ConfigTOML string `json:"config_toml,omitempty"`
}

// PluginInfo identifies the plugin binary; diagnostic only.
type PluginInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is the plugin's initialize response. All three
// declaration blocks (node_types, facets, bodies) ride here because
// their in-process contracts are stable for the plugin's lifetime —
// there are no types.list / facets.describe round trips.
type InitializeResult struct {
	// Schema is the single version selected from ProtocolVersions.
	Schema string     `json:"schema"`
	Plugin PluginInfo `json:"plugin"`
	// Schemes are the URI schemes the plugin serves (Plugin.Schemes).
	// MUST be non-empty; the host validates it covers the schemes its
	// configuration routed to this plugin.
	Schemes []string `json:"schemes"`
	// TypeTag is Plugin.TypeTag (RFC 0002 vocabulary), present for
	// registry parity even though this transport performs no capture.
	TypeTag string `json:"type_tag"`
	// Capabilities are the gate tokens of §Method set (the Cap*
	// constants); unknown tokens are ignored by the host.
	Capabilities []string `json:"capabilities"`
	// NodeTypes is the RootLister.Types() declaration. MUST be
	// non-empty and stable for the session's lifetime.
	NodeTypes []NodeTypeView `json:"node_types"`
	// Facets is OPTIONAL: the FacetDescriber.DescribeFacets()
	// declaration. Its presence IS the FacetDescriber capability.
	Facets []NodeTypeFacetsView `json:"facets,omitempty"`
	// Bodies is OPTIONAL: the BodyDescriber.DescribeBodies()
	// declaration. Meaningful only alongside CapMutate.
	Bodies []NodeTypeBodyView `json:"bodies,omitempty"`
}

// NodesListParams is the nodes.list request payload: the URI whose
// immediate children to enumerate (RootLister.ListRoots — one level,
// lazy). The plugin fails a URI whose scheme it did not advertise with
// CodeInvalidParams.
type NodesListParams struct {
	URI string `json:"uri"`
}

// NodesListResult is the nodes.list response: the immediate children
// of the requested URI. A leaf (or empty container) returns an empty
// list.
type NodesListResult struct {
	Nodes []NodeView `json:"nodes"`
}

// RootsListResult is the roots.list response: the plugin's top-level
// entry points (RootProvider.Roots), possibly empty. Each MUST be
// credential-free. The request carries empty params.
type RootsListResult struct {
	Roots []string `json:"roots"`
}

// LeafReadParams is the leaf.read request payload (LeafReader.ReadLeaf).
type LeafReadParams struct {
	URI string `json:"uri"`
}

// LeafReadResult is the leaf.read response. OK false (all other fields
// absent) means "not a fetchable leaf — fall back to the child
// listing", NOT an error; a JSON-RPC error is reserved for unexpected
// failures, exactly as the Go contract reserves non-nil err.
type LeafReadResult struct {
	OK bool `json:"ok"`
	// Structured is the parsed JSON projection of the leaf
	// (LeafContent.Structured); absent when the plugin offers none.
	// It stays raw here so the wire bytes cross the boundary
	// unreinterpreted.
	Structured json.RawMessage `json:"structured,omitempty"`
	// RawBase64 is the leaf's verbatim source bytes in standard
	// base64 (RFC 4648 §4, with padding); absent when there is no raw
	// form.
	RawBase64 string `json:"raw_base64,omitempty"`
	// RawMimeType is RawBase64's IANA content type; absent alongside
	// it.
	RawMimeType string `json:"raw_mime_type,omitempty"`
}

// FacetCountsParams is the facets.counts request payload
// (FacetCounter.FacetCounts). An absent/empty Filter matches
// everything (RFC 0012 §6).
type FacetCountsParams struct {
	URI    string          `json:"uri"`
	Filter []PredicateView `json:"filter,omitempty"`
}

// FacetCountsResult is the facets.counts response. OK false means "I
// do not summarize this node; fall back to the framework fold over
// nodes.list" (RFC 0012 §4–§5). Every dimension key in Summary MUST be
// declared in the initialize facets block.
type FacetCountsResult struct {
	OK bool `json:"ok"`
	// Summary is the per-dimension aggregate — already
	// map[string]map[string]int64-shaped, so the domain type crosses
	// the wire directly.
	Summary cutting_garden_plugins.FacetSummary `json:"summary,omitempty"`
	// Complete is false when the summary is known not to cover the
	// whole subtree (RFC 0012 §5).
	Complete bool `json:"complete,omitempty"`
}

// FacetVersionParams is the facets.version request payload
// (FacetVersioner.FacetVersion).
type FacetVersionParams struct {
	URI string `json:"uri"`
}

// FacetVersionResult is the facets.version response: the RFC 0012 §11
// change token. OK false means no token — the host's cache falls back
// to its TTL.
type FacetVersionResult struct {
	OK    bool   `json:"ok"`
	Token string `json:"token,omitempty"`
}

// LabelsResolveParams is the labels.resolve request payload
// (FacetLabeler.ResolveFacetLabels): one labelled dimension and the
// value keys to name.
type LabelsResolveParams struct {
	Dimension string   `json:"dimension"`
	Keys      []string `json:"keys"`
}

// LabelsResolveResult is the labels.resolve response. A key absent
// from Labels (or an empty label) means "no label" and the host falls
// back to the key; the whole method is presentation-only and non-fatal
// (RFC 0012 §7).
type LabelsResolveResult struct {
	Labels map[string]string `json:"labels"`
}

// NodeCreateParams is the node.create request payload
// (NodeMutator, FDR 0020): strict create, no upsert — an existing URI
// is an error. Type MUST be a declared node_types tag. The result is
// empty.
type NodeCreateParams struct {
	URI  string `json:"uri"`
	Type string `json:"type"`
	// BodyBase64 is the new node's body in standard base64; OPTIONAL
	// (a bodyless create).
	BodyBase64 string `json:"body_base64,omitempty"`
}

// NodeCreateChildParams is the node.create_child request payload
// (ContainerCreator, cutting-garden#143): create a node of Type under
// the Container URI, the source assigning the created node's identity.
type NodeCreateChildParams struct {
	Container string `json:"container"`
	Type      string `json:"type"`
	// BodyBase64 is the new node's body in standard base64; OPTIONAL.
	BodyBase64 string `json:"body_base64,omitempty"`
}

// NodeCreateChildResult reports the URI the source assigned. MUST be
// non-empty and credential-free.
type NodeCreateChildResult struct {
	Created string `json:"created"`
}

// NodePutParams is the node.put request payload: full-replace an
// existing leaf's body (NodeMutator.PutNode — the body represents the
// complete desired state). A non-existent URI is an error. The result
// is empty.
type NodePutParams struct {
	URI        string `json:"uri"`
	BodyBase64 string `json:"body_base64"`
}

// NodePatchParams is the node.patch request payload: a partial-field
// update of an existing node (NodeMutator.PatchNode — only the fields
// named in the body change; the body format is plugin-defined). An
// empty body is a bad-request error.
type NodePatchParams struct {
	URI        string `json:"uri"`
	BodyBase64 string `json:"body_base64"`
}

// NodePatchResult reports which field keys the plugin ACTUALLY applied
// (NodeMutator.PatchNode's applied, cutting-garden#182). Applied is a
// POINTER so the three states the Go contract distinguishes survive the
// wire: a present list (possibly empty) is authoritative, and an OMITTED
// key means "this plugin does not report applied fields" — the state a
// peer written against RFC 0013 before this field existed lands in. A
// plain []string would collapse that absence into an empty list and
// report every such peer's successful patch as a no-op, which is the
// very false signal #182 exists to remove.
type NodePatchResult struct {
	Applied *[]string `json:"applied,omitempty"`
}

// NodeDeleteParams is the node.delete request payload. The result is
// empty.
type NodeDeleteParams struct {
	URI string `json:"uri"`
}

// FacetValueView is the wire form of cutting_garden_plugins.FacetValue
// (RFC 0013 §Wire encodings): Order is omitted when 0 ("no hint").
type FacetValueView struct {
	Key   string `json:"key"`
	Order int64  `json:"order,omitempty"`
}

// FacetValueViewFrom projects one facet value onto the wire.
func FacetValueViewFrom(
	value cutting_garden_plugins.FacetValue,
) FacetValueView {
	return FacetValueView{Key: value.Key, Order: value.Order}
}

// ToFacetValue is the inverse of FacetValueViewFrom.
func (v FacetValueView) ToFacetValue() cutting_garden_plugins.FacetValue {
	return cutting_garden_plugins.FacetValue{Key: v.Key, Order: v.Order}
}

// facetValueViewsFrom projects a value slice, preserving nil-ness —
// load-bearing for FacetDimension.Values, where nil means an OPEN
// domain and non-nil a CLOSED one (RFC 0012 §2).
func facetValueViewsFrom(
	values []cutting_garden_plugins.FacetValue,
) []FacetValueView {
	if values == nil {
		return nil
	}

	views := make([]FacetValueView, len(values))

	for i, value := range values {
		views[i] = FacetValueViewFrom(value)
	}

	return views
}

// facetValuesFrom is the inverse of facetValueViewsFrom, with the same
// nil preservation.
func facetValuesFrom(
	views []FacetValueView,
) []cutting_garden_plugins.FacetValue {
	if views == nil {
		return nil
	}

	values := make([]cutting_garden_plugins.FacetValue, len(views))

	for i, view := range views {
		values[i] = view.ToFacetValue()
	}

	return values
}

// NodeView is the wire form of cutting_garden_plugins.Node (RFC 0013
// §Wire encodings): the URI as an RFC 3986 string, Facets omitted when
// the node contributes nothing.
type NodeView struct {
	URI    string                      `json:"uri"`
	Name   string                      `json:"name"`
	Type   string                      `json:"type"`
	Facets map[string][]FacetValueView `json:"facets,omitempty"`
}

// NodeViewFrom projects a Node onto the wire.
func NodeViewFrom(node cutting_garden_plugins.Node) NodeView {
	view := NodeView{
		URI:  node.URIString(),
		Name: node.Name,
		Type: node.Type,
	}

	if len(node.Facets) > 0 {
		view.Facets = make(
			map[string][]FacetValueView, len(node.Facets),
		)

		for key, values := range node.Facets {
			view.Facets[key] = facetValueViewsFrom(values)
		}
	}

	return view
}

// ToNode is the inverse of NodeViewFrom; an unparseable URI surfaces
// as an error.
func (v NodeView) ToNode() (cutting_garden_plugins.Node, error) {
	uri, err := url.Parse(v.URI)
	if err != nil {
		return cutting_garden_plugins.Node{}, errors.Wrapf(
			err, "parse node uri %q", v.URI,
		)
	}

	node := cutting_garden_plugins.Node{
		URI:  uri,
		Name: v.Name,
		Type: v.Type,
	}

	if len(v.Facets) > 0 {
		node.Facets = make(
			map[string][]cutting_garden_plugins.FacetValue,
			len(v.Facets),
		)

		for key, values := range v.Facets {
			node.Facets[key] = facetValuesFrom(values)
		}
	}

	return node, nil
}

// NodeTypeView is the wire form of cutting_garden_plugins.NodeType
// (RFC 0013 §Wire encodings). An absent mime_type on a leaf means
// unspecified — the HOST applies the octet-stream default via
// NodeType.BodyMimeType; the plugin never sends the default.
type NodeTypeView struct {
	Tag       string `json:"tag"`
	Container bool   `json:"container"`
	MimeType  string `json:"mime_type,omitempty"`
}

// NodeTypeViewFrom projects a declared NodeType onto the wire. A leaf
// declared with the explicit octet-stream default is normalized to
// unspecified, enforcing the plugin-never-sends-the-default rule.
func NodeTypeViewFrom(
	nodeType cutting_garden_plugins.NodeType,
) NodeTypeView {
	mimeType := nodeType.MimeType

	if !nodeType.Container &&
		mimeType == cutting_garden_plugins.MimeTypeDefault {
		mimeType = ""
	}

	return NodeTypeView{
		Tag:       nodeType.Tag,
		Container: nodeType.Container,
		MimeType:  mimeType,
	}
}

// ToNodeType is the inverse of NodeTypeViewFrom. An absent mime_type
// stays empty here; the consumer resolves the leaf default through
// NodeType.BodyMimeType, exactly as with a linked plugin.
func (v NodeTypeView) ToNodeType() cutting_garden_plugins.NodeType {
	return cutting_garden_plugins.NodeType{
		Tag:       v.Tag,
		Container: v.Container,
		MimeType:  v.MimeType,
	}
}

// FacetDimensionView is the wire form of
// cutting_garden_plugins.FacetDimension (RFC 0013 §Wire encodings):
// Values present ≙ a CLOSED domain (RFC 0012 §2);
// revalidate_after_seconds (absent ≙ 0) marks a VOLATILE dimension
// (RFC 0012 §11.3) — the additive-field precedent of RFC 0013
// §Compatibility.
type FacetDimensionView struct {
	Key                    string           `json:"key"`
	Label                  string           `json:"label,omitempty"`
	Kind                   string           `json:"kind"`
	Multi                  bool             `json:"multi,omitempty"`
	Values                 []FacetValueView `json:"values,omitempty"`
	RevalidateAfterSeconds int64            `json:"revalidate_after_seconds,omitempty"`
}

// FacetDimensionViewFrom projects a declared dimension onto the wire.
// RevalidateAfter is truncated to whole seconds (the wire unit).
func FacetDimensionViewFrom(
	dimension cutting_garden_plugins.FacetDimension,
) FacetDimensionView {
	return FacetDimensionView{
		Key:                    dimension.Key,
		Label:                  dimension.Label,
		Kind:                   string(dimension.Kind),
		Multi:                  dimension.Multi,
		Values:                 facetValueViewsFrom(dimension.Values),
		RevalidateAfterSeconds: int64(dimension.RevalidateAfter / time.Second),
	}
}

// ToFacetDimension is the inverse of FacetDimensionViewFrom.
func (v FacetDimensionView) ToFacetDimension() cutting_garden_plugins.FacetDimension {
	return cutting_garden_plugins.FacetDimension{
		Key:             v.Key,
		Label:           v.Label,
		Kind:            cutting_garden_plugins.FacetKind(v.Kind),
		Multi:           v.Multi,
		Values:          facetValuesFrom(v.Values),
		RevalidateAfter: time.Duration(v.RevalidateAfterSeconds) * time.Second,
	}
}

// NodeTypeFacetsView is the wire form of
// cutting_garden_plugins.NodeTypeFacets: one node type's declared
// facet dimensions, carried in the initialize facets block.
type NodeTypeFacetsView struct {
	Tag        string               `json:"tag"`
	Dimensions []FacetDimensionView `json:"dimensions"`
}

// NodeTypeFacetsViewFrom projects one type's facet declaration onto
// the wire.
func NodeTypeFacetsViewFrom(
	facets cutting_garden_plugins.NodeTypeFacets,
) NodeTypeFacetsView {
	view := NodeTypeFacetsView{Tag: facets.Tag}

	if facets.Dimensions != nil {
		view.Dimensions = make(
			[]FacetDimensionView, len(facets.Dimensions),
		)

		for i, dimension := range facets.Dimensions {
			view.Dimensions[i] = FacetDimensionViewFrom(dimension)
		}
	}

	return view
}

// ToNodeTypeFacets is the inverse of NodeTypeFacetsViewFrom.
func (v NodeTypeFacetsView) ToNodeTypeFacets() cutting_garden_plugins.NodeTypeFacets {
	facets := cutting_garden_plugins.NodeTypeFacets{Tag: v.Tag}

	if v.Dimensions != nil {
		facets.Dimensions = make(
			[]cutting_garden_plugins.FacetDimension,
			len(v.Dimensions),
		)

		for i, dimension := range v.Dimensions {
			facets.Dimensions[i] = dimension.ToFacetDimension()
		}
	}

	return facets
}

// NodeTypeBodyView is the wire form of
// cutting_garden_plugins.NodeTypeBody, carried in the initialize
// bodies block. Example is any JSON value (the domain field is
// JSON-marshalable by contract); absent when the plugin offers no
// structured form.
type NodeTypeBodyView struct {
	Tag     string   `json:"tag"`
	Accepts []string `json:"accepts"`
	Example any      `json:"example,omitempty"`
	// ServerAssignedIdentity marks a container-create type
	// (cutting-garden#143, node.create_child) — additive under
	// RFC 0013 §Compatibility's ignore-unknown rule.
	ServerAssignedIdentity bool `json:"server_assigned_identity,omitempty"`
}

// NodeTypeBodyViewFrom projects one writable type's body description
// onto the wire.
func NodeTypeBodyViewFrom(
	body cutting_garden_plugins.NodeTypeBody,
) NodeTypeBodyView {
	return NodeTypeBodyView{
		Tag:                    body.Tag,
		Accepts:                body.Accepts,
		Example:                body.Example,
		ServerAssignedIdentity: body.ServerAssignedIdentity,
	}
}

// ToNodeTypeBody is the inverse of NodeTypeBodyViewFrom. A decoded
// Example is the generic JSON shape (map[string]any etc.), which
// satisfies the domain field's JSON-marshalable contract.
func (v NodeTypeBodyView) ToNodeTypeBody() cutting_garden_plugins.NodeTypeBody {
	return cutting_garden_plugins.NodeTypeBody{
		Tag:                    v.Tag,
		Accepts:                v.Accepts,
		Example:                v.Example,
		ServerAssignedIdentity: v.ServerAssignedIdentity,
	}
}

// PredicateView is the wire form of
// cutting_garden_plugins.FacetPredicate: one equality constraint of a
// facets.counts filter.
type PredicateView struct {
	Dimension string `json:"dimension"`
	Value     string `json:"value"`
}

// PredicateViewsFrom projects a FacetFilter onto the wire. The empty
// filter (nil or zero-length) projects to nil, which marshals as an
// absent filter — both mean matches-everything (RFC 0012 §6).
func PredicateViewsFrom(
	filter cutting_garden_plugins.FacetFilter,
) []PredicateView {
	if len(filter) == 0 {
		return nil
	}

	views := make([]PredicateView, len(filter))

	for i, predicate := range filter {
		views[i] = PredicateView{
			Dimension: predicate.Dimension,
			Value:     predicate.Value,
		}
	}

	return views
}

// FacetFilterFrom is the inverse of PredicateViewsFrom: an absent or
// empty wire filter becomes the empty FacetFilter, whose Matches
// accepts everything.
func FacetFilterFrom(
	views []PredicateView,
) cutting_garden_plugins.FacetFilter {
	if len(views) == 0 {
		return nil
	}

	filter := make(cutting_garden_plugins.FacetFilter, len(views))

	for i, view := range views {
		filter[i] = cutting_garden_plugins.FacetPredicate{
			Dimension: view.Dimension,
			Value:     view.Value,
		}
	}

	return filter
}
