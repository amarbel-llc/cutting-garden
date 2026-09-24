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

// inlineAndTrailer declares the fake leaf's state (write:one) and month
// (write:none) dimensions as inline atoms, in that order, and "title" as the
// trailer field (forge organize F11).
func inlineAndTrailer() []NodeTypePresentation {
	return []NodeTypePresentation{{
		Tag:          fakeLeafType,
		InlineFields: []string{"state", "month"},
		TrailerField: "title",
	}}
}

// TestServerAdvertisesInlineFieldsAndTrailer pins the wire shape: the
// inline_fields list (declared order) and trailer_field ride the type's
// node_types entry.
func TestServerAdvertisesInlineFieldsAndTrailer(t *testing.T) {
	client, _ := startServe(t, presentingPluginConfig(inlineAndTrailer()))

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
		`"inline_fields":["state","month"],"trailer_field":"title"}]`
	if got != want {
		t.Errorf("node_types =\n%s\nwant\n%s", got, want)
	}
}

// TestWirePluginPresentsInlineFieldsAndTrailer pins the synthesized linked
// surface: FieldPresenter renders each inline dimension's facet value as a
// `name=value` atom in declared order (absent value, no atom); the listing
// fields declare each inline field (writable iff it has a one write) and the
// trailer (writable, the Trailer slot); and each listed node carries the
// projected Fields — inline values, and its name under the trailer field.
func TestWirePluginPresentsInlineFieldsAndTrailer(t *testing.T) {
	adapter, _ := newTestWirePlugin(t, memSpec(), presentingPluginConfig(inlineAndTrailer()))
	ctx := context.Background()

	nodes, err := adapter.ListRoots(ctx, mustParseURL(t, fakeRootURI))
	if err != nil {
		t.Fatalf("ListRoots: %v", err)
	}

	if got, want := adapter.PresentBoxAtoms(nodes[0]), []cutting_garden_plugins.BoxAtom{
		{Name: "state", Value: "open"}, {Name: "month", Value: "2026-07"},
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("a atoms = %+v, want %+v", got, want)
	}
	if got, want := adapter.PresentBoxAtoms(nodes[1]), []cutting_garden_plugins.BoxAtom{
		{Name: "state", Value: "closed"},
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("b atoms = %+v, want %+v", got, want)
	}

	if got, want := nodes[0].Fields, map[string]any{
		"state": "open", "month": "2026-07", "title": "a",
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("a Fields = %+v, want %+v", got, want)
	}

	if got, want := adapter.DescribeListingFields(), []cutting_garden_plugins.NodeTypeListingFields{{
		Tag: fakeLeafType,
		Fields: []cutting_garden_plugins.ListingField{
			{Key: "state", Writable: true},
			{Key: "month"},
			{Key: "title", Writable: true, Trailer: true},
		},
	}}; !reflect.DeepEqual(got, want) {
		t.Errorf("DescribeListingFields = %+v, want %+v", got, want)
	}
}

// TestWirePluginBuildsFieldWritePatch pins the host-built node.patch body
// for a box edit batch: the trailer writes {"<trailer_field>": "<text>"}, an
// inline atom writes through its dimension's one write exactly like a bucket
// move ({"<field>": "<value>"}, or null for a clear of a clearable write),
// all in ONE body. A read-only or undeclared atom, and a clear of a
// non-clearable write, are refused before the wire.
func TestWirePluginBuildsFieldWritePatch(t *testing.T) {
	cfg := presentingPluginConfig(inlineAndTrailer())
	adapter, dialer := newTestWirePlugin(t, memSpec(), cfg)
	ctx := context.Background()
	node := cutting_garden_plugins.Node{URI: mustParseURL(t, fakeLeafA), Type: fakeLeafType}

	body, err := adapter.BuildFieldWritePatch(ctx, node, []cutting_garden_plugins.FieldEdit{
		{Name: "state", Value: "closed"}, {Name: "title", Value: "Renamed a"},
	})
	if err != nil {
		t.Fatalf("BuildFieldWritePatch: %v", err)
	}
	if got, want := string(body), `{"state":"closed","title":"Renamed a"}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}

	writesBefore := dialer.writeCount()
	for _, tc := range []struct {
		name  string
		edits []cutting_garden_plugins.FieldEdit
		want  string
	}{
		{"read-only inline field", []cutting_garden_plugins.FieldEdit{{Name: "month", Value: "2026-08"}}, `"month"`},
		{"undeclared field", []cutting_garden_plugins.FieldEdit{{Name: "location", Value: "x"}}, `"location"`},
		{"non-clearable clear", []cutting_garden_plugins.FieldEdit{{Name: "state", Value: ""}}, "cannot be cleared"},
		{"empty batch", nil, "no edits"},
	} {
		_, err := adapter.BuildFieldWritePatch(ctx, node, tc.edits)
		if err == nil || !errors.Is400BadRequest(err) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want a bad request containing %q", tc.name, err, tc.want)
		}
	}
	if got := dialer.writeCount(); got != writesBefore {
		t.Errorf("refused builds sent %d wire message(s), want none", got-writesBefore)
	}
}

// TestWirePluginBuildsClearForClearableInlineField pins the emptied-atom
// clear: an inline field whose one write is clearable writes null.
func TestWirePluginBuildsClearForClearableInlineField(t *testing.T) {
	cfg := presentingPluginConfig(inlineAndTrailer())
	plugin := cfg.Plugin.(*fakePresentingPlugin)
	writes := conformingWrites()
	writes[0].Writes[0].Clearable = true
	plugin.writes = writes
	adapter, _ := newTestWirePlugin(t, memSpec(), cfg)

	body, err := adapter.BuildFieldWritePatch(
		context.Background(),
		cutting_garden_plugins.Node{URI: mustParseURL(t, fakeLeafA), Type: fakeLeafType},
		[]cutting_garden_plugins.FieldEdit{{Name: "state", Value: ""}},
	)
	if err != nil {
		t.Fatalf("BuildFieldWritePatch: %v", err)
	}
	if got, want := string(body), `{"state":null}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

// TestWirePluginRejectsUnusableInlineFieldsAndTrailer pins bring-up
// validation of inline_fields / trailer_field.
func TestWirePluginRejectsUnusableInlineFieldsAndTrailer(t *testing.T) {
	leaf := func(inline []string, trailer string) []NodeTypePresentation {
		return []NodeTypePresentation{{
			Tag: fakeLeafType, InlineFields: inline, TrailerField: trailer,
		}}
	}

	for _, tc := range []struct {
		name  string
		cfg   ServeConfig
		spec  PluginSpec
		wants []string
	}{
		{
			name:  "undeclared inline field",
			cfg:   presentingPluginConfig(leaf([]string{"priority"}, "")),
			spec:  memSpec(),
			wants: []string{`inline fields: type "mem-obj-v1" field "priority" is not a declared facet dimension`},
		},
		{
			name:  "multi-valued inline field",
			cfg:   presentingPluginConfig(leaf([]string{"label"}, "")),
			spec:  memSpec(),
			wants: []string{`inline fields: type "mem-obj-v1" field "label" is multi-valued`},
		},
		{
			name:  "duplicate inline field",
			cfg:   presentingPluginConfig(leaf([]string{"state", "state"}, "")),
			spec:  memSpec(),
			wants: []string{`inline fields: type "mem-obj-v1" field "state" is listed more than once`},
		},
		{
			name:  "trailer names a facet dimension",
			cfg:   presentingPluginConfig(leaf(nil, "state")),
			spec:  memSpec(),
			wants: []string{`trailer field: type "mem-obj-v1" field "state" is a facet dimension`},
		},
		{
			name: "trailer without mutate",
			cfg: ServeConfig{
				Plugin: presentingReadOnlyPlugin{},
				Info:   PluginInfo{Name: "fake-min", Version: "0.0.1"},
			},
			spec:  minSpec(),
			wants: []string{`trailer field: type "min-obj-v1" field "title" is writable but the plugin does not advertise "mutate"`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter, _ := newTestWirePlugin(t, tc.spec, tc.cfg)
			_, err := adapter.ListRoots(
				context.Background(), mustParseURL(t, tc.spec.Schemes[0]+"://host/root"),
			)
			if err == nil {
				t.Fatal("expected initialize to be rejected")
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

// presentingReadOnlyPlugin declares a trailer field without advertising
// mutate — the trailer is writable by declaration, so bring-up refuses it.
type presentingReadOnlyPlugin struct{ fakeMinimalPlugin }

func (presentingReadOnlyPlugin) DescribePresentation() []NodeTypePresentation {
	return []NodeTypePresentation{{Tag: "min-obj-v1", TrailerField: "title"}}
}
