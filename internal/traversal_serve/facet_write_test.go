package traversal_serve

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// fakeWritablePlugin is fakeFullPlugin (every capability, including mutate)
// plus a multi-valued "label" dimension and a configurable facet-write
// declaration — the RFC 0013 facet_writes amendment's test subject.
type fakeWritablePlugin struct {
	*fakeFullPlugin
	writes []cutting_garden_plugins.NodeTypeFacetWrites
}

var _ cutting_garden_plugins.FacetWriteDescriber = (*fakeWritablePlugin)(nil)

func (p *fakeWritablePlugin) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	declared := p.fakeFullPlugin.DescribeFacets()
	declared[0].Dimensions = append(
		declared[0].Dimensions,
		cutting_garden_plugins.FacetDimension{
			Key:   "label",
			Kind:  cutting_garden_plugins.FacetCategorical,
			Multi: true,
		},
	)
	return declared
}

func (p *fakeWritablePlugin) DescribeFacetWrites() []cutting_garden_plugins.NodeTypeFacetWrites {
	return p.writes
}

// conformingWrites is a valid declaration over fakeWritablePlugin's leaf
// type: a one (status-like) write, a many (labels-like) write whose field
// deliberately differs from its dimension key, and a declared read-only
// dimension.
func conformingWrites() []cutting_garden_plugins.NodeTypeFacetWrites {
	return []cutting_garden_plugins.NodeTypeFacetWrites{{
		Tag: fakeLeafType,
		Writes: []cutting_garden_plugins.FacetWrite{
			{
				DimensionKey: "state",
				Mode:         cutting_garden_plugins.FacetWriteOne,
				Field:        "state",
				Values:       []string{"open", "closed"},
			},
			{
				DimensionKey:   "label",
				Mode:           cutting_garden_plugins.FacetWriteMany,
				Field:          "labels",
				CompletionHint: "full-set replacement",
			},
			{
				DimensionKey: "month",
				Mode:         cutting_garden_plugins.FacetWriteNone,
			},
		},
	}}
}

func writablePluginConfig(
	writes []cutting_garden_plugins.NodeTypeFacetWrites,
) ServeConfig {
	return ServeConfig{
		Plugin: &fakeWritablePlugin{
			fakeFullPlugin: &fakeFullPlugin{},
			writes:         writes,
		},
		Info: PluginInfo{Name: "fake-mem", Version: "0.0.1"},
	}
}

// TestServerAdvertisesFacetWritesInInitialize pins the wire shape a Go peer
// emits: a facet_writes block parallel to facets, one entry per node type,
// each write keyed by dimension with mode/field/values and the optional
// descriptive members, omitted when zero.
func TestServerAdvertisesFacetWritesInInitialize(t *testing.T) {
	client, _ := startServe(t, writablePluginConfig(conformingWrites()))

	var raw map[string]json.RawMessage
	if err := client.Call(
		context.Background(), MethodInitialize,
		InitializeParams{ProtocolVersions: []string{SchemaV1}}, &raw,
	); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	got := string(raw["facet_writes"])
	want := `[{"tag":"mem-obj-v1","writes":[` +
		`{"dimension":"state","mode":"one","field":"state","values":["open","closed"]},` +
		`{"dimension":"label","mode":"many","field":"labels","completion_hint":"full-set replacement"},` +
		`{"dimension":"month","mode":"none"}]}]`
	if got != want {
		t.Errorf("facet_writes =\n%s\nwant\n%s", got, want)
	}
}

// TestServerOmitsFacetWritesWithoutDescriber pins back-compat: a plugin with
// no FacetWriteDescriber sends no facet_writes key at all.
func TestServerOmitsFacetWritesWithoutDescriber(t *testing.T) {
	client, _ := startServe(t, fullPluginConfig(&fakeFullPlugin{}))

	var raw map[string]json.RawMessage
	if err := client.Call(
		context.Background(), MethodInitialize,
		InitializeParams{ProtocolVersions: []string{SchemaV1}}, &raw,
	); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	if value, present := raw["facet_writes"]; present {
		t.Errorf("facet_writes present (%s) for a plugin declaring none", value)
	}
}

// TestWirePluginDescribeFacetWritesMatchesLinked pins indistinguishability of
// the declaration: the adapter answers DescribeFacetWrites from the cached
// initialize exactly as the linked plugin answers it.
func TestWirePluginDescribeFacetWritesMatchesLinked(t *testing.T) {
	cfg := writablePluginConfig(conformingWrites())
	adapter, _ := newTestWirePlugin(t, memSpec(), cfg)

	linked := cfg.Plugin.(cutting_garden_plugins.FacetWriteDescriber)
	if got, want := adapter.DescribeFacetWrites(), linked.DescribeFacetWrites(); !reflect.DeepEqual(got, want) {
		t.Errorf("DescribeFacetWrites:\nwire:   %+v\nlinked: %+v", got, want)
	}
}

// TestWirePluginWithoutFacetWritesDescribesNothing pins the no-writes peer:
// the adapter still satisfies FacetWriteDescriber statically, but reports no
// mappings — the "plugin omits the interface" decline.
func TestWirePluginWithoutFacetWritesDescribesNothing(t *testing.T) {
	adapter, _ := newTestWirePlugin(
		t, memSpec(), fullPluginConfig(&fakeFullPlugin{}),
	)

	if got := adapter.DescribeFacetWrites(); got != nil {
		t.Errorf("DescribeFacetWrites = %+v, want nil", got)
	}
}

// TestHostFacetWritePatchShapes pins the two host-built node.patch bodies
// (RFC 0013 facet_writes amendment): one → {field: bucket}; many →
// {field: [complete set]}, with an empty or nil set clearing as [].
func TestHostFacetWritePatchShapes(t *testing.T) {
	one := cutting_garden_plugins.FacetWrite{
		DimensionKey: "state", Mode: cutting_garden_plugins.FacetWriteOne,
		Field: "state",
	}
	many := cutting_garden_plugins.FacetWrite{
		DimensionKey: "label", Mode: cutting_garden_plugins.FacetWriteMany,
		Field: "labels",
	}

	body, err := HostFacetWritePatch(one, "closed")
	if err != nil {
		t.Fatalf("one: %v", err)
	}
	if got, want := string(body), `{"state":"closed"}`; got != want {
		t.Errorf("one body = %s, want %s", got, want)
	}

	for _, tc := range []struct {
		name string
		set  []string
		want string
	}{
		{"set", []string{"b", "a"}, `{"labels":["b","a"]}`},
		{"empty", []string{}, `{"labels":[]}`},
		{"nil", nil, `{"labels":[]}`},
	} {
		body, err := HostMembershipWritePatch(many, tc.set)
		if err != nil {
			t.Fatalf("many %s: %v", tc.name, err)
		}
		if string(body) != tc.want {
			t.Errorf("many %s body = %s, want %s", tc.name, body, tc.want)
		}
	}

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"one into many builder", func() error {
			_, err := HostMembershipWritePatch(one, []string{"x"})
			return err
		}},
		{"many into one builder", func() error {
			_, err := HostFacetWritePatch(many, "x")
			return err
		}},
		{"empty bucket", func() error {
			_, err := HostFacetWritePatch(one, "")
			return err
		}},
		{"none mode", func() error {
			_, err := HostFacetWritePatch(cutting_garden_plugins.FacetWrite{
				DimensionKey: "month", Mode: cutting_garden_plugins.FacetWriteNone,
			}, "2026-07")
			return err
		}},
	} {
		err := tc.call()
		if err == nil || !errors.Is400BadRequest(err) {
			t.Errorf("%s: err = %v, want a bad request", tc.name, err)
		}
	}
}

// TestWirePluginBuildsHostPatchesFromDeclaration pins the adapter's
// FacetWriteApplier / MembershipWriteApplier: bodies are built from the
// DECLARED mapping for the node's type, and a request for an undeclared or
// read-only dimension is refused as a bad request before anything is sent.
func TestWirePluginBuildsHostPatchesFromDeclaration(t *testing.T) {
	adapter, dialer := newTestWirePlugin(
		t, memSpec(), writablePluginConfig(conformingWrites()),
	)
	ctx := context.Background()
	node := cutting_garden_plugins.Node{
		URI:  mustParseURL(t, fakeLeafA),
		Type: fakeLeafType,
	}
	declared := map[string]cutting_garden_plugins.FacetWrite{}
	for _, w := range conformingWrites()[0].Writes {
		declared[w.DimensionKey] = w
	}

	body, err := adapter.BuildFacetWritePatch(ctx, node, declared["state"], "closed")
	if err != nil {
		t.Fatalf("BuildFacetWritePatch: %v", err)
	}
	if got, want := string(body), `{"state":"closed"}`; got != want {
		t.Errorf("one body = %s, want %s", got, want)
	}

	body, err = adapter.BuildMembershipWritePatch(
		ctx, node, declared["label"], []string{"x", "y"},
	)
	if err != nil {
		t.Fatalf("BuildMembershipWritePatch: %v", err)
	}
	if got, want := string(body), `{"labels":["x","y"]}`; got != want {
		t.Errorf("many body = %s, want %s", got, want)
	}

	writesBefore := dialer.writeCount()

	undeclared := cutting_garden_plugins.FacetWrite{
		DimensionKey: "nope", Mode: cutting_garden_plugins.FacetWriteOne,
		Field: "nope",
	}
	if _, err := adapter.BuildFacetWritePatch(ctx, node, undeclared, "x"); err == nil ||
		!errors.Is400BadRequest(err) ||
		!strings.Contains(err.Error(), `"nope"`) {
		t.Errorf("undeclared dimension: err = %v, want a bad request naming it", err)
	}

	if _, err := adapter.BuildFacetWritePatch(
		ctx, node, declared["month"], "2026-07",
	); err == nil || !errors.Is400BadRequest(err) {
		t.Errorf("read-only dimension: err = %v, want a bad request", err)
	}

	otherType := node
	otherType.Type = fakeContainerType
	if _, err := adapter.BuildFacetWritePatch(
		ctx, otherType, declared["state"], "closed",
	); err == nil || !errors.Is400BadRequest(err) {
		t.Errorf("type without mappings: err = %v, want a bad request", err)
	}

	if got := dialer.writeCount(); got != writesBefore {
		t.Errorf("refused builds sent %d wire message(s), want none",
			got-writesBefore)
	}
}

// readOnlyWritesPlugin declares facets and a writable mapping but does NOT
// advertise mutate — the host cannot honor the declaration, so initialize
// is rejected.
type readOnlyWritesPlugin struct{ fakeMinimalPlugin }

func (readOnlyWritesPlugin) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	return []cutting_garden_plugins.NodeTypeFacets{{
		Tag: "min-obj-v1",
		Dimensions: []cutting_garden_plugins.FacetDimension{
			{Key: "state", Kind: cutting_garden_plugins.FacetCategorical},
		},
	}}
}

func (readOnlyWritesPlugin) DescribeFacetWrites() []cutting_garden_plugins.NodeTypeFacetWrites {
	return []cutting_garden_plugins.NodeTypeFacetWrites{{
		Tag: "min-obj-v1",
		Writes: []cutting_garden_plugins.FacetWrite{{
			DimensionKey: "state", Mode: cutting_garden_plugins.FacetWriteOne,
			Field: "state",
		}},
	}}
}

// TestWirePluginRejectsUnusableFacetWriteDeclaration pins host-side
// validation of the facet_writes block at bring-up: each violation fails
// the plugin loudly (naming the plugin and the dimension), persistently —
// every later operation fails fast on the same recorded error.
func TestWirePluginRejectsUnusableFacetWriteDeclaration(t *testing.T) {
	leafWrites := func(
		writes ...cutting_garden_plugins.FacetWrite,
	) []cutting_garden_plugins.NodeTypeFacetWrites {
		return []cutting_garden_plugins.NodeTypeFacetWrites{
			{Tag: fakeLeafType, Writes: writes},
		}
	}

	for _, tc := range []struct {
		name  string
		cfg   ServeConfig
		spec  PluginSpec
		wants []string
	}{
		{
			name: "undeclared dimension",
			cfg: writablePluginConfig(leafWrites(cutting_garden_plugins.FacetWrite{
				DimensionKey: "no-such-dimension",
				Mode:         cutting_garden_plugins.FacetWriteOne,
				Field:        "x",
			})),
			spec: memSpec(),
			wants: []string{
				`wire plugin "fake-mem"`,
				`type "mem-obj-v1" dimension "no-such-dimension" is not a declared facet dimension`,
			},
		},
		{
			name: "undeclared type",
			cfg: writablePluginConfig([]cutting_garden_plugins.NodeTypeFacetWrites{{
				Tag: "mem-ghost-v1",
				Writes: []cutting_garden_plugins.FacetWrite{{
					DimensionKey: "state",
					Mode:         cutting_garden_plugins.FacetWriteOne,
					Field:        "state",
				}},
			}}),
			spec:  memSpec(),
			wants: []string{`type "mem-ghost-v1" declares write mappings but no read facets`},
		},
		{
			name: "missing field",
			cfg: writablePluginConfig(leafWrites(cutting_garden_plugins.FacetWrite{
				DimensionKey: "state", Mode: cutting_garden_plugins.FacetWriteOne,
			})),
			spec:  memSpec(),
			wants: []string{`dimension "state" mode "one" requires a Field`},
		},
		{
			name: "invalid mode",
			cfg: writablePluginConfig(leafWrites(cutting_garden_plugins.FacetWrite{
				DimensionKey: "state", Mode: "several", Field: "state",
			})),
			spec:  memSpec(),
			wants: []string{`dimension "state" has invalid mode "several"`},
		},
		{
			name: "many on a single-valued dimension",
			cfg: writablePluginConfig(leafWrites(cutting_garden_plugins.FacetWrite{
				DimensionKey: "state", Mode: cutting_garden_plugins.FacetWriteMany,
				Field: "state",
			})),
			spec:  memSpec(),
			wants: []string{`dimension "state" mode "many" requires a multi-valued dimension`},
		},
		{
			name: "duplicate mapping",
			cfg: writablePluginConfig(leafWrites(
				cutting_garden_plugins.FacetWrite{
					DimensionKey: "state", Mode: cutting_garden_plugins.FacetWriteOne,
					Field: "state",
				},
				cutting_garden_plugins.FacetWrite{
					DimensionKey: "state", Mode: cutting_garden_plugins.FacetWriteNone,
				},
			)),
			spec:  memSpec(),
			wants: []string{`dimension "state" is mapped more than once`},
		},
		{
			name: "writable without mutate",
			cfg: ServeConfig{
				Plugin: readOnlyWritesPlugin{},
				Info:   PluginInfo{Name: "fake-min", Version: "0.0.1"},
			},
			spec: minSpec(),
			wants: []string{
				`wire plugin "fake-min"`,
				`dimension "state" is writable (mode "one") but the plugin does not advertise "mutate"`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter, dialer := newTestWirePlugin(t, tc.spec, tc.cfg)
			ctx := context.Background()
			uri := mustParseURL(t, tc.spec.Schemes[0]+"://host/root")

			_, err := adapter.ListRoots(ctx, uri)
			if err == nil {
				t.Fatal("expected initialize to be rejected")
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}

			if _, err := adapter.Roots(ctx); err == nil {
				t.Error("Roots after rejection: no error")
			}
			if got := adapter.DescribeFacetWrites(); got != nil {
				t.Errorf("DescribeFacetWrites after rejection = %+v, want nil", got)
			}
			if got := dialer.dialCount(); got != 1 {
				t.Errorf("dials = %d, want 1 — a bad declaration must not respawn", got)
			}
		})
	}
}

// TestFacetWriteViewRoundTrip pins the view conversion's fidelity for every
// FacetWrite member.
func TestFacetWriteViewRoundTrip(t *testing.T) {
	declared := cutting_garden_plugins.NodeTypeFacetWrites{
		Tag: "t",
		Writes: []cutting_garden_plugins.FacetWrite{{
			DimensionKey:      "d",
			Mode:              cutting_garden_plugins.FacetWriteOne,
			Field:             "f",
			IdentityAffecting: true,
			CreationRequired:  true,
			CompletionHint:    "hint",
			Values:            []string{"a", "b"},
		}},
	}

	data, err := json.Marshal(NodeTypeFacetWritesViewFrom(declared))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"tag":"t","writes":[{"dimension":"d","mode":"one","field":"f",` +
		`"identity_affecting":true,"creation_required":true,` +
		`"completion_hint":"hint","values":["a","b"]}]}`
	if string(data) != want {
		t.Errorf("marshal = %s\nwant      %s", data, want)
	}

	var view NodeTypeFacetWritesView
	if err := json.Unmarshal(data, &view); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := view.ToNodeTypeFacetWrites(); !reflect.DeepEqual(got, declared) {
		t.Errorf("round trip = %+v, want %+v", got, declared)
	}
}
