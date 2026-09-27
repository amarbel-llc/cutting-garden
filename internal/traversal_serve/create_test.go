package traversal_serve

import (
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

func TestCreationsOf(t *testing.T) {
	got := CreationsOf(creatableInit())
	if len(got) != 1 || got[0].Tag != "fj-issue-v1" || got[0].ContainerType != "fj-repo-v1" ||
		len(got[0].Required) != 1 || got[0].Required[0] != "title" {
		t.Fatalf("CreationsOf = %+v", got)
	}
}
