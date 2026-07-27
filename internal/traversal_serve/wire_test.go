package traversal_serve

import (
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// TestNodePatchResultDecodeStates pins how a node.patch result decodes for
// every shape a FOREIGN peer can legally send (cutting-garden#182). The
// adapter's three-state contract rests entirely on these semantics, and
// encoding/json's pointer-to-slice rules are easy to get backwards from
// memory — so they are asserted here rather than reasoned about. The peer
// is arbitrary (RFC 0013's first external implementation is Rust), so
// "our server never emits null" is NOT an argument about what can arrive.
func TestNodePatchResultDecodeStates(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		raw        string
		wantNilPtr bool
		wantNilVal bool
		wantLen    int
	}{
		{name: "key omitted", raw: `{}`, wantNilPtr: true},
		{
			// JSON null sets the POINTER to nil, so it collapses onto the
			// same "does not report" state as an omitted key.
			name: "explicit null", raw: `{"applied":null}`, wantNilPtr: true,
		},
		{name: "empty list", raw: `{"applied":[]}`, wantLen: 0},
		{name: "populated", raw: `{"applied":["state"]}`, wantLen: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var result NodePatchResult
			if err := json.Unmarshal([]byte(testCase.raw), &result); err != nil {
				t.Fatalf("decode %s: %v", testCase.raw, err)
			}

			if (result.Applied == nil) != testCase.wantNilPtr {
				t.Fatalf("%s: Applied pointer nil = %v, want %v",
					testCase.raw, result.Applied == nil, testCase.wantNilPtr)
			}
			if testCase.wantNilPtr {
				return
			}
			if (*result.Applied == nil) != testCase.wantNilVal {
				t.Errorf("%s: dereferenced slice nil = %v, want %v",
					testCase.raw, *result.Applied == nil, testCase.wantNilVal)
			}
			if got := len(*result.Applied); got != testCase.wantLen {
				t.Errorf("%s: len = %d, want %d", testCase.raw, got, testCase.wantLen)
			}
		})
	}
}

func TestSchemaAndTokens(t *testing.T) {
	if SchemaV1 != "traversal-plugin/v1" {
		t.Errorf("SchemaV1 = %q", SchemaV1)
	}

	for _, tc := range []struct {
		got, want string
	}{
		{MethodInitialize, "initialize"},
		{MethodShutdown, "shutdown"},
		{MethodNodesList, "nodes.list"},
		{MethodRootsList, "roots.list"},
		{MethodLeafRead, "leaf.read"},
		{MethodFacetCounts, "facets.counts"},
		{MethodFacetVersion, "facets.version"},
		{MethodLabelsResolve, "labels.resolve"},
		{MethodNodeCreate, "node.create"},
		{MethodNodePut, "node.put"},
		{MethodNodePatch, "node.patch"},
		{MethodNodeDelete, "node.delete"},
		{CapRoots, "roots"},
		{CapLeafRead, "leaf-read"},
		{CapFacetCounts, "facet-counts"},
		{CapFacetVersion, "facet-version"},
		{CapFacetLabels, "facet-labels"},
		{CapMutate, "mutate"},
	} {
		if tc.got != tc.want {
			t.Errorf("token = %q, want %q", tc.got, tc.want)
		}
	}

	for _, tc := range []struct {
		got, want int
	}{
		{CodeUnsupportedVersion, -32000},
		{CodeInvalidConfig, -32002},
		{CodeMethodNotFound, -32601},
		{CodeInvalidParams, -32602},
	} {
		if tc.got != tc.want {
			t.Errorf("code = %d, want %d", tc.got, tc.want)
		}
	}
}

// TestNodeViewRoundTrip carries a Node with a multi-valued dimension and
// a numeric-bucket order through the wire projection, JSON, and back.
func TestNodeViewRoundTrip(t *testing.T) {
	uri, err := url.Parse(
		"fj://forge.example/friedenberg/cutting-garden/issues/140",
	)
	if err != nil {
		t.Fatal(err)
	}

	node := cutting_garden_plugins.Node{
		URI:  uri,
		Name: "RFC: out-of-process traversal plugin protocol",
		Type: "fj-issue-v1",
		Facets: map[string][]cutting_garden_plugins.FacetValue{
			"label": {{Key: "rfc"}, {Key: "transport"}},
			"month": {{Key: "2026-07", Order: 202607}},
		},
	}

	raw, err := json.Marshal(NodeViewFrom(node))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(raw), `"order":202607`) {
		t.Errorf("non-zero order not on the wire: %s", raw)
	}

	// Order MUST be omitted when 0 (RFC 0013 §Wire encodings).
	if strings.Contains(string(raw), `"order":0`) {
		t.Errorf("zero order emitted: %s", raw)
	}

	var view NodeView
	if err = json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}

	got, err := view.ToNode()
	if err != nil {
		t.Fatal(err)
	}

	if got.URIString() != node.URIString() {
		t.Errorf("uri = %q, want %q", got.URIString(), node.URIString())
	}

	if got.Name != node.Name || got.Type != node.Type {
		t.Errorf("name/type = %q/%q, want %q/%q",
			got.Name, got.Type, node.Name, node.Type)
	}

	if !reflect.DeepEqual(got.Facets, node.Facets) {
		t.Errorf("facets = %#v, want %#v", got.Facets, node.Facets)
	}
}

// TestNodeViewFacetsOmittedWhenEmpty pins the OPTIONAL facets encoding:
// a node contributing nothing has no "facets" key at all.
func TestNodeViewFacetsOmittedWhenEmpty(t *testing.T) {
	uri, err := url.Parse("fj://forge.example/")
	if err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(NodeViewFrom(cutting_garden_plugins.Node{
		URI:  uri,
		Name: "forge",
		Type: "fj-repo-v1",
	}))
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(raw), "facets") {
		t.Errorf("empty facets emitted: %s", raw)
	}
}

func TestNodeViewToNodeBadURI(t *testing.T) {
	if _, err := (NodeView{URI: "://missing-scheme"}).ToNode(); err == nil {
		t.Error("expected a URI parse error, got nil")
	}
}

// TestNodeTypeViewRoundTrip pins the mime_type encoding: the plugin
// never sends the octet-stream leaf default (the host applies it), an
// unspecified leaf mimetype stays absent, and an explicit leaf mimetype
// plus containers pass through verbatim.
func TestNodeTypeViewRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name     string
		domain   cutting_garden_plugins.NodeType
		wantWire cutting_garden_plugins.NodeType
		wantJSON string
	}{
		{
			name: "leaf with explicit default is sent unspecified",
			domain: cutting_garden_plugins.NodeType{
				Tag:      "fj-blob-v1",
				MimeType: cutting_garden_plugins.MimeTypeDefault,
			},
			wantWire: cutting_garden_plugins.NodeType{Tag: "fj-blob-v1"},
			wantJSON: `{"tag":"fj-blob-v1","container":false}`,
		},
		{
			name:     "leaf with unspecified mimetype",
			domain:   cutting_garden_plugins.NodeType{Tag: "fj-blob-v1"},
			wantWire: cutting_garden_plugins.NodeType{Tag: "fj-blob-v1"},
			wantJSON: `{"tag":"fj-blob-v1","container":false}`,
		},
		{
			name: "leaf with declared mimetype",
			domain: cutting_garden_plugins.NodeType{
				Tag:      "fj-comment-v1",
				MimeType: "text/markdown",
			},
			wantWire: cutting_garden_plugins.NodeType{
				Tag:      "fj-comment-v1",
				MimeType: "text/markdown",
			},
			wantJSON: `{"tag":"fj-comment-v1","container":false,` +
				`"mime_type":"text/markdown"}`,
		},
		{
			name: "container",
			domain: cutting_garden_plugins.NodeType{
				Tag:       "fj-repo-v1",
				Container: true,
			},
			wantWire: cutting_garden_plugins.NodeType{
				Tag:       "fj-repo-v1",
				Container: true,
			},
			wantJSON: `{"tag":"fj-repo-v1","container":true}`,
		},
		{
			// RFC 0018 §1: the uri_template field rides the node_types
			// declaration and survives the wire round trip verbatim; a
			// type without one omits it (the cases above).
			name: "container with uri_template",
			domain: cutting_garden_plugins.NodeType{
				Tag:         "fj-issue-v1",
				Container:   true,
				URITemplate: "fj://{host}/{owner}/{repo}/issues/{number}",
			},
			wantWire: cutting_garden_plugins.NodeType{
				Tag:         "fj-issue-v1",
				Container:   true,
				URITemplate: "fj://{host}/{owner}/{repo}/issues/{number}",
			},
			wantJSON: `{"tag":"fj-issue-v1","container":true,` +
				`"uri_template":"fj://{host}/{owner}/{repo}/issues/{number}"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(NodeTypeViewFrom(tc.domain))
			if err != nil {
				t.Fatal(err)
			}

			if string(raw) != tc.wantJSON {
				t.Errorf("json = %s, want %s", raw, tc.wantJSON)
			}

			var view NodeTypeView
			if err = json.Unmarshal(raw, &view); err != nil {
				t.Fatal(err)
			}

			if got := view.ToNodeType(); got != tc.wantWire {
				t.Errorf("round trip = %#v, want %#v", got, tc.wantWire)
			}
		})
	}
}

// TestFacetDimensionViewRoundTrip pins the closed-vs-open domain
// distinction: a non-nil Values slice (CLOSED domain, RFC 0012 §2)
// survives the round trip, and an open domain never grows a "values"
// key.
func TestFacetDimensionViewRoundTrip(t *testing.T) {
	closed := cutting_garden_plugins.FacetDimension{
		Key:   "state",
		Label: "State",
		Kind:  cutting_garden_plugins.FacetCategorical,
		Values: []cutting_garden_plugins.FacetValue{
			{Key: "open"}, {Key: "closed"},
		},
	}

	raw, err := json.Marshal(FacetDimensionViewFrom(closed))
	if err != nil {
		t.Fatal(err)
	}

	var view FacetDimensionView
	if err = json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}

	if got := view.ToFacetDimension(); !reflect.DeepEqual(got, closed) {
		t.Errorf("closed domain round trip = %#v, want %#v", got, closed)
	}

	open := cutting_garden_plugins.FacetDimension{
		Key:   "label",
		Kind:  cutting_garden_plugins.FacetCategorical,
		Multi: true,
	}

	if raw, err = json.Marshal(FacetDimensionViewFrom(open)); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(raw), "values") {
		t.Errorf("open domain emitted values: %s", raw)
	}

	if strings.Contains(string(raw), "label\":") &&
		!strings.Contains(string(raw), `"label":"`) {
		t.Errorf("empty label emitted: %s", raw)
	}

	view = FacetDimensionView{}
	if err = json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}

	if got := view.ToFacetDimension(); !reflect.DeepEqual(got, open) {
		t.Errorf("open domain round trip = %#v, want %#v", got, open)
	}
}

func TestNodeTypeFacetsViewRoundTrip(t *testing.T) {
	declared := cutting_garden_plugins.NodeTypeFacets{
		Tag: "fj-issue-v1",
		Dimensions: []cutting_garden_plugins.FacetDimension{
			{
				Key:  "month",
				Kind: cutting_garden_plugins.FacetNumericBucket,
			},
			{
				Key:  "feed",
				Kind: cutting_garden_plugins.FacetLabelled,
			},
		},
	}

	raw, err := json.Marshal(NodeTypeFacetsViewFrom(declared))
	if err != nil {
		t.Fatal(err)
	}

	var view NodeTypeFacetsView
	if err = json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}

	if got := view.ToNodeTypeFacets(); !reflect.DeepEqual(got, declared) {
		t.Errorf("round trip = %#v, want %#v", got, declared)
	}
}

// TestFacetFilterConversion pins the matches-everything semantics of
// the empty filter (RFC 0012 §6) across the wire projection.
func TestFacetFilterConversion(t *testing.T) {
	if views := PredicateViewsFrom(nil); views != nil {
		t.Errorf("empty filter projected to %#v, want nil", views)
	}

	empty := FacetFilterFrom(nil)
	if !empty.Matches(map[string][]cutting_garden_plugins.FacetValue{
		"state": {{Key: "open"}},
	}) {
		t.Error("empty filter must match everything")
	}

	filter := cutting_garden_plugins.FacetFilter{
		{Dimension: "state", Value: "open"},
		{Dimension: "label", Value: "rfc"},
	}

	views := PredicateViewsFrom(filter)

	raw, err := json.Marshal(views)
	if err != nil {
		t.Fatal(err)
	}

	want := `[{"dimension":"state","value":"open"},` +
		`{"dimension":"label","value":"rfc"}]`
	if string(raw) != want {
		t.Errorf("json = %s, want %s", raw, want)
	}

	var decoded []PredicateView
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}

	if got := FacetFilterFrom(decoded); !reflect.DeepEqual(got, filter) {
		t.Errorf("round trip = %#v, want %#v", got, filter)
	}
}

// TestLeafReadResultDecline pins the ok:false encoding: all optional
// fields absent, never null (RFC 0013 §Leaf content).
func TestLeafReadResultDecline(t *testing.T) {
	raw, err := json.Marshal(LeafReadResult{})
	if err != nil {
		t.Fatal(err)
	}

	if string(raw) != `{"ok":false}` {
		t.Errorf("json = %s, want {\"ok\":false}", raw)
	}
}

func TestFacetCountsResultEncoding(t *testing.T) {
	raw, err := json.Marshal(FacetCountsResult{})
	if err != nil {
		t.Fatal(err)
	}

	if string(raw) != `{"ok":false}` {
		t.Errorf("decline json = %s, want {\"ok\":false}", raw)
	}

	summary := cutting_garden_plugins.FacetSummary{
		"state": {"open": 3, "closed": 1},
	}

	raw, err = json.Marshal(FacetCountsResult{
		OK:       true,
		Summary:  summary,
		Complete: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	var decoded FacetCountsResult
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}

	if !decoded.OK || !decoded.Complete ||
		!reflect.DeepEqual(decoded.Summary, summary) {
		t.Errorf("round trip = %#v", decoded)
	}
}

func TestFacetVersionResultDecline(t *testing.T) {
	raw, err := json.Marshal(FacetVersionResult{})
	if err != nil {
		t.Fatal(err)
	}

	if string(raw) != `{"ok":false}` {
		t.Errorf("json = %s, want {\"ok\":false}", raw)
	}
}

// TestInitializeRoundTrip exercises the full handshake payloads,
// including the OPTIONAL facets/bodies declaration blocks and the
// omitempty behavior of config_toml.
func TestInitializeRoundTrip(t *testing.T) {
	params := InitializeParams{ProtocolVersions: []string{SchemaV1}}

	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(raw), "config_toml") {
		t.Errorf("absent config_toml emitted: %s", raw)
	}

	result := InitializeResult{
		Schema:       SchemaV1,
		Plugin:       PluginInfo{Name: "fj-cg", Version: "0.1.0"},
		Schemes:      []string{"fj"},
		TypeTag:      "cutting_garden-capture_receipt-fj-v1",
		Capabilities: []string{CapRoots, CapLeafRead},
		NodeTypes: []NodeTypeView{
			{Tag: "fj-repo-v1", Container: true},
			{Tag: "fj-comment-v1", MimeType: "text/markdown"},
		},
		Facets: []NodeTypeFacetsView{
			{
				Tag: "fj-issue-v1",
				Dimensions: []FacetDimensionView{
					{Key: "state", Kind: "categorical"},
				},
			},
		},
		Bodies: []NodeTypeBodyView{
			{
				Tag:     "fj-comment-v1",
				Accepts: []string{"text/markdown (the comment body)"},
				Example: "Looks good to me.",
			},
		},
	}

	if raw, err = json.Marshal(result); err != nil {
		t.Fatal(err)
	}

	var decoded InitializeResult
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(decoded, result) {
		t.Errorf("round trip = %#v, want %#v", decoded, result)
	}

	bare := InitializeResult{
		Schema:       SchemaV1,
		Plugin:       PluginInfo{Name: "x", Version: "0"},
		Schemes:      []string{"x"},
		TypeTag:      "t",
		Capabilities: []string{},
		NodeTypes:    []NodeTypeView{{Tag: "x-v1"}},
	}

	if raw, err = json.Marshal(bare); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"facets", "bodies"} {
		if strings.Contains(string(raw), key) {
			t.Errorf("absent %s block emitted: %s", key, raw)
		}
	}
}

// TestNodeTypeBodyViewRoundTrip pins the example encoding: a structured
// example survives JSON, and a plugin with no structured form emits no
// "example" key.
func TestNodeTypeBodyViewRoundTrip(t *testing.T) {
	body := cutting_garden_plugins.NodeTypeBody{
		Tag:     "caldav-object-v1",
		Accepts: []string{"application/json"},
		Example: map[string]any{"component": "event"},
	}

	raw, err := json.Marshal(NodeTypeBodyViewFrom(body))
	if err != nil {
		t.Fatal(err)
	}

	var view NodeTypeBodyView
	if err = json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}

	if got := view.ToNodeTypeBody(); !reflect.DeepEqual(got, body) {
		t.Errorf("round trip = %#v, want %#v", got, body)
	}

	raw, err = json.Marshal(NodeTypeBodyViewFrom(
		cutting_garden_plugins.NodeTypeBody{
			Tag:     "x-v1",
			Accepts: []string{"text/plain"},
		},
	))
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(raw), "example") {
		t.Errorf("absent example emitted: %s", raw)
	}
}

// TestMutationParamEncodings pins the mutation payload field names and
// the OPTIONAL body_base64 on create (RFC 0013 §Mutation).
func TestMutationParamEncodings(t *testing.T) {
	raw, err := json.Marshal(NodeCreateParams{
		URI:  "fj://forge.example/x",
		Type: "fj-comment-v1",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := `{"uri":"fj://forge.example/x","type":"fj-comment-v1"}`
	if string(raw) != want {
		t.Errorf("json = %s, want %s", raw, want)
	}

	if raw, err = json.Marshal(NodePutParams{
		URI:        "fj://forge.example/x",
		BodyBase64: "SGVsbG8=",
	}); err != nil {
		t.Fatal(err)
	}

	want = `{"uri":"fj://forge.example/x","body_base64":"SGVsbG8="}`
	if string(raw) != want {
		t.Errorf("json = %s, want %s", raw, want)
	}
}
