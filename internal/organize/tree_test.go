package organize

import (
	"strings"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The `(tree)` grouping (cutting-garden#353): the nodes of a tag-vocabulary file
// laid out by their own full hyphen names. These tests pin what exists in the
// read-only slices: parsing the spelling, generating the flat layout, the
// canonical NAME-SET form that makes nesting and `_nest` spellings equal, and
// the apply refusals that point at #380.

func TestParseGroupSpec_Tree(t *testing.T) {
	spec, err := parseGroupSpec("(tree)", nil, nil, "")
	if err != nil {
		t.Fatalf("parseGroupSpec((tree)): %v", err)
	}
	if spec.Kind != groupKindTree || !spec.grouped() {
		t.Fatalf("spec = %+v, want a grouped tree spec", spec)
	}
	if spec.String() != "(tree)" || spec.groupByEncoding() != "(tree)" {
		t.Errorf("spelling = %q / %q, want (tree)", spec.String(), spec.groupByEncoding())
	}
	back, err := parseGroupByEncoding("(tree)")
	if err != nil || back.Kind != groupKindTree {
		t.Errorf("parseGroupByEncoding((tree)) = %+v, %v", back, err)
	}

	_, err = parseGroupSpec("(foo)", nil, nil, "")
	if err == nil {
		t.Fatal("an unknown qualifier must be refused")
	}
	for _, frag := range []string{"(tags)", "(tree)"} {
		if !strings.Contains(err.Error(), frag) {
			t.Errorf("error %q must name %s among the qualifiers", err, frag)
		}
	}
}

// namedLister is fakeLister plus a listing declaration, with `name` the hyphen
// name field of the node type `h`.
type namedLister struct {
	*fakeLister
	listing []cgp.NodeTypeListingFields
}

func (l namedLister) DescribeListingFields() []cgp.NodeTypeListingFields { return l.listing }

func treeLister(t *testing.T, nodes ...cgp.Node) namedLister {
	t.Helper()
	return namedLister{
		fakeLister: &fakeLister{nodes: nodes},
		listing: []cgp.NodeTypeListingFields{{Tag: "h", Fields: []cgp.ListingField{
			{Key: "title", Trailer: true},
			{Key: "name", Name: true},
		}}},
	}
}

func treeNode(t *testing.T, id, name string) cgp.Node {
	fields := map[string]any{"title": "x"}
	if name != "" {
		fields["name"] = name
	}
	return cgp.Node{URI: mustURL(t, "fake://cal/"+id), Type: "h", Fields: fields}
}

func TestBuildTreeDocument_FlatLayoutOfFullNames(t *testing.T) {
	nodes := []cgp.Node{
		treeNode(t, "t0", "trips"),
		treeNode(t, "t1", "trips-26-01-a"),
		treeNode(t, "inbox", "inbox"),
	}
	spec := groupSpec{Kind: groupKindTree}
	doc, err := buildDocument(nodes, "fake://cal/", "", spec, treeLister(t, nodes...), nil, tagRender{}, nil)
	if err != nil {
		t.Fatalf("buildDocument: %v", err)
	}
	if doc.GroupBy != "(tree)" || doc.Type != "h" || len(doc.Sections) != 0 {
		t.Fatalf("doc = %+v, want a flat (tree) document of type h", doc)
	}
	var got []string
	for _, ln := range doc.Ungrouped {
		if len(ln.Tags) != 0 || len(ln.Fields) != 0 || ln.Type != "" {
			t.Errorf("a tree box carries only its id: %+v", ln)
		}
		got = append(got, ln.ID+"="+ln.Desc)
	}
	want := []string{"t0=trips", "t1=trips-26-01-a", "inbox=inbox"} // selection order
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("lines = %v, want %v", got, want)
	}
}

func TestBuildTreeDocument_Refusals(t *testing.T) {
	spec := groupSpec{Kind: groupKindTree}
	build := func(l namedLister, nodes []cgp.Node) error {
		_, err := buildDocument(nodes, "fake://cal/", "", spec, l, nil, tagRender{}, nil)
		return err
	}

	t.Run("a node with no hyphen name", func(t *testing.T) {
		nodes := []cgp.Node{treeNode(t, "ok", "ok"), treeNode(t, "bare", "")}
		err := build(treeLister(t, nodes...), nodes)
		if err == nil || !errors.Is400BadRequest(err) {
			t.Fatalf("err = %v, want a bad request", err)
		}
		for _, frag := range []string{"bare", "hyphen name", "tag_files"} {
			if !strings.Contains(err.Error(), frag) {
				t.Errorf("error %q lacks %q", err, frag)
			}
		}
	})

	t.Run("two nodes with one full name", func(t *testing.T) {
		nodes := []cgp.Node{treeNode(t, "a", "trips-26"), treeNode(t, "b", "trips-26")}
		err := build(treeLister(t, nodes...), nodes)
		if err == nil || !errors.Is400BadRequest(err) {
			t.Fatalf("err = %v, want a bad request", err)
		}
		for _, frag := range []string{"trips-26", "a", "b"} {
			if !strings.Contains(err.Error(), frag) {
				t.Errorf("error %q lacks %q", err, frag)
			}
		}
	})

	t.Run("a plugin declaring no name field", func(t *testing.T) {
		nodes := []cgp.Node{treeNode(t, "a", "a")}
		l := treeLister(t, nodes...)
		l.listing = []cgp.NodeTypeListingFields{{Tag: "h", Fields: []cgp.ListingField{{Key: "title"}}}}
		err := build(l, nodes)
		if err == nil || !errors.Is400BadRequest(err) {
			t.Fatalf("err = %v, want a bad request", err)
		}
		if !strings.Contains(err.Error(), "name field") {
			t.Errorf("error %q must say the plugin declares no name field", err)
		}
	})

	t.Run("a plugin with no listing declaration at all", func(t *testing.T) {
		nodes := []cgp.Node{treeNode(t, "a", "a")}
		err := build(namedLister{fakeLister: &fakeLister{nodes: nodes}}, nodes)
		if err == nil || !strings.Contains(err.Error(), "name field") {
			t.Fatalf("err = %v, want the no-name-field refusal", err)
		}
	})
}

const treeFlatBody = `
- [t0] trips
- [t1] trips-26-01
- [a] trips-26-01-a
- [inbox] inbox
`

const treeNestedBody = `
- [inbox] inbox

# [t0] trips

## -26

### [t1] -01

- [a] -a
`

func treeDoc2(t *testing.T, extra, body string) document {
	t.Helper()
	return mustParse(t, "---\n- _base = @blake2b256-acdef9\n- _anchor = fake://cal/\n- _type = !h\n"+
		"- _group-by = (tree)\n"+extra+"! organize-base-v1\n---\n"+body)
}

// THE property: the canonical form is the sorted NAME SET, so the nested and
// flat spellings of one set of names are the same base.
func TestTreeCanonical_NameSetIsLayoutIndependent(t *testing.T) {
	flat := renderCanonical(treeDoc2(t, "- _nest = none\n", treeFlatBody))
	nested := renderCanonical(treeDoc2(t, "", treeNestedBody))
	if flat != nested {
		t.Fatalf("nested and flat spellings must share a canonical form:\n--- flat\n%s\n--- nested\n%s", flat, nested)
	}
	if strings.Contains(flat, "#") || strings.Contains(flat, "_nest") {
		t.Errorf("the canonical form has no headings and no _nest:\n%s", flat)
	}
	order := []string{"] inbox", "] trips\n", "] trips-26-01\n", "] trips-26-01-a"}
	last := -1
	for _, frag := range order {
		i := strings.Index(flat, frag)
		if i < 0 || i < last {
			t.Fatalf("lines must be sorted by full name (inbox, trips, trips-26-01, trips-26-01-a):\n%s", flat)
		}
		last = i
	}
}

// A synthetic heading is no node: it contributes nothing to the name set.
func TestTreeEntries_SyntheticHeadingsContributeNothing(t *testing.T) {
	entries := treeDoc2(t, "", treeNestedBody).treeEntries()
	got := map[string]string{}
	for _, e := range entries {
		got[e.ID] = e.Desc
	}
	want := map[string]string{
		"inbox": "inbox", "t0": "trips", "t1": "trips-26-01", "a": "trips-26-01-a",
	}
	if len(got) != len(want) {
		t.Fatalf("entries = %v, want exactly %v (the `## -26` heading has no node)", got, want)
	}
	for id, name := range want {
		if got[id] != name {
			t.Errorf("entry %s = %q, want %q", id, got[id], name)
		}
	}
}

func TestBodyMatchesBase_Tree(t *testing.T) {
	base := renderCanonical(treeDoc2(t, "- _nest = none\n", treeFlatBody))
	for name, tc := range map[string]struct {
		doc   document
		clean bool
	}{
		"the same names, nested":         {treeDoc2(t, "", treeNestedBody), true},
		"the same names, flat":           {treeDoc2(t, "- _nest = none\n", treeFlatBody), true},
		"one node renamed":               {treeDoc2(t, "", strings.Replace(treeNestedBody, "-a", "-z", 1)), false},
		"a node dropped":                 {treeDoc2(t, "", strings.Replace(treeNestedBody, "- [a] -a\n", "", 1)), false},
		"the same names, tag lever edit": {treeDoc2(t, "- _tag-atoms = trailing\n", treeNestedBody), false},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := bodyMatchesBase(tc.doc, base)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.clean {
				t.Errorf("bodyMatchesBase = %v, want %v", got, tc.clean)
			}
		})
	}
}

// The read-only contract: the difference between edited and base name sets is
// refused with a message pointing at #380, never planned and never silent.
func TestRefuseTreeEdits(t *testing.T) {
	base := treeDoc2(t, "- _nest = none\n", treeFlatBody)
	for _, tc := range []struct {
		name string
		body string
		want []string // nil: accepted
	}{
		{name: "unchanged", body: treeFlatBody},
		{name: "respelled nested", body: treeNestedBody},
		{
			name: "a node renamed",
			body: strings.Replace(treeFlatBody, "trips-26-01-a", "trips-26-01-z", 1),
			want: []string{"[a]", "renamed", "trips-26-01-a", "trips-26-01-z", "#380"},
		},
		{
			name: "a node deleted",
			body: strings.Replace(treeFlatBody, "- [a] trips-26-01-a\n", "", 1),
			want: []string{"[a]", "deleted", "#380"},
		},
		{
			name: "an id the base does not hold",
			body: treeFlatBody + "- [zz] zz-new\n",
			want: []string{"[zz]", "unknown", "#380"},
		},
		{
			name: "a creation",
			body: treeFlatBody + "- [+n] fresh\n",
			want: []string{"creat", "#380"},
		},
		{
			name: "a nested respelling that renames",
			body: strings.Replace(treeNestedBody, "### [t1] -01", "### [t1] -02", 1),
			want: []string{"[t1]", "renamed", "#380"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := refuseTreeEdits(base, treeDoc2(t, "", tc.body))
			if tc.want == nil {
				if err != nil {
					t.Fatalf("refuseTreeEdits = %v, want nil", err)
				}
				return
			}
			if err == nil || !errors.Is400BadRequest(err) {
				t.Fatalf("refuseTreeEdits = %v, want a bad request", err)
			}
			for _, frag := range tc.want {
				if !strings.Contains(err.Error(), frag) {
					t.Errorf("error %q lacks %q", err, frag)
				}
			}
		})
	}
}
