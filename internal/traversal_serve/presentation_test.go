package traversal_serve

import (
	"context"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// fakePresentingPlugin is fakeWritablePlugin (a multi-valued "label"
// dimension written through a many write) plus a presentation declaration
// and label memberships on its leaves — the RFC 0013 presentation
// additions' test subject (forge organize F11).
type fakePresentingPlugin struct {
	*fakeWritablePlugin
	presentations []NodeTypePresentation
}

var _ PresentationDescriber = (*fakePresentingPlugin)(nil)

func (p *fakePresentingPlugin) DescribePresentation() []NodeTypePresentation {
	return p.presentations
}

func (p *fakePresentingPlugin) ListRoots(
	ctx context.Context, node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	nodes, err := p.fakeWritablePlugin.ListRoots(ctx, node)
	for i := range nodes {
		if nodes[i].URIString() == fakeLeafA {
			nodes[i].Facets["label"] = []cutting_garden_plugins.FacetValue{
				{Key: "ui"}, {Key: "good first issue"},
			}
		}
	}
	return nodes, err
}

// ListEnriched routes through the overriding ListRoots, so the filtered
// listing carries the same label memberships.
func (p *fakePresentingPlugin) ListEnriched(
	ctx context.Context, node *url.URL, filter cutting_garden_plugins.FacetFilter,
) ([]cutting_garden_plugins.Node, bool, error) {
	all, err := p.ListRoots(ctx, node)
	if err != nil || node.String() != fakeRootURI {
		return nil, false, err
	}
	var matched []cutting_garden_plugins.Node
	for _, child := range all {
		if filter.Matches(child.Facets) {
			matched = append(matched, child)
		}
	}
	return matched, true, nil
}

func labelTagSet(interpreter string) []NodeTypePresentation {
	return []NodeTypePresentation{{
		Tag:    fakeLeafType,
		TagSet: &TagSetView{Dimension: "label", Interpreter: interpreter},
	}}
}

func presentingPluginConfig(presentations []NodeTypePresentation) ServeConfig {
	return ServeConfig{
		Plugin: &fakePresentingPlugin{
			fakeWritablePlugin: &fakeWritablePlugin{
				fakeFullPlugin: &fakeFullPlugin{},
				writes:         conformingWrites(),
			},
			presentations: presentations,
		},
		Info: PluginInfo{Name: "fake-mem", Version: "0.0.1"},
	}
}

// TestServerAdvertisesTagSetOnNodeTypes pins the wire shape a Go peer emits
// for a tag set: an optional tag_set member on the type's node_types entry,
// absent on every type declaring none.
func TestServerAdvertisesTagSetOnNodeTypes(t *testing.T) {
	client, _ := startServe(t, presentingPluginConfig(labelTagSet("dodder-hyphen")))

	var raw map[string]json.RawMessage
	if err := client.Call(
		context.Background(), MethodInitialize,
		InitializeParams{ProtocolVersions: []string{SchemaV1}}, &raw,
	); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	got := string(raw["node_types"])
	want := `[{"tag":"mem-dir-v1","container":true},` +
		`{"tag":"mem-obj-v1","container":false,"mime_type":"text/plain",` +
		`"tag_set":{"dimension":"label","interpreter":"dodder-hyphen"}}]`
	if got != want {
		t.Errorf("node_types =\n%s\nwant\n%s", got, want)
	}
}

// TestServeRefusesPresentationForUndeclaredType pins the Go-peer side: the
// presentation members ride a node_types entry, so a presentation naming a
// type the plugin does not declare has nowhere to go — Serve refuses to
// start rather than silently dropping it.
func TestServeRefusesPresentationForUndeclaredType(t *testing.T) {
	cfg := presentingPluginConfig([]NodeTypePresentation{{
		Tag:    "mem-ghost-v1",
		TagSet: &TagSetView{Dimension: "label", Interpreter: "naive"},
	}})

	_, err := newServer(cfg)
	if err == nil || !strings.Contains(
		err.Error(), `presentation: type "mem-ghost-v1" is not a declared node type`,
	) {
		t.Errorf("newServer err = %v, want the undeclared-type refusal", err)
	}
}

// TestWirePluginPresentsTagSetAsUnifiedTagField pins the host side: the
// adapter synthesizes a minimal unified declaration — ONE FieldTag field
// keyed by the tag_set dimension, carrying its interpreter and writable iff
// the dimension has a many write — and projects each listed node's
// memberships in that dimension into Node.Fields, so the framework's single
// tag path (PresentUnifiedTags over the codecs) presents them.
func TestWirePluginPresentsTagSetAsUnifiedTagField(t *testing.T) {
	adapter, _ := newTestWirePlugin(
		t, memSpec(), presentingPluginConfig(labelTagSet("dodder-hyphen")),
	)
	ctx := context.Background()

	sets := adapter.DescribeUnified()
	if len(sets) != 1 || sets[0].Tag != fakeLeafType || len(sets[0].Codecs) != 1 {
		t.Fatalf("DescribeUnified = %+v, want one tag codec on %s", sets, fakeLeafType)
	}
	fields := sets[0].Codecs[0].Fields()
	want := cutting_garden_plugins.UnifiedField{
		Key:         "label",
		Kind:        cutting_garden_plugins.FieldTag,
		Groupable:   true,
		MultiValued: true,
		Writable:    true,
		Interpreter: "dodder-hyphen",
	}
	if len(fields) != 1 || !reflect.DeepEqual(fields[0], want) {
		t.Errorf("tag field = %+v, want %+v", fields, want)
	}
	if err := cutting_garden_plugins.ValidateUnifiedFieldSets(sets); err != nil {
		t.Errorf("ValidateUnifiedFieldSets: %v", err)
	}

	nodes, err := adapter.ListRoots(ctx, mustParseURL(t, fakeRootURI))
	if err != nil {
		t.Fatalf("ListRoots: %v", err)
	}
	if got := cutting_garden_plugins.PresentUnifiedTags(sets[0].Codecs, nodes[0]); !reflect.DeepEqual(
		got, []string{"ui", "good first issue"},
	) {
		t.Errorf("a's presented tags = %v, want [ui, good first issue]", got)
	}
	if got := cutting_garden_plugins.PresentUnifiedTags(sets[0].Codecs, nodes[1]); got != nil {
		t.Errorf("b's presented tags = %v, want none", got)
	}

	enriched, ok, err := adapter.ListEnriched(
		ctx, mustParseURL(t, fakeRootURI),
		cutting_garden_plugins.FacetFilter{{Dimension: "state", Value: "open"}},
	)
	if err != nil || !ok || len(enriched) != 1 {
		t.Fatalf("ListEnriched = %+v, %t, %v", enriched, ok, err)
	}
	if got := cutting_garden_plugins.PresentUnifiedTags(sets[0].Codecs, enriched[0]); len(got) != 2 {
		t.Errorf("enriched a's presented tags = %v, want two", got)
	}
}

// TestWirePluginTagSetOnReadOnlyDimensionIsNotWritable pins that a tag set
// whose dimension has no many write still presents (renders), but its
// synthesized field is read-only — organize then refuses tag edits loudly.
func TestWirePluginTagSetOnReadOnlyDimensionIsNotWritable(t *testing.T) {
	cfg := presentingPluginConfig(labelTagSet("naive"))
	plugin := cfg.Plugin.(*fakePresentingPlugin)
	plugin.writes = []cutting_garden_plugins.NodeTypeFacetWrites{{
		Tag: fakeLeafType,
		Writes: []cutting_garden_plugins.FacetWrite{
			{DimensionKey: "label", Mode: cutting_garden_plugins.FacetWriteNone},
		},
	}}
	adapter, _ := newTestWirePlugin(t, memSpec(), cfg)

	sets := adapter.DescribeUnified()
	if len(sets) != 1 {
		t.Fatalf("DescribeUnified = %+v", sets)
	}
	field := sets[0].Codecs[0].Fields()[0]
	if field.Writable || field.Interpreter != "naive" {
		t.Errorf("read-only tag field = %+v, want Writable false, naive", field)
	}
}

// TestWirePluginWithoutPresentationDescribesNothing pins back-compat: a peer
// declaring no tag set yields no unified declaration and no projected
// fields — the "plugin omits the interface" outcome.
func TestWirePluginWithoutPresentationDescribesNothing(t *testing.T) {
	adapter, _ := newTestWirePlugin(
		t, memSpec(), writablePluginConfig(conformingWrites()),
	)

	if got := adapter.DescribeUnified(); got != nil {
		t.Errorf("DescribeUnified = %+v, want nil", got)
	}
	nodes, err := adapter.ListRoots(context.Background(), mustParseURL(t, fakeRootURI))
	if err != nil {
		t.Fatalf("ListRoots: %v", err)
	}
	for _, node := range nodes {
		if node.Fields != nil {
			t.Errorf("%s Fields = %+v, want nil", node.URIString(), node.Fields)
		}
	}
}

// TestWirePluginRejectsUnusableTagSet pins bring-up validation of tag_set:
// each violation fails the plugin loudly, naming the plugin and the type.
func TestWirePluginRejectsUnusableTagSet(t *testing.T) {
	for _, tc := range []struct {
		name          string
		presentations []NodeTypePresentation
		wants         []string
	}{
		{
			name: "undeclared dimension",
			presentations: []NodeTypePresentation{{
				Tag:    fakeLeafType,
				TagSet: &TagSetView{Dimension: "nope", Interpreter: "naive"},
			}},
			wants: []string{
				`wire plugin "fake-mem": initialize rejected`,
				`tag set: type "mem-obj-v1" dimension "nope" is not a declared facet dimension`,
			},
		},
		{
			name: "single-valued dimension",
			presentations: []NodeTypePresentation{{
				Tag:    fakeLeafType,
				TagSet: &TagSetView{Dimension: "state", Interpreter: "naive"},
			}},
			wants: []string{`tag set: type "mem-obj-v1" dimension "state" is not multi-valued`},
		},
		{
			name:          "unknown interpreter",
			presentations: labelTagSet("klingon"),
			wants: []string{
				`tag set: type "mem-obj-v1" names interpreter "klingon", which this host does not know`,
			},
		},
		{
			name:          "missing interpreter",
			presentations: labelTagSet(""),
			wants:         []string{`tag set: type "mem-obj-v1" names no interpreter`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter, dialer := newTestWirePlugin(t, memSpec(), presentingPluginConfig(tc.presentations))

			_, err := adapter.ListRoots(context.Background(), mustParseURL(t, fakeRootURI))
			if err == nil {
				t.Fatal("expected initialize to be rejected")
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
			if got := adapter.DescribeUnified(); got != nil {
				t.Errorf("DescribeUnified after rejection = %+v, want nil", got)
			}
			if got := dialer.dialCount(); got != 1 {
				t.Errorf("dials = %d, want 1", got)
			}
		})
	}
}
