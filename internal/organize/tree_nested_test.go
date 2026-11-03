package organize

import (
	"strings"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// The nested layout of `(tree)` (cutting-garden#353, S3): one continuation
// heading per hyphen segment, boxed when a node has exactly that prefix name and
// synthetic when none does, no collapsing of single-child chains, leaves before
// sub-headings, childless top-level nodes above the first heading.

// treeNodes are the fixture's shapes in miniature, in selection (file) order:
// a family with leaf members, a family whose members nest through synthetic
// segments, a childless top-level node, a member with children (one of which has
// a child of its own, listed between its siblings), and a hyphenated level-1 name.
func treeNodes(t *testing.T) []cgp.Node {
	pairs := [][2]string{
		{"pri", "priority"},
		{"pri0", "priority-0_must"},
		{"pri1", "priority-1_should"},
		{"trips", "trips"},
		{"t1", "trips-26-01-a"},
		{"t2", "trips-26-02-b"},
		{"t3", "trips-26-02-c"},
		{"inbox", "inbox"},
		{"proj", "proj"},
		{"alpha", "proj-alpha"},
		{"gamma", "proj-gamma"},
		{"one", "proj-gamma-one"},
		{"two", "proj-gamma-two"},
		{"deep", "proj-gamma-two-deep"},
		{"beta", "proj-beta"},
		{"zz", "zz-inbox"},
	}
	nodes := make([]cgp.Node, 0, len(pairs))
	for _, p := range pairs {
		nodes = append(nodes, treeNode(t, p[0], p[1]))
	}
	return nodes
}

// bodyOf is a rendered document's body: everything after the closing fence.
func bodyOf(t *testing.T, doc document) string {
	t.Helper()
	out := render(doc)
	_, body, ok := strings.Cut(strings.TrimPrefix(out, "---\n"), "\n---\n")
	if !ok {
		t.Fatalf("no closing fence in:\n%s", out)
	}
	return body
}

func buildTree(t *testing.T, nest string, nodes []cgp.Node) document {
	t.Helper()
	spec := groupSpec{Kind: groupKindTree, Nest: nest}
	doc, err := buildDocument(nodes, "fake://cal/", "", spec, treeLister(t, nodes...), nil, tagRender{}, nil)
	if err != nil {
		t.Fatalf("buildDocument: %v", err)
	}
	return doc
}

const nestedFull = `
- [inbox] inbox

# [pri] priority

- [pri0] -0_must
- [pri1] -1_should

# [trips] trips

## -26

### -01

- [t1] -a

### -02

- [t2] -b
- [t3] -c

# [proj] proj

- [alpha] -alpha
- [beta] -beta

## [gamma] -gamma

- [one] -one

### [two] -two

- [deep] -deep

# zz

- [zz] -inbox
`

func TestBuildTreeDocument_NestedFull(t *testing.T) {
	doc := buildTree(t, nestFull, treeNodes(t))
	if got := bodyOf(t, doc); got != nestedFull {
		t.Errorf("nested layout:\n--- got\n%s\n--- want\n%s", got, nestedFull)
	}
	// An unset Nest is the built-in default: full.
	if got := bodyOf(t, buildTree(t, "", treeNodes(t))); got != nestedFull {
		t.Errorf("an empty Nest must lay out as full:\n%s", got)
	}
}

// `_nest = 1`: only the family heading nests; everything below it is a line
// whose text is the name relative to the family, in selection order.
const nestedOne = `
- [inbox] inbox

# [pri] priority

- [pri0] -0_must
- [pri1] -1_should

# [trips] trips

- [t1] -26-01-a
- [t2] -26-02-b
- [t3] -26-02-c

# [proj] proj

- [alpha] -alpha
- [gamma] -gamma
- [one] -gamma-one
- [two] -gamma-two
- [deep] -gamma-two-deep
- [beta] -beta

# zz

- [zz] -inbox
`

func TestBuildTreeDocument_NestOne(t *testing.T) {
	doc := buildTree(t, nestOne, treeNodes(t))
	if got := bodyOf(t, doc); got != nestedOne {
		t.Errorf("nest 1:\n--- got\n%s\n--- want\n%s", got, nestedOne)
	}
}

// A boxed heading is boxed only because its node is IN the selection: when the
// family's own node is filtered out, its heading degrades to a synthetic one
// and its members keep their names.
func TestBuildTreeDocument_FilteredParentDegradesToSyntheticHeading(t *testing.T) {
	var nodes []cgp.Node
	for _, n := range treeNodes(t) {
		if n.URI.Path != "/trips" {
			nodes = append(nodes, n)
		}
	}
	want := strings.Replace(nestedFull, "# [trips] trips", "# trips", 1)
	if got := bodyOf(t, buildTree(t, nestFull, nodes)); got != want {
		t.Errorf("a filtered-out parent must render as a synthetic heading:\n%s", got)
	}
}

// Every layout of one set of names parses back to that set, and rendering what
// was parsed is a fixed point.
func TestNestedLayout_RoundTrips(t *testing.T) {
	nodes := treeNodes(t)
	for _, nest := range []string{nestFull, nestOne, nestNone} {
		doc := buildTree(t, nest, nodes)
		out := render(doc)
		parsed, err := parseDocument(out)
		if err != nil {
			t.Fatalf("nest %s: re-parse: %v\n%s", nest, err, out)
		}
		if render(parsed) != out {
			t.Errorf("nest %s: render is not a fixed point:\n%s\n---\n%s", nest, out, render(parsed))
		}
		got := map[string]string{}
		for _, e := range parsed.treeEntries() {
			got[e.ID] = e.Desc
		}
		for _, n := range nodes {
			id := strings.TrimPrefix(n.URI.Path, "/")
			want, _ := n.Fields["name"].(string)
			if got[id] != want {
				t.Errorf("nest %s: %s parsed as %q, want %q", nest, id, got[id], want)
			}
		}
		if len(got) != len(nodes) {
			t.Errorf("nest %s: %d entries, want %d", nest, len(got), len(nodes))
		}
	}
	// The three layouts share one canonical form, hence one base.
	full, one, none := buildTree(t, nestFull, nodes), buildTree(t, nestOne, nodes), buildTree(t, nestNone, nodes)
	if renderCanonical(full) != renderCanonical(one) || renderCanonical(full) != renderCanonical(none) {
		t.Error("full, 1 and none layouts of one name set must share a canonical form")
	}
}
