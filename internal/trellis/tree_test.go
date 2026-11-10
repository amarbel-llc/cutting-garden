package trellis

import (
	"reflect"
	"strings"
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The literal tree (RFC 0014 "Isometry" and "Stencil fragment"; the PEG's
// Literal entry): one TreeNode per root, children from `[-> …]`, nesting to
// any depth, the id slot admitting a real id, the null id `_`, a temp id and a
// locked id. Every case is also a write -> re-parse round trip.

func eqPred(name, value string) TreeAtom {
	return TreeAtom{Pred: FieldPred{
		Field: FieldName{Name: name}, Op: FieldOpEq, Values: []Value{Bareword{Name: value}},
	}}
}

func TestParseTree_IDSlot(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want TreeNode
	}{
		{"real id", `[work !task]`, TreeNode{ID: "work", Type: "task"}},
		{"null id", `[_ !task]`, TreeNode{Kind: IDNull, Type: "task"}},
		{"an object named _", `["_" !task]`, TreeNode{ID: "_", Type: "task"}},
		{"an identifier starting with _", `[_foo !task]`, TreeNode{ID: "_foo", Type: "task"}},
		{"bare temp id", `[+ !task]`, TreeNode{Kind: IDTemp, Type: "task"}},
		{"temp id with a handle", `[+wrap-bug !task]`, TreeNode{Kind: IDTemp, ID: "wrap-bug", Type: "task"}},
		{"temp id with a quoted handle", `[+"wrap bug" !task]`, TreeNode{Kind: IDTemp, ID: "wrap bug", Type: "task"}},
		{
			"locked id", `[run_recipe@blake2b256-s3 !mcp-tool-v1]`,
			TreeNode{ID: "run_recipe", Lock: "blake2b256-s3", Type: "mcp-tool-v1"},
		},
		{
			"globstar id", `[file:/wt/** !fs-path-v1]`,
			TreeNode{ID: "file:/wt", Globstar: true, Type: "fs-path-v1"},
		},
		{"quoted id is never a globstar", `["file:/wt/**"]`, TreeNode{ID: "file:/wt/**"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertTreeRoundTrip(t, tc.src, []TreeNode{tc.want})
		})
	}
}

func TestParseTree_Nesting(t *testing.T) {
	src := `[moxy !clown-plugin-v1 required
  [-> folio !mcp-server-v1
    [-> read !mcp-tool-v1 kind=read perm=allow
      [-> file:/wt !fs-path-v1 subtree]
      [-> _ !tool-input-v1 ^field=repo value!=[a, "b c"] open]]]]

[just-us !clown-plugin-v1]`
	negField := TreeAtom{Negate: true, Pred: FieldPred{
		Field: FieldName{Name: "field"}, Op: FieldOpEq, Values: []Value{Bareword{Name: "repo"}},
	}}
	valueList := TreeAtom{Pred: FieldPred{
		Field: FieldName{Name: "value"}, Op: FieldOpNotEq, List: true,
		Values: []Value{Bareword{Name: "a"}, StringValue{Value: "b c"}},
	}}
	want := []TreeNode{
		{ID: "moxy", Type: "clown-plugin-v1", Tags: []string{"required"}, Children: []TreeNode{
			{ID: "folio", Type: "mcp-server-v1", Children: []TreeNode{
				{
					ID: "read", Type: "mcp-tool-v1",
					Atoms: []TreeAtom{eqPred("kind", "read"), eqPred("perm", "allow")},
					Children: []TreeNode{
						{ID: "file:/wt", Type: "fs-path-v1", Tags: []string{"subtree"}},
						{
							Kind: IDNull, Type: "tool-input-v1", Tags: []string{"open"},
							Atoms: []TreeAtom{negField, valueList},
						},
					},
				},
			}},
		}},
		{ID: "just-us", Type: "clown-plugin-v1"},
	}
	assertTreeRoundTrip(t, src, want)
}

func TestWriteTree_Canonical(t *testing.T) {
	// Tags before atoms before children, whatever the source order; one
	// space between terms; each child on its own line, two spaces deeper.
	roots, err := ParseTree(`[a k=v t1 [-> b] !ty t2]  [c]`)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	WriteTree(&b, roots)
	want := "[a !ty t1 t2 k=v\n  [-> b]]\n\n[c]\n"
	if b.String() != want {
		t.Fatalf("WriteTree = %q, want %q", b.String(), want)
	}
}

func TestParseTree_Headings(t *testing.T) {
	headings := `# [moxy !clown-plugin-v1]
## [folio !mcp-server-v1]
### [read !mcp-tool-v1 kind=read perm=allow]
- [file:/wt !fs-path-v1 subtree]
- [file:/wt/secrets !fs-path-v1
    subtree deny]
# [just-us !clown-plugin-v1]
## [just-us !mcp-server-v1]
### [run_recipe@blake2b256-s3 !mcp-tool-v1]
- [_ !just-recipe-v1 group=[test, lint]]
`
	nested := `[moxy !clown-plugin-v1
  [-> folio !mcp-server-v1
    [-> read !mcp-tool-v1 kind=read perm=allow
      [-> file:/wt !fs-path-v1 subtree]
      [-> file:/wt/secrets !fs-path-v1 subtree deny]]]]
[just-us !clown-plugin-v1
  [-> just-us !mcp-server-v1
    [-> run_recipe@blake2b256-s3 !mcp-tool-v1
      [-> _ !just-recipe-v1 group=[test, lint]]]]]`
	assertSameTree(t, headings, nested)
}

func TestParseTree_HeadingBuckets(t *testing.T) {
	// A bare heading is a `_` bucket carrying the heading's terms (the one-
	// isometry sketch §4); depth normalizes to the shallowest heading; an
	// empty heading closes every open heading.
	headings := `## status=needs-action
- [task1.ics]
#### work
- [task2.ics]
##
- [task3.ics]
`
	nested := `[_ status=needs-action [-> task1.ics] [-> _ work [-> task2.ics]]]
[task3.ics]`
	assertSameTree(t, headings, nested)
}

// TestParseTree_Refusals pins that every form outside the literal tree is a
// bad request naming the offending term.
func TestParseTree_Refusals(t *testing.T) {
	cases := []struct {
		src      string
		wantTerm string
	}{
		{`[!task]`, "!task"},
		{`[]`, "id slot"},
		{`[^a]`, "^a"},
		{`[=a]`, "=a"},
		{`[todo: !t]`, "todo:"},
		{`[a !t:]`, "!t:"},
		{`[a !t !u]`, "!u"},
		{`[a [b, c]]`, "OR-alternatives"},
		{`[a [: x]]`, "version subpath"},
		{`[a (tags)]`, "(tags)"},
		{`[a ^tag]`, "^tag"},
		{`[a x:]`, "x:"},
		{`[a b@blake2b256-s3]`, "b@blake2b256-s3"},
		{`[a [->> b]]`, "->>"},
		{`[a [<- b]]`, "<-"},
		{`[a [-[e]-> b]]`, "-[e]->"},
		{`[a [->]]`, "[->]"},
		{`[a [-> b -> c]]`, "more than one step"},
		{`[file:/**/x !fs-path-v1]`, "file:/**/x"},
		{`[file:/wt** !fs-path-v1]`, "file:/wt**"},
		{`[a @blake2b256-s3 @blake2b256-c3]`, "@blake2b256-c3"},
		{`[a`, "["},
		{`x`, "x"},
		{"# [a]\nnot a line", "not a line"},
		{"- [a] trailer", "trailer"},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			_, err := ParseTree(tc.src)
			if err == nil {
				t.Fatalf("ParseTree(%q) succeeded; want a bad request", tc.src)
			}
			if !errors.Is400BadRequest(err) {
				t.Fatalf("ParseTree(%q) error = %v; want a bad request", tc.src, err)
			}
			if !strings.Contains(err.Error(), tc.wantTerm) {
				t.Fatalf("error = %v; want it to name %q", err, tc.wantTerm)
			}
		})
	}
}

// TestParseTree_EspalierVectors pins that the PEG's literal conformance
// vectors and the espalier data literal parse as trees.
func TestParseTree_EspalierVectors(t *testing.T) {
	for _, src := range []string{
		`[+ !caldav-object-vtodo-v1 summary="buy milk"]`,
		`[+wrap-bug !task]`,
		`[+"wrap bug" !task]`,
		`[_ !just-recipe-v1 group=[test, lint]]`,
		`["_" !task]`,
		`[_foo !task]`,
		`[run_recipe@blake2b256-s3 !mcp-tool-v1 [-> _ !just-recipe-v1 group=[test]]]`,
		`[work !task -client [-> one/uno !md priority=1]]`,
		`[story-8841 !newsblur-story-v1 year=2026 [-> content-8841 @blake2b256-9ft3x]]`,
	} {
		t.Run(src, func(t *testing.T) {
			roots, err := ParseTree(src)
			if err != nil {
				t.Fatalf("ParseTree(%q): %v", src, err)
			}
			assertTreeRoundTrip(t, src, roots)
		})
	}
}

func assertTreeRoundTrip(t *testing.T, src string, want []TreeNode) {
	t.Helper()
	got, err := ParseTree(src)
	if err != nil {
		t.Fatalf("ParseTree(%q): %v", src, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseTree(%q) =\n%#v\nwant\n%#v", src, got, want)
	}
	var b strings.Builder
	WriteTree(&b, got)
	again, err := ParseTree(b.String())
	if err != nil {
		t.Fatalf("re-parse %q: %v", b.String(), err)
	}
	// `/**` is an input spelling WriteTree never emits.
	got = withoutGlobstar(got)
	if !reflect.DeepEqual(again, got) {
		t.Fatalf("re-parse of %q =\n%#v\nwant\n%#v", b.String(), again, got)
	}
}

func withoutGlobstar(nodes []TreeNode) []TreeNode {
	if nodes == nil {
		return nil
	}
	out := make([]TreeNode, len(nodes))
	for i, n := range nodes {
		n.Globstar = false
		n.Children = withoutGlobstar(n.Children)
		out[i] = n
	}
	return out
}

func assertSameTree(t *testing.T, a, b string) {
	t.Helper()
	ta, err := ParseTree(a)
	if err != nil {
		t.Fatalf("ParseTree(%q): %v", a, err)
	}
	tb, err := ParseTree(b)
	if err != nil {
		t.Fatalf("ParseTree(%q): %v", b, err)
	}
	if !reflect.DeepEqual(ta, tb) {
		t.Fatalf("trees differ:\n%#v\n%#v", ta, tb)
	}
}
