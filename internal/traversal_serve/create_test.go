package traversal_serve

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// creatableInit is a forge-shaped initialize result: an issue type creatable
// under a repo, with state (one), milestone (one, clearable), label (many,
// the tag set) and a title trailer.
func creatableInit() InitializeResult {
	return InitializeResult{
		Capabilities: []string{CapMutate, CapContainerCreate},
		NodeTypes: []NodeTypeView{
			{Tag: "fj-repo-v1", Container: true},
			{
				Tag:          "fj-issue-v1",
				TagSet:       &TagSetView{Dimension: "label", Interpreter: "naive"},
				InlineFields: []string{"milestone"},
				TrailerField: "title",
				Creatable:    &CreatableView{Container: "fj-repo-v1", Required: []string{"title"}},
			},
		},
		Facets: []NodeTypeFacetsView{{
			Tag: "fj-issue-v1",
			Dimensions: []FacetDimensionView{
				{Key: "state", Kind: "categorical"},
				{Key: "milestone", Kind: "categorical"},
				{Key: "label", Kind: "categorical", Multi: true},
				{Key: "month", Kind: "categorical"},
			},
		}},
		FacetWrites: []NodeTypeFacetWritesView{{
			Tag: "fj-issue-v1",
			Writes: []FacetWriteView{
				{Dimension: "state", Mode: "one", Field: "state"},
				{Dimension: "milestone", Mode: "one", Field: "milestone", Clearable: true},
				{Dimension: "label", Mode: "many", Field: "labels"},
				{Dimension: "month", Mode: "none"},
			},
		}},
	}
}

func TestValidateCreationDeclaration(t *testing.T) {
	if err := ValidateCreationDeclaration(creatableInit()); err != nil {
		t.Fatalf("valid declaration refused: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*InitializeResult)
		want   string
	}{
		{
			name:   "container is not declared",
			mutate: func(i *InitializeResult) { i.NodeTypes[1].Creatable.Container = "fj-ghost-v1" },
			want:   `container "fj-ghost-v1" is not a declared node type`,
		},
		{
			name:   "container is a leaf",
			mutate: func(i *InitializeResult) { i.NodeTypes[1].Creatable.Container = "fj-issue-v1" },
			want:   "is not a container type",
		},
		{
			name:   "no container-create capability",
			mutate: func(i *InitializeResult) { i.Capabilities = []string{CapMutate} },
			want:   `does not advertise "container-create"`,
		},
		{
			name:   "no trailer_field",
			mutate: func(i *InitializeResult) { i.NodeTypes[1].TrailerField = "" },
			want:   "declares no trailer_field",
		},
		{
			name:   "required field the host cannot send",
			mutate: func(i *InitializeResult) { i.NodeTypes[1].Creatable.Required = []string{"assignee"} },
			want:   `field "assignee" is neither the trailer_field nor a dimension with a facet write`,
		},
		{
			name:   "required read-only dimension",
			mutate: func(i *InitializeResult) { i.NodeTypes[1].Creatable.Required = []string{"month"} },
			want:   `field "month" is read-only`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			init := creatableInit()
			tc.mutate(&init)
			err := ValidateCreationDeclaration(init)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateCreationDeclaration = %v, want %q", err, tc.want)
			}
		})
	}
}

// The host-built create body maps each document field through the declared
// facet write (one → string, many → array) or the trailer_field.
func TestPresentationBuildCreateBody(t *testing.T) {
	presentation := presentationOfInit(creatableInit())

	body, err := presentation.BuildCreateBody("fj-issue-v1", map[string][]string{
		"title":     {"Wrapped boxes lose their description"},
		"milestone": {"v0.3"},
		"label":     {"bug", "area-organize"},
		"state":     {"open"},
	})
	if err != nil {
		t.Fatalf("BuildCreateBody: %v", err)
	}
	want := `{"labels":["bug","area-organize"],"milestone":"v0.3","state":"open",` +
		`"title":"Wrapped boxes lose their description"}`
	if string(body) != want {
		t.Fatalf("body =\n%s\nwant\n%s", body, want)
	}

	for _, tc := range []struct {
		fields map[string][]string
		want   string
	}{
		{map[string][]string{"title": {"x"}, "month": {"2026-09"}}, `field "month" is read-only`},
		{map[string][]string{"title": {"x"}, "assignee": {"me"}}, `field "assignee" is neither`},
		{map[string][]string{"title": {"x"}, "state": {"a", "b"}}, `field "state" is single-valued, got 2`},
	} {
		_, err := presentation.BuildCreateBody("fj-issue-v1", tc.fields)
		if err == nil || !errors.Is400BadRequest(err) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("BuildCreateBody(%v) = %v, want a bad request containing %q", tc.fields, err, tc.want)
		}
	}
}

// A peer whose plugin has no IdempotentCreator still serves a keyed
// create_child — the key, and any param a newer host sends, is tolerated as
// unknown (plain JSON decode) — and answers with no `existed`.
func TestServerCreateChildToleratesKeyAndUnknownParams(t *testing.T) {
	plugin := &fakeFullPlugin{}
	client, _ := startServe(t, fullPluginConfig(plugin))
	ctx := context.Background()
	if err := client.Call(ctx, MethodInitialize,
		InitializeParams{ProtocolVersions: []string{SchemaV1}}, nil); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := client.Call(ctx, MethodNodeCreateChild, map[string]any{
		"container":       "mem://fixture/root",
		"type":            "mem-obj-v1",
		"body_base64":     base64.StdEncoding.EncodeToString([]byte("body")),
		"idempotency_key": "cgk1-0123",
		"a_future_param":  map[string]any{"nested": true},
	}, &raw); err != nil {
		t.Fatalf("create_child with extra params: %v", err)
	}
	if string(raw["created"]) != `"mem://fixture/root/assigned-1"` {
		t.Fatalf("created = %s", raw["created"])
	}
	if _, present := raw["existed"]; present {
		t.Fatalf("existed present from a non-idempotent peer: %s", raw["existed"])
	}
}

// keyedPlugin is fakeFullPlugin plus an IdempotentCreator remembering keys.
type keyedPlugin struct {
	*fakeFullPlugin
	byKey map[string]string
}

func (p *keyedPlugin) CreateChildWithKey(
	_ context.Context, container *url.URL, _ io.Reader, _ string, key string,
) (*url.URL, bool, error) {
	if uri, ok := p.byKey[key]; ok {
		u, err := url.Parse(uri)
		return u, true, err
	}
	uri := container.String() + "/" + key
	p.byKey[key] = uri
	u, err := url.Parse(uri)
	return u, false, err
}

// A keyed create over the wire reaches a Go peer's IdempotentCreator, and a
// repeat reports `existed` with the same URI — end to end through WirePlugin.
func TestWirePluginCreateChildWithKey(t *testing.T) {
	cfg := fullPluginConfig(&fakeFullPlugin{})
	cfg.Plugin = &keyedPlugin{fakeFullPlugin: cfg.Plugin.(*fakeFullPlugin), byKey: map[string]string{}}
	adapter, _ := newTestWirePlugin(t, memSpec(), cfg)
	ctx := context.Background()
	container := mustParseURL(t, "mem://fixture/root")

	first, existed, err := adapter.CreateChildWithKey(ctx, container, strings.NewReader("b"), "mem-obj-v1", "cgk1-k")
	if err != nil || existed || first.String() != "mem://fixture/root/cgk1-k" {
		t.Fatalf("first = %v, %v, %v", first, existed, err)
	}
	again, existed, err := adapter.CreateChildWithKey(ctx, container, strings.NewReader("b"), "mem-obj-v1", "cgk1-k")
	if err != nil || !existed || again.String() != first.String() {
		t.Fatalf("again = %v, %v, %v", again, existed, err)
	}
}

func TestCreationsOf(t *testing.T) {
	got := CreationsOf(creatableInit())
	if len(got) != 1 || got[0].Tag != "fj-issue-v1" || got[0].ContainerType != "fj-repo-v1" ||
		len(got[0].Required) != 1 || got[0].Required[0] != "title" {
		t.Fatalf("CreationsOf = %+v", got)
	}
}
