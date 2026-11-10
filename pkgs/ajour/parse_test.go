package ajour

import (
	"reflect"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/trellis"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The parse half of FDR 0028's vector suite ("Vectors"), one row per FDR row
// id. Verdict rows (A-1..A-10, A-12..A-14, A-16, A-21, A-23, A-25, A-27..A-32)
// belong to Evaluate (phase 2, #346) and extend this file with their own
// table. Every row that parses is also an A-15 round trip: Write, re-parse as
// the same kind of layer, compare.

const (
	asStencil = layerStencil
	asPatch   = layerPatch
)

type parseVector struct {
	id   string // FDR 0028 vector row id
	name string
	src  string
	as   layerKind
	// wantErr is a substring the parse error must carry (the offending term);
	// empty means the source parses.
	wantErr string
	// skip, when set, is why the row is not ajour's to check.
	skip  string
	check func(t *testing.T, roots []Node)
}

// sampleA is circus D2 §3's body (`docs/plans/2026-10-22-d2-profile-schema.md`
// in circus, as fixed by circus 21065c39 and 62bd7b5c: the `_` id slot on the
// recipe node, blech32 digest stand-ins, `mutate=` retired), copied verbatim.
const sampleA = `[clown-builtin-jobs !clown-plugin-v1 required
  [-> ringmaster !mcp-server-v1 required
    [-> job_status !mcp-tool-v1 kind=read perm=allow]
    [-> job_read !mcp-tool-v1 kind=read perm=allow]
    [-> job_wait !mcp-tool-v1 kind=read perm=allow]]
  [-> troupe !mcp-server-v1 required
    [-> chat_list !mcp-tool-v1 kind=read perm=allow]
    [-> chat_read !mcp-tool-v1 kind=read perm=allow]
    [-> chat_send !mcp-tool-v1 kind=write perm=allow]]]

[spinclass !clown-plugin-v1
  [-> spinclass !mcp-server-v1
    [-> whoami !mcp-tool-v1 kind=read perm=allow]
    [-> grit_stage !mcp-tool-v1 kind=read perm=allow
      [-> status !tool-arg-v1 arg=subcommand]
      [-> diff !tool-arg-v1 arg=subcommand]]
    [-> grit_branch !mcp-tool-v1 kind=read perm=allow
      [-> log !tool-arg-v1 arg=subcommand]
      [-> diff !tool-arg-v1 arg=subcommand]
      [-> rev-parse !tool-arg-v1 arg=subcommand]]]]

[moxy !clown-plugin-v1
  [-> rg !mcp-server-v1
    [-> search !mcp-tool-v1 kind=read perm=allow
      [-> file:circus/.worktrees/swift-plum !fs-path-v1 subtree]]]
  [-> folio !mcp-server-v1
    [-> read !mcp-tool-v1 kind=read perm=allow
      [-> file:circus/.worktrees/swift-plum !fs-path-v1 subtree]
      [-> file:circus/.worktrees/swift-plum/infra/secrets !fs-path-v1 subtree deny]
      [-> file:/nix/store !fs-path-v1 subtree]]
    [-> glob !mcp-tool-v1 kind=read perm=allow
      [-> file:circus/.worktrees/swift-plum !fs-path-v1 subtree]]]
  [-> man !mcp-server-v1
    [-> list !mcp-tool-v1 kind=read perm=allow]
    [-> toc !mcp-tool-v1 kind=read perm=allow]
    [-> section !mcp-tool-v1 kind=read perm=allow]]
  [-> smith !mcp-server-v1
    [-> issue-get !mcp-tool-v1 kind=read perm=allow]
    [-> pr-get !mcp-tool-v1 kind=read perm=allow]]]

[just-us !clown-plugin-v1
  [-> just-us !mcp-server-v1
    [-> list_recipes !mcp-tool-v1 kind=read perm=allow]
    [-> show_recipe !mcp-tool-v1 kind=read perm=allow]
    [-> run_recipe@blake2b256-s3 !mcp-tool-v1 kind=exec perm=allow async=no impure=no
      [-> _ !just-recipe-v1 group=[test, lint]]
      [-> explore-circus-cli@blake2b256-s4 !just-recipe-v1
        [-> verb !recipe-arg-v1 position=1 value=[status, update]]
        [-> flag !recipe-arg-v1 position=2 value="--dry-run"]]]]]

[cutting-garden !clown-plugin-v1
  [-> cutting-garden !mcp-server-v1
    [-> describe_node_types !mcp-tool-v1 kind=read perm=allow]
    [-> read_facets !mcp-tool-v1 kind=read perm=allow]
    [-> list_nodes !mcp-tool-v1 kind=read perm=allow]
    [-> read_node !mcp-tool-v1 kind=read perm=allow]
    [-> resources !mcp-resources-v1
      [-> smith:github.com/amarbel-llc/circus !cg-root-v1 scheme=smith subtree op=[read]]]]]


[attest !clown-plugin-v1
  [-> attest !mcp-server-v1
    [-> emit_issue !mcp-tool-v1 kind=write perm=allow]]]
`

// sampleB is circus D2 §4's body, as fixed by the same circus commits.
const sampleB = `[moxy !clown-plugin-v1
  [-> rg !mcp-server-v1
    [-> search !mcp-tool-v1 kind=read perm=allow
      [-> file:circus/.worktrees/swift-plum !fs-path-v1 subtree]]]
  [-> folio !mcp-server-v1
    [-> read !mcp-tool-v1 kind=read perm=allow
      [-> file:circus/.worktrees/swift-plum !fs-path-v1 subtree]
      [-> file:circus/.worktrees/swift-plum/infra/secrets !fs-path-v1 subtree deny]]]
  [-> man !mcp-server-v1
    [-> toc !mcp-tool-v1 kind=read perm=allow]
    [-> section !mcp-tool-v1 kind=read perm=allow]]]

[just-us !clown-plugin-v1
  [-> just-us !mcp-server-v1
    [-> run_recipe@blake2b256-s3 !mcp-tool-v1 kind=exec perm=allow async=no impure=no
      [-> _ !just-recipe-v1 group=[test, lint]]]]]

[attest !clown-plugin-v1
  [-> attest !mcp-server-v1
    [-> emit_issue !mcp-tool-v1 kind=write perm=allow]]]
`

// linkBody is RFC 0013 §9's one-deny link body (FDR 0028 "Examples", P).
const linkBody = `[moxy !clown-plugin-v1
  [-> folio !mcp-server-v1
    [-> read !mcp-tool-v1
      [-> file:circus/.worktrees/swift-plum/infra/secrets !fs-path-v1 subtree deny]]]]`

// sampleAHeadings is the one-isomorphism sketch §5's heading rendering of a
// trimmed sample A, with sampleNestedTrimmed its canonical twin.
const sampleAHeadings = `# [moxy !clown-plugin-v1]
## [folio !mcp-server-v1]
### [read !mcp-tool-v1 kind=read perm=allow]
- [file:circus/.worktrees/swift-plum !fs-path-v1 subtree]
- [file:circus/.worktrees/swift-plum/infra/secrets !fs-path-v1 deny]
# [just-us !clown-plugin-v1]
## [just-us !mcp-server-v1]
### [run_recipe@blake2b256-s3 !mcp-tool-v1 kind=exec perm=allow]
- [_ !just-recipe-v1 group=[test, lint]]
`

const sampleNestedTrimmed = `[moxy !clown-plugin-v1
  [-> folio !mcp-server-v1
    [-> read !mcp-tool-v1 kind=read perm=allow
      [-> file:circus/.worktrees/swift-plum !fs-path-v1 subtree]
      [-> file:circus/.worktrees/swift-plum/infra/secrets !fs-path-v1 deny]]]]
[just-us !clown-plugin-v1
  [-> just-us !mcp-server-v1
    [-> run_recipe@blake2b256-s3 !mcp-tool-v1 kind=exec perm=allow
      [-> _ !just-recipe-v1 group=[test, lint]]]]]`

// completeDocument is FDR 0028 "A complete document"'s body.
const completeDocument = `[moxy !clown-plugin-v1
  [-> folio !mcp-server-v1
    [-> _ !mcp-tool-v1 kind=read
      [-> file:eng/repos !fs-path-v1 subtree]
      [-> _ !tool-input-v1 open]]
    [-> write !mcp-tool-v1
      [-> file:eng/repos/cutting-garden/.worktrees/swift-plum !fs-path-v1 subtree]
      [-> _ !tool-input-v1 open]]]
  [-> grit !mcp-server-v1
    [-> _ !mcp-tool-v1 kind=read
      [-> _ !tool-input-v1 open]]
    [-> commit !mcp-tool-v1
      [-> _ !tool-input-v1 open]]
    [-> remote !mcp-tool-v1
      [-> _ !tool-input-v1 field=subcommand value=[fetch, pull]]
      [-> _ !tool-input-v1 ^field=subcommand open]]]
  [-> smith !mcp-server-v1
    [-> _ !mcp-tool-v1 kind=read
      [-> _ !tool-input-v1 field=repo value=[linenisgreat/cutting-garden, linenisgreat/madder]]
      [-> _ !tool-input-v1 ^field=repo open]]
    [-> issue-comment !mcp-tool-v1
      [-> _ !tool-input-v1 field=repo value=[linenisgreat/cutting-garden, linenisgreat/madder]]
      [-> _ !tool-input-v1 ^field=repo open]]]]

[just-us !clown-plugin-v1
  [-> just-us !mcp-server-v1
    [-> run_recipe@blake2b256-s3 !mcp-tool-v1
      [-> _ !just-recipe-v1 group=[build, test, lint]]]]]

[cutting-garden !clown-plugin-v1
  [-> cutting-garden !mcp-server-v1
    [-> list_nodes !mcp-tool-v1 [-> _ !tool-input-v1 open]]
    [-> read_node !mcp-tool-v1]
    [-> mutate_nodes !mcp-tool-v1]
    [-> resources !mcp-resources-v1
      [-> ynab:main !cg-root-v1 subtree op=[read]]
      [-> ynab:main !cg-root-v1 op=[patch]
        [-> budget/b1 !cutting_garden-ynab-budget-v1
          [-> transactions
            [-> _ !cutting_garden-ynab-transaction-v1 op=[patch]
              [-> _ !cg-field-v1 field=[category, memo]]]]]]]]]

[_ !clown-plugin-v1 open
  [-> _ !mcp-server-v1 open
    [-> _ !mcp-tool-v1 open
      [-> file:eng/repos/cutting-garden/infra/secrets !fs-path-v1 subtree deny]]]]`

// issuesExample is FDR 0028 "A second example"'s body.
const issuesExample = `[cutting-garden !clown-plugin-v1
  [-> cutting-garden !mcp-server-v1
    [-> list_nodes !mcp-tool-v1 [-> _ !tool-input-v1 open]]
    [-> read_node !mcp-tool-v1]
    [-> read_facets !mcp-tool-v1 [-> _ !tool-input-v1 open]]
    [-> mutate_nodes !mcp-tool-v1]
    [-> resources !mcp-resources-v1
      [-> smith://code.linenisgreat.com/linenisgreat !cg-root-v1
        [-> cutting-garden !smith-repo-v1 subtree op=[read]]
        [-> cutting-garden !smith-repo-v1 op=[patch]
          [-> issues !smith-issues-v1
            [-> _ !smith-issue-v1 op=[patch]
              [-> + !smith-comment-v1]
              [-> _ !cg-field-v1 field=[state] value=[closed]]]]]]]]]`

var parseVectors = []parseVector{
	{id: "A-15", name: "FDR 0028's complete document", src: completeDocument},
	{id: "A-15", name: "FDR 0028's second example", src: issuesExample},
	{id: "V-D2-1", name: "sample A parses as a stencil", src: sampleA},
	{id: "V-D2-2", name: "sample B parses as a stencil", src: sampleB},
	{
		id: "V-D2-8", name: "quoted --dry-run", src: `[flag !recipe-arg-v1 position=2 value="--dry-run"]`,
		check: func(t *testing.T, roots []Node) {
			assertValue(t, roots[0].Atoms[1], trellis.StringValue{Value: "--dry-run"})
		},
	},
	{
		// Recorded: the bare form parses, as a Bareword (a leading `-` is an
		// identifier rune), so it is the same value as the quoted form.
		id: "V-D2-8", name: "bare --dry-run is a bareword", src: `[flag !recipe-arg-v1 position=2 value=--dry-run]`,
		check: func(t *testing.T, roots []Node) {
			assertValue(t, roots[0].Atoms[1], trellis.Bareword{Name: "--dry-run"})
		},
	},
	{
		id: "V-D1-7d", name: "a malformed regex errors naming the atom",
		src:     `[x [-> _ !recipe-arg-v1 value~="(unclosed"]]`,
		wantErr: `value~="(unclosed"`,
	},
	{
		id: "V-D1-7d", name: "a well-formed regex compiles",
		src: `[x [-> _ !recipe-arg-v1 value~="^v[0-9]+$"]]`,
	},
	{
		id: "A-11", name: "a patch with an allowance-shaped leaf", as: asPatch,
		src:     `[moxy [-> folio [-> read [-> file:/wt !fs-path-v1 subtree]]]]`,
		wantErr: `[file:/wt !fs-path-v1 subtree]`,
	},
	{id: "A-17", name: "the one-deny link body as a patch", src: linkBody, as: asPatch},
	{id: "A-17", name: "the one-deny link body as a stencil", src: linkBody},
	{
		id: "A-18", name: "an allowance leaf as a stencil",
		src: `[moxy [-> folio [-> read [-> file:/wt !fs-path-v1 subtree]]]]`,
	},
	{
		id: "A-18", name: "an allowance leaf as a patch", as: asPatch,
		src:     `[moxy [-> folio [-> read [-> file:/wt !fs-path-v1 subtree]]]]`,
		wantErr: "carries no deny",
	},
	{
		id: "A-19", name: "a value-list node is in the fragment and not espalier",
		src: `[_ !just-recipe-v1 group=[test, lint]]`,
		check: func(t *testing.T, _ []Node) {
			if _, err := trellis.ParseLiteral(`_ !just-recipe-v1 group=[test, lint]`); err == nil {
				t.Fatal("trellis.ParseLiteral accepted a value list; want the espalier refusal")
			}
		},
	},
	{id: "A-19", name: "the whole of sample A", src: sampleA},
	{
		id: "A-20", name: "real id", src: `[work !task]`,
		check: func(t *testing.T, roots []Node) { assertID(t, roots[0], "work", true) },
	},
	{
		id: "A-20", name: "null id (a wildcard, so `open`)", src: `[_ !task open]`,
		check: func(t *testing.T, roots []Node) { assertID(t, roots[0], "", false) },
	},
	{
		id: "A-20", name: "an object named _", src: `["_" !task]`,
		check: func(t *testing.T, roots []Node) { assertID(t, roots[0], "_", true) },
	},
	{id: "A-20", name: "no id slot", src: `[!task]`, wantErr: "id slot"},
	{
		id: "A-22", name: "a patch denying a creation", as: asPatch,
		src: `[cal-7 [-> + !vtodo deny]]`,
		check: func(t *testing.T, roots []Node) {
			if c := roots[0].Children[0]; !c.New || !c.Deny {
				t.Fatalf("child = %+v; want a denied temp-id node", c)
			}
		},
	},
	{
		id: "A-22", name: "a patch with a creation allowance", as: asPatch,
		src:     `[cal-7 [-> + !vtodo summary^=x]]`,
		wantErr: "[+ !vtodo summary^=x]",
	},
	{
		id: "A-24", name: "op=create in a stencil", src: `[cal-7 op=[create]]`,
		skip: "FDR 0028 \"Verbs\": op=create is refused by the profile validator " +
			"(clown's ruleset); ajour never knows the `op` vocabulary",
	},
	{
		id: "A-26", name: "a trailing /** is the id plus subtree",
		src: `[w [-> file:/wt/** !fs-path-v1]]`,
		check: func(t *testing.T, roots []Node) {
			c := roots[0].Children[0]
			if c.ID != "file:/wt" || !c.Subtree || !reflect.DeepEqual(c.Tags, []string{"subtree"}) {
				t.Fatalf("child = %+v; want file:/wt with subtree", c)
			}
			var b strings.Builder
			Stencil{Roots: roots}.Write(&b)
			if strings.Contains(b.String(), "/**") || !strings.Contains(b.String(), "subtree") {
				t.Fatalf("Write = %q; want subtree and never /**", b.String())
			}
			again, err := ParseStencil(`[w [-> file:/wt/** !fs-path-v1 subtree]]`)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(again.Roots, roots) {
				t.Fatalf("/** with subtree = %+v; want %+v", again.Roots, roots)
			}
		},
	},
	{
		id: "A-26", name: "/** not at the end of an id",
		src:     `[w [-> file:/**/x !fs-path-v1]]`,
		wantErr: "file:/**/x",
	},
	{
		id: "A-33", name: "a typed _ node with no atoms and no open is refused",
		src:     `[read [-> _ !tool-input-v1]]`,
		wantErr: "[_ !tool-input-v1]",
	},
	{
		id: "A-33", name: "an interior or deny wildcard needs open too",
		src:     `[_ !clown-plugin-v1 [-> _ !mcp-server-v1 open [-> file:/s !fs-path-v1 subtree deny]]]`,
		wantErr: "[_ !clown-plugin-v1]",
	},
	{
		id: "A-34", name: "same-id siblings are kept, in order, never merged",
		src: `[resources [-> ynab:main !cg-root-v1 subtree op=[read]] [-> ynab:main !cg-root-v1 op=[patch] [-> budget/b1]]]`,
		check: func(t *testing.T, roots []Node) {
			c := roots[0].Children
			if len(c) != 2 || c[0].ID != "ynab:main" || c[1].ID != "ynab:main" ||
				!c[0].Subtree || c[1].Subtree || len(c[1].Children) != 1 {
				t.Fatalf("children = %+v; want both ynab:main siblings as written", c)
			}
		},
	},
	{
		id: "A-35", name: "a negated value list on the open node",
		src: `[read [-> _ !tool-input-v1 ^field=[repo, host] open]]`,
		check: func(t *testing.T, roots []Node) {
			c := roots[0].Children[0]
			if len(c.Atoms) != 1 || !c.Negated[0] || !c.Atoms[0].List || len(c.Atoms[0].Values) != 2 {
				t.Fatalf("node = %+v; want one negated two-value list", c)
			}
		},
	},
	{id: "A-33", name: "with open", src: `[read [-> _ !tool-input-v1 open]]`},
	{id: "A-33", name: "a patch is exempt", src: `[read [-> _ !tool-input-v1 deny]]`, as: asPatch},
	{
		id: "fragment", name: "a heading rendering canonicalizes to nested espalier", src: sampleAHeadings,
		check: func(t *testing.T, roots []Node) {
			nested, err := ParseStencil(sampleNestedTrimmed)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(roots, nested.Roots) {
				t.Fatalf("heading rendering = %+v; want %+v", roots, nested.Roots)
			}
			var b strings.Builder
			Stencil{Roots: roots}.Write(&b)
			if strings.Contains(b.String(), "#") {
				t.Fatalf("Write = %q; want nested espalier, never headings", b.String())
			}
		},
	},
	{id: "fragment", name: "a reserved field name", src: `[x _body=y]`, wantErr: "_body=y"},
	{id: "fragment", name: "a quoted _ field name is opaque", src: `[x "_body"=y]`},
	{id: "fragment", name: "a qualifier value", src: `[x date=(month)]`, wantErr: "date=(month)"},
	{id: "fragment", name: "a standalone digest", src: `[x @blake2b256-s3]`, wantErr: "@blake2b256-s3"},
	{id: "fragment", name: "OR-alternatives", src: `[x [a, b]]`, wantErr: "OR-alternatives"},
	{id: "fragment", name: "a sigil", src: `[x tag:]`, wantErr: "tag:"},
	{id: "fragment", name: "a closure child", src: `[x [->> y]]`, wantErr: "->>"},
	{id: "fragment", name: "a two-step child", src: `[x [-> y -> z]]`, wantErr: "more than one step"},
	{id: "fragment", name: "negation of a tag", src: `[x ^t]`, wantErr: "^t"},
	{id: "fragment", name: "negation of an atom", src: `[x !t ^value=master]`},
	{id: "fragment", name: "two types", src: `[x !t !u]`, wantErr: "!u"},
}

func TestParseVectors(t *testing.T) {
	for _, v := range parseVectors {
		t.Run(v.id+" "+v.as.String()+": "+v.name, func(t *testing.T) {
			if v.skip != "" {
				t.Skip(v.skip)
			}
			roots, err := parseAs(v.as, v.src)
			if v.wantErr != "" {
				if err == nil {
					t.Fatalf("parse succeeded; want an error naming %q", v.wantErr)
				}
				if !errors.Is400BadRequest(err) || !strings.Contains(err.Error(), v.wantErr) {
					t.Fatalf("error = %v; want a bad request naming %q", err, v.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if v.check != nil {
				v.check(t, roots)
			}
			assertRoundTrip(t, v.as, roots) // A-15
		})
	}
}

func parseAs(k layerKind, src string) ([]Node, error) {
	if k == asPatch {
		p, err := ParsePatch(src)
		return p.Roots, err
	}
	s, err := ParseStencil(src)
	return s.Roots, err
}

func assertRoundTrip(t *testing.T, k layerKind, roots []Node) {
	t.Helper()
	var b strings.Builder
	if k == asPatch {
		Patch{Roots: roots}.Write(&b)
	} else {
		Stencil{Roots: roots}.Write(&b)
	}
	again, err := parseAs(k, b.String())
	if err != nil {
		t.Fatalf("A-15: re-parse of %q: %v", b.String(), err)
	}
	if !reflect.DeepEqual(again, roots) {
		t.Fatalf("A-15: re-parse of %q =\n%+v\nwant\n%+v", b.String(), again, roots)
	}
}

func assertID(t *testing.T, n Node, id string, hasID bool) {
	t.Helper()
	if n.ID != id || n.HasID != hasID || n.New {
		t.Fatalf("node = %+v; want ID %q HasID %v", n, id, hasID)
	}
}

func assertValue(t *testing.T, atom trellis.FieldPred, want trellis.Value) {
	t.Helper()
	if len(atom.Values) != 1 || !reflect.DeepEqual(atom.Values[0], want) {
		t.Fatalf("atom %+v; want the one value %#v", atom, want)
	}
}

// Stencil and Patch are the two Layers.
var _ = []Layer{Stencil{}, Patch{}}
