package ajour

import (
	"fmt"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/trellis"
)

// The verdict half of FDR 0028's vector suite, one row per FDR row id (the
// parse half is parse_test.go). A row is a stack of layers, a call, and the
// expected verdict: Admitted, and every refusal as "layer:kind" or
// "layer:kind@depth".

type verdictVector struct {
	id     string
	name   string
	layers []layerSrc
	call   Call
	admit  bool
	// refusals lists the expected refusals in stack order, each
	// "layer:deny", "layer:no-allowance" or "layer:lock-mismatch", with an
	// optional "@depth".
	refusals []string
}

type layerSrc struct {
	kind layerKind
	src  string
}

func stencil(src string) layerSrc { return layerSrc{layerStencil, src} }
func patch(src string) layerSrc   { return layerSrc{layerPatch, src} }

// subj builds a subject; each field is "name=value", repeated for a
// multi-valued field.
func subj(typ, id string, fields ...string) Subject {
	s := Subject{Type: typ, ID: id}
	for _, f := range fields {
		name, value, _ := strings.Cut(f, "=")
		if s.Fields == nil {
			s.Fields = map[string][]string{}
		}
		s.Fields[name] = append(s.Fields[name], value)
	}
	return s
}

func newSubj(typ string, fields ...string) Subject {
	s := subj(typ, "", fields...)
	s.New = true
	return s
}

func locked(s Subject, digest string) Subject {
	s.Digest = digest
	return s
}

func path(steps ...Subject) []Subject { return steps }

// A moxy tool call: plugin, server, tool; resources hang under the server.
func moxyCall(server, tool string, toolFields []string, values ...[]Subject) Call {
	return Call{
		Path: path(
			subj("clown-plugin-v1", "moxy"),
			subj("mcp-server-v1", server),
			subj("mcp-tool-v1", tool, toolFields...),
		),
		Values: values,
		Anchor: 1,
	}
}

func fsPath(id string) []Subject {
	return path(subj("fs-path-v1", id, "field=file_path"))
}

func input(field, value string) []Subject {
	return path(subj("tool-input-v1", "", "field="+field, "value="+value))
}

var readTool = []string{"kind=read", "perm=allow"}

// stencilA is FDR 0028 "Examples"' sample A, trimmed to the folio read tool.
const stencilA = `[moxy !clown-plugin-v1
  [-> folio !mcp-server-v1
    [-> read !mcp-tool-v1 kind=read perm=allow
      [-> file:circus/.worktrees/swift-plum !fs-path-v1 subtree]
      [-> file:circus/.worktrees/swift-plum/infra/secrets !fs-path-v1 subtree deny]
      [-> file:/nix/store !fs-path-v1 subtree]]]
  [-> rg !mcp-server-v1
    [-> search !mcp-tool-v1 kind=read perm=allow
      [-> file:circus/.worktrees/swift-plum !fs-path-v1 subtree]]]]`

// stencilC is stencilA without the /nix/store allowance (A-3).
const stencilC = `[moxy !clown-plugin-v1
  [-> folio !mcp-server-v1
    [-> read !mcp-tool-v1 kind=read perm=allow
      [-> file:circus/.worktrees/swift-plum !fs-path-v1 subtree]]]
  [-> rg !mcp-server-v1
    [-> search !mcp-tool-v1 kind=read perm=allow
      [-> file:circus/.worktrees/swift-plum !fs-path-v1 subtree]]]]`

// stencilM is the manager's stencil of RFC 0013 §9: read under the worktree.
const stencilM = `[moxy !clown-plugin-v1
  [-> folio !mcp-server-v1
    [-> read !mcp-tool-v1
      [-> file:circus/.worktrees/swift-plum !fs-path-v1 subtree]]]
  [-> rg !mcp-server-v1
    [-> search !mcp-tool-v1
      [-> file:circus/.worktrees/swift-plum !fs-path-v1 subtree]]]]`

const wt = "file:circus/.worktrees/swift-plum"

// recipeStencil holds one run_recipe tool with a recipe value path (FDR 0028
// "The two inputs": the recipe is a value path under the tool step).
func recipeStencil(argNode string) string {
	return `[just-us !clown-plugin-v1
  [-> just-us !mcp-server-v1
    [-> run_recipe !mcp-tool-v1
      [-> r !just-recipe-v1 ` + argNode + `]]]]`
}

func recipeCall(argValue string) Call {
	return Call{
		Path: path(
			subj("clown-plugin-v1", "just-us"),
			subj("mcp-server-v1", "just-us"),
			subj("mcp-tool-v1", "run_recipe"),
		),
		Values: [][]Subject{path(
			subj("just-recipe-v1", "r"),
			subj("recipe-arg-v1", "", "position=1", "value="+argValue),
		)},
		Anchor: 1,
	}
}

// cgCall is a cutting-garden tool call touching one resource path under the
// server's surface; every resource step carries the verb.
func cgCall(tool, verb string, resource ...Subject) Call {
	steps := []Subject{subj("mcp-resources-v1", "resources")}
	for _, s := range resource {
		if s.Fields == nil {
			s.Fields = map[string][]string{}
		}
		s.Fields["op"] = append([]string(nil), verb)
		steps = append(steps, s)
	}
	return Call{
		Path: path(
			subj("clown-plugin-v1", "cutting-garden"),
			subj("mcp-server-v1", "cutting-garden"),
			subj("mcp-tool-v1", tool),
		),
		Resources: [][]Subject{steps},
		Anchor:    1,
	}
}

func cgStencil(surface string) string {
	return `[cutting-garden !clown-plugin-v1
  [-> cutting-garden !mcp-server-v1
    [-> mutate_nodes !mcp-tool-v1]
    [-> read_node !mcp-tool-v1]
    [-> resources !mcp-resources-v1 ` + surface + `]]]`
}

func calCall(verb string, object ...Subject) Call {
	return cgCall("mutate_nodes", verb, append([]Subject{
		subj("cg-root-v1", "caldav:fastmail"),
		subj("caldav-calendar-v1", "cal-7"),
	}, object...)...)
}

func ynabCall(verb string, steps ...string) Call {
	resource := []Subject{subj("cg-root-v1", "ynab:main")}
	for _, s := range steps {
		typ := ""
		if strings.HasPrefix(s, "budget/") {
			typ = "cutting_garden-ynab-budget-v1"
		}
		resource = append(resource, subj(typ, s))
	}
	return cgCall("mutate_nodes", verb, resource...)
}

func withField(c Call, field, value string) Call {
	r := c.Resources[0]
	last := r[len(r)-1]
	c.Resources[0] = append(append([]Subject(nil), r...),
		subj("cg-field-v1", "", "field="+field, "value="+value, "op="+last.Fields["op"][0]))
	return c
}

const autoCreation = `[-> caldav:fastmail !cg-root-v1
  [-> cal-7 !caldav-calendar-v1
    [-> + !caldav-object-vtodo-v1 summary^="[auto]"]]]`

const ynabOneNode = `[-> ynab:main !cg-root-v1 subtree op=[read]
  [-> budget/b1 !cutting_garden-ynab-budget-v1
    [-> transactions
      [-> _ !cutting_garden-ynab-transaction-v1 op=[patch]
        [-> _ !cg-field-v1 field=[category, memo]]]]]]`

const ynabTwoSiblings = `[-> ynab:main !cg-root-v1 subtree op=[read]]
[-> ynab:main !cg-root-v1 op=[patch]
  [-> budget/b1 !cutting_garden-ynab-budget-v1
    [-> transactions
      [-> _ !cutting_garden-ynab-transaction-v1 op=[patch]
        [-> _ !cg-field-v1 field=[category, memo]]]]]]`

func ynabTxn(verb, budget string) Call {
	c := ynabCall(verb, budget, "transactions")
	r := c.Resources[0]
	t5 := subj("cutting_garden-ynab-transaction-v1", "t5", "op="+verb)
	c.Resources[0] = append(r, t5)
	return c
}

func justfileWrite(id string) Call {
	return moxyCall("folio", "write", nil, fsPath(id))
}

const parentWT = `[moxy [-> folio [-> write [-> file:/wt !fs-path-v1 subtree]]]]`

var verdictVectors = []verdictVector{
	// FDR 0028 "Examples".
	{
		id: "example", name: "sample A admits a read under the worktree",
		layers: []layerSrc{stencil(stencilA)},
		call:   moxyCall("folio", "read", readTool, fsPath(wt+"/src/a.go")),
		admit:  true,
	},
	{
		id: "example", name: "sample A denies the secrets folder",
		layers:   []layerSrc{stencil(stencilA)},
		call:     moxyCall("folio", "read", readTool, fsPath(wt+"/infra/secrets/token")),
		refusals: []string{"0:deny@3"},
	},

	// V-D2-12: the recipe-arg-v1 matrix.
	{id: "V-D2-12", name: "value list admits", layers: []layerSrc{stencil(recipeStencil(`[-> _ !recipe-arg-v1 position=1 value=[a, b]]`))}, call: recipeCall("b"), admit: true},
	{id: "V-D2-12", name: "value list refuses", layers: []layerSrc{stencil(recipeStencil(`[-> _ !recipe-arg-v1 position=1 value=[a, b]]`))}, call: recipeCall("c"), refusals: []string{"0:no-allowance@4"}},
	{id: "V-D2-12", name: "prefix admits", layers: []layerSrc{stencil(recipeStencil(`[-> _ !recipe-arg-v1 position=1 value^=origin/]`))}, call: recipeCall("origin/main"), admit: true},
	{id: "V-D2-12", name: "prefix refuses", layers: []layerSrc{stencil(recipeStencil(`[-> _ !recipe-arg-v1 position=1 value^=origin/]`))}, call: recipeCall("main"), refusals: []string{"0:no-allowance"}},
	{id: "V-D2-12", name: "regex admits", layers: []layerSrc{stencil(recipeStencil(`[-> _ !recipe-arg-v1 position=1 value~="^v[0-9]+$"]`))}, call: recipeCall("v12"), admit: true},
	{id: "V-D2-12", name: "regex refuses", layers: []layerSrc{stencil(recipeStencil(`[-> _ !recipe-arg-v1 position=1 value~="^v[0-9]+$"]`))}, call: recipeCall("v1x"), refusals: []string{"0:no-allowance"}},
	{id: "V-D2-12", name: "not-equal admits", layers: []layerSrc{stencil(recipeStencil(`[-> _ !recipe-arg-v1 position=1 value!=master]`))}, call: recipeCall("dev"), admit: true},
	{id: "V-D2-12", name: "not-equal refuses", layers: []layerSrc{stencil(recipeStencil(`[-> _ !recipe-arg-v1 position=1 value!=master]`))}, call: recipeCall("master"), refusals: []string{"0:no-allowance"}},

	// V-D2-13: a subtree allowance with a deny beside it; a file created later.
	{
		id: "V-D2-13", name: "a file created after bless",
		layers: []layerSrc{stencil(`[moxy [-> folio [-> write [-> file:/wt !fs-path-v1 subtree] [-> file:/wt/secrets !fs-path-v1 subtree deny]]]]`)},
		call:   justfileWrite("file:/wt/new.go"),
		admit:  true,
	},
	{
		id: "V-D2-13", name: "under the deny",
		layers:   []layerSrc{stencil(`[moxy [-> folio [-> write [-> file:/wt !fs-path-v1 subtree] [-> file:/wt/secrets !fs-path-v1 subtree deny]]]]`)},
		call:     justfileWrite("file:/wt/secrets/k"),
		refusals: []string{"0:deny"},
	},

	// V-D2-14: an input field no node names; then the open wildcard.
	{
		id: "V-D2-14", name: "an unnamed field is refused",
		layers:   []layerSrc{stencil(`[moxy [-> grit [-> remote [-> _ !tool-input-v1 field=subcommand value=[fetch]]]]]`)},
		call:     moxyCall("grit", "remote", nil, input("subcommand", "fetch"), input("remote", "origin")),
		refusals: []string{"0:no-allowance@3"},
	},
	{
		id: "V-D2-14", name: "the open wildcard admits it",
		layers: []layerSrc{stencil(`[moxy [-> grit [-> remote [-> _ !tool-input-v1 field=subcommand value=[fetch]] [-> _ !tool-input-v1 ^field=subcommand open]]]]`)},
		call:   moxyCall("grit", "remote", nil, input("subcommand", "fetch"), input("remote", "origin")),
		admit:  true,
	},

	// V-D1-7a..c: the child restates the parent's allowance and adds a deny.
	{
		id: "V-D1-7a", name: "the child's deny",
		layers:   []layerSrc{stencil(parentWT), stencil(`[moxy [-> folio [-> write [-> file:/wt !fs-path-v1 subtree] [-> file:/wt/justfile !fs-path-v1 deny]]]]`)},
		call:     justfileWrite("file:/wt/justfile"),
		refusals: []string{"1:deny"},
	},
	{
		id: "V-D1-7b", name: "both admit",
		layers: []layerSrc{stencil(parentWT), stencil(`[moxy [-> folio [-> write [-> file:/wt !fs-path-v1 subtree] [-> file:/wt/justfile !fs-path-v1 deny]]]]`)},
		call:   justfileWrite("file:/wt/src/a.go"),
		admit:  true,
	},
	{
		id: "V-D1-7c", name: "no allowance in either",
		layers:   []layerSrc{stencil(parentWT), stencil(`[moxy [-> folio [-> write [-> file:/wt !fs-path-v1 subtree] [-> file:/wt/justfile !fs-path-v1 deny]]]]`)},
		call:     justfileWrite("file:/etc/hosts"),
		refusals: []string{"0:no-allowance", "1:no-allowance"},
	},
	{
		id: "V-D1-7e", name: "a child widening to /etc is refused by the parent (= A-5)",
		layers:   []layerSrc{stencil(parentWT), stencil(`[moxy [-> folio [-> write [-> file:/wt !fs-path-v1 subtree] [-> file:/etc !fs-path-v1 subtree]]]]`)},
		call:     justfileWrite("file:/etc/hosts"),
		refusals: []string{"0:no-allowance"},
	},
	{
		id: "V-D1-7f", name: "a child silent about paths refuses (= A-3)",
		layers:   []layerSrc{stencil(parentWT), stencil(`[moxy [-> folio [-> write]]]`)},
		call:     justfileWrite("file:/wt/a.go"),
		refusals: []string{"1:no-allowance@3"},
	},

	// A-1: patches alone admit nothing.
	{
		id: "A-1", name: "a stack of patches only",
		layers: []layerSrc{patch(`[moxy [-> folio [-> read [-> file:/x !fs-path-v1 deny]]]]`)},
		call:   moxyCall("folio", "read", readTool, fsPath(wt+"/a.go")),
	},
	{id: "A-1", name: "no layers at all", call: moxyCall("folio", "read", readTool, fsPath(wt+"/a.go"))},

	// A-2: the RFC 0013 §9 link.
	{
		id: "A-2", name: "read under the worktree",
		layers: []layerSrc{stencil(stencilM), stencil(stencilA), patch(linkBody)},
		call:   moxyCall("folio", "read", readTool, fsPath(wt+"/src/a.go")),
		admit:  true,
	},
	{
		id: "A-2", name: "the patch denies the secrets",
		layers:   []layerSrc{stencil(stencilM), stencil(stencilA), patch(linkBody)},
		call:     moxyCall("folio", "read", readTool, fsPath(wt+"/infra/secrets/x")),
		refusals: []string{"1:deny", "2:deny"},
	},
	{
		id: "A-2", name: "rg is transparent to the patch",
		layers: []layerSrc{stencil(stencilM), stencil(stencilA), patch(linkBody)},
		call:   moxyCall("rg", "search", readTool, fsPath(wt)),
		admit:  true,
	},

	// A-3, A-4, A-5: the stencil rule.
	{
		id: "A-3", name: "the child omits an allowance",
		layers:   []layerSrc{stencil(stencilA), stencil(stencilC)},
		call:     moxyCall("folio", "read", readTool, fsPath("file:/nix/store/abc-x/bin/x")),
		refusals: []string{"1:no-allowance@3"},
	},
	{
		id: "A-4", name: "the child restates it",
		layers: []layerSrc{stencil(stencilA), stencil(stencilA)},
		call:   moxyCall("folio", "read", readTool, fsPath("file:/nix/store/abc-x/bin/x")),
		admit:  true,
	},
	{
		id: "A-5", name: "a child cannot widen",
		layers:   []layerSrc{stencil(stencilC), stencil(stencilA)},
		call:     moxyCall("folio", "read", readTool, fsPath("file:/nix/store/abc-x/bin/x")),
		refusals: []string{"0:no-allowance@3"},
	},

	// A-6: locks.
	{
		id: "A-6", name: "a live digest that differs",
		layers:   []layerSrc{stencil(`[just-us [-> just-us [-> run_recipe@blake2b256-s3 !mcp-tool-v1]]]`)},
		call:     Call{Path: path(subj("", "just-us"), subj("", "just-us"), locked(subj("mcp-tool-v1", "run_recipe"), "blake2b256-s2"))},
		refusals: []string{"0:lock-mismatch@2"},
	},
	{
		id: "A-6", name: "no live digest",
		layers:   []layerSrc{stencil(`[just-us [-> just-us [-> run_recipe@blake2b256-s3 !mcp-tool-v1]]]`)},
		call:     Call{Path: path(subj("", "just-us"), subj("", "just-us"), subj("mcp-tool-v1", "run_recipe"))},
		refusals: []string{"0:lock-mismatch@2"},
	},
	{
		id: "A-6", name: "the matching digest",
		layers: []layerSrc{stencil(`[just-us [-> just-us [-> run_recipe@blake2b256-s3 !mcp-tool-v1]]]`)},
		call:   Call{Path: path(subj("", "just-us"), subj("", "just-us"), locked(subj("mcp-tool-v1", "run_recipe"), "blake2b256-s3"))},
		admit:  true,
	},

	{
		// #411: a wildcard tool node (the complete document's cross-cutting
		// deny root) also matches the tool step, so the step does not refuse
		// there; the lock still has to be what the verdict names, at the tool.
		id: "A-6", name: "no live digest under a wildcard tool node (#411)",
		layers: []layerSrc{stencil(completeDocument)},
		call: Call{
			Path: path(
				subj("clown-plugin-v1", "just-us"), subj("mcp-server-v1", "just-us"),
				subj("mcp-tool-v1", "run_recipe"),
			),
			Values: [][]Subject{path(subj("just-recipe-v1", "build-nix", "group=build"))},
			Anchor: 1,
		},
		refusals: []string{"0:lock-mismatch@2"},
	},

	// A-7 and V-D2-11 (perm decides, kind pins) are in ask_test.go.

	// A-8: a catalog predicate on a multi-valued field.
	{
		id: "A-8", name: "a recipe in [test, docs]",
		layers: []layerSrc{stencil(`[j [-> j [-> run_recipe [-> _ !just-recipe-v1 group=[test, lint]]]]]`)},
		call:   Call{Path: path(subj("", "j"), subj("", "j"), subj("", "run_recipe")), Values: [][]Subject{path(subj("just-recipe-v1", "test-go", "group=test", "group=docs"))}},
		admit:  true,
	},
	{
		id: "A-8", name: "a recipe in [docs]",
		layers:   []layerSrc{stencil(`[j [-> j [-> run_recipe [-> _ !just-recipe-v1 group=[test, lint]]]]]`)},
		call:     Call{Path: path(subj("", "j"), subj("", "j"), subj("", "run_recipe")), Values: [][]Subject{path(subj("just-recipe-v1", "docs", "group=docs"))}},
		refusals: []string{"0:no-allowance@3"},
	},

	// A-9: negation is universal over a multi-valued field.
	{
		id: "A-9", name: "value!=master against [master, dev]",
		layers:   []layerSrc{stencil(`[t [-> _ !v value!=master]]`)},
		call:     Call{Path: path(subj("", "t")), Values: [][]Subject{path(subj("v", "", "value=master", "value=dev"))}},
		refusals: []string{"0:no-allowance@1"},
	},
	{
		id: "A-9", name: "^value=master against [master, dev]",
		layers:   []layerSrc{stencil(`[t [-> _ !v ^value=master]]`)},
		call:     Call{Path: path(subj("", "t")), Values: [][]Subject{path(subj("v", "", "value=master", "value=dev"))}},
		refusals: []string{"0:no-allowance@1"},
	},

	// A-10: segment-prefix containment.
	{
		id: "A-10", name: "/wtx is not under /wt",
		layers: []layerSrc{stencil(parentWT)}, call: justfileWrite("file:/wtx/a"),
		refusals: []string{"0:no-allowance"},
	},
	{id: "A-10", name: "/wt contains itself", layers: []layerSrc{stencil(parentWT)}, call: justfileWrite("file:/wt"), admit: true},

	// A-12: a negated atom is a condition, never a deny.
	{id: "A-12", name: "dev admitted", layers: []layerSrc{stencil(`[t [-> _ !v ^value=master]]`)}, call: Call{Path: path(subj("", "t")), Values: [][]Subject{path(subj("v", "", "value=dev"))}}, admit: true},
	{id: "A-12", name: "master refused as no allowance", layers: []layerSrc{stencil(`[t [-> _ !v ^value=master]]`)}, call: Call{Path: path(subj("", "t")), Values: [][]Subject{path(subj("v", "", "value=master"))}}, refusals: []string{"0:no-allowance"}},

	// A-13: two layers deny at different depths.
	{
		id: "A-13", name: "both refusals listed",
		layers: []layerSrc{
			stencil(`[moxy [-> folio deny]]`),
			patch(`[moxy [-> folio [-> read [-> file:/s !fs-path-v1 subtree deny]]]]`),
		},
		call:     moxyCall("folio", "read", readTool, fsPath("file:/s/k")),
		refusals: []string{"0:deny@1", "1:deny@3"},
	},

	// A-14: type matching.
	{id: "A-14", name: "a type-less node matches a typed subject", layers: []layerSrc{stencil(`[t [-> x]]`)}, call: Call{Path: path(subj("p", "t"), subj("q", "x"))}, admit: true},
	{id: "A-14", name: "a node !t does not match a subject of u", layers: []layerSrc{stencil(`[t [-> x !t]]`)}, call: Call{Path: path(subj("p", "t"), subj("u", "x"))}, refusals: []string{"0:no-allowance@1"}},

	// A-21: a creation allowance.
	{id: "A-21", name: "a New VTODO with [auto] x", layers: []layerSrc{stencil(cgStencil(autoCreation))}, call: calCall("create", newSubj("caldav-object-vtodo-v1", "summary=[auto] x")), admit: true},
	{id: "A-21", name: "a New VTODO with y", layers: []layerSrc{stencil(cgStencil(autoCreation))}, call: calCall("create", newSubj("caldav-object-vtodo-v1", "summary=y")), refusals: []string{"0:no-allowance@5"}},
	{id: "A-21", name: "an existing VTODO", layers: []layerSrc{stencil(cgStencil(autoCreation))}, call: calCall("patch", subj("caldav-object-vtodo-v1", "t1", "summary=[auto] x")), refusals: []string{"0:no-allowance@5"}},

	// A-23: inclusive containment with a verb.
	{id: "A-23", name: "delete the container itself (inclusive)", layers: []layerSrc{stencil(cgStencil(`[-> caldav:fastmail !cg-root-v1 [-> cal-7 !caldav-calendar-v1 subtree op=[delete]]]`))}, call: calCall("delete"), admit: true},
	{id: "A-23", name: "delete an event (sticky)", layers: []layerSrc{stencil(cgStencil(`[-> caldav:fastmail !cg-root-v1 [-> cal-7 !caldav-calendar-v1 subtree op=[delete]]]`))}, call: calCall("delete", subj("caldav-object-vevent-v1", "event-3")), admit: true},
	{id: "A-23", name: "patch an event", layers: []layerSrc{stencil(cgStencil(`[-> caldav:fastmail !cg-root-v1 [-> cal-7 !caldav-calendar-v1 subtree op=[delete]]]`))}, call: calCall("patch", subj("caldav-object-vevent-v1", "event-3")), refusals: []string{"0:no-allowance@4"}},

	// A-25: a bare deny covers one id; subtree deny covers what is under it.
	{id: "A-25", name: "bare deny: token admitted", layers: []layerSrc{stencil(`[moxy [-> folio [-> read [-> file:/wt subtree] [-> file:/wt/secrets deny]]]]`)}, call: moxyCall("folio", "read", nil, fsPath("file:/wt/secrets/token")), admit: true},
	{id: "A-25", name: "bare deny: the folder denied", layers: []layerSrc{stencil(`[moxy [-> folio [-> read [-> file:/wt subtree] [-> file:/wt/secrets deny]]]]`)}, call: moxyCall("folio", "read", nil, fsPath("file:/wt/secrets")), refusals: []string{"0:deny"}},
	{id: "A-25", name: "subtree deny: token denied", layers: []layerSrc{stencil(`[moxy [-> folio [-> read [-> file:/wt subtree] [-> file:/wt/secrets subtree deny]]]]`)}, call: moxyCall("folio", "read", nil, fsPath("file:/wt/secrets/token")), refusals: []string{"0:deny"}},
	{id: "A-25", name: "subtree deny: the folder denied", layers: []layerSrc{stencil(`[moxy [-> folio [-> read [-> file:/wt subtree] [-> file:/wt/secrets subtree deny]]]]`)}, call: moxyCall("folio", "read", nil, fsPath("file:/wt/secrets")), refusals: []string{"0:deny"}},

	// A-27: New is a birth, not a write.
	{id: "A-27", name: "write a new path", layers: []layerSrc{stencil(parentWT)}, call: justfileWrite("file:/wt/new.go"), admit: true},
	{
		id: "A-27", name: "the same subject marked New",
		layers: []layerSrc{stencil(parentWT)},
		call: Call{
			Path:   path(subj("clown-plugin-v1", "moxy"), subj("mcp-server-v1", "folio"), subj("mcp-tool-v1", "write")),
			Values: [][]Subject{path(func() Subject { s := subj("fs-path-v1", "file:/wt/new.go"); s.New = true; return s }())},
		},
		refusals: []string{"0:no-allowance@3"},
	},
	{
		id: "A-27", name: "subtree op=[patch] against a New VTODO",
		layers:   []layerSrc{stencil(cgStencil(`[-> caldav:fastmail !cg-root-v1 [-> cal-7 !caldav-calendar-v1 subtree op=[patch]]]`))},
		call:     calCall("create", newSubj("caldav-object-vtodo-v1")),
		refusals: []string{"0:no-allowance@5"},
	},

	// A-28: one kind per field; open covers only tool-input-v1.
	{
		id: "A-28", name: "a path under the subtree and a limit",
		layers: []layerSrc{stencil(`[moxy [-> folio [-> read [-> _ !tool-input-v1 open] [-> file:/wt !fs-path-v1 subtree]]]]`)},
		call:   moxyCall("folio", "read", nil, fsPath("file:/wt/a.go"), input("limit", "5")),
		admit:  true,
	},
	{
		id: "A-28", name: "a path outside",
		layers:   []layerSrc{stencil(`[moxy [-> folio [-> read [-> _ !tool-input-v1 open] [-> file:/wt !fs-path-v1 subtree]]]]`)},
		call:     moxyCall("folio", "read", nil, fsPath("file:/etc/passwd"), input("limit", "5")),
		refusals: []string{"0:no-allowance@3"},
	},
	{
		id: "A-28", name: "open does not cover a path",
		layers:   []layerSrc{stencil(`[moxy [-> folio [-> read [-> _ !tool-input-v1 open]]]]`)},
		call:     moxyCall("folio", "read", nil, fsPath("file:/wt/a.go")),
		refusals: []string{"0:no-allowance@3"},
	},

	// A-29: creation only in this collection.
	{id: "A-29", name: "[auto] x under cal-7", layers: []layerSrc{stencil(cgStencil(autoCreation))}, call: calCall("create", newSubj("caldav-object-vtodo-v1", "summary=[auto] x")), admit: true},
	{
		id: "A-29", name: "the same under cal-8",
		layers: []layerSrc{stencil(cgStencil(autoCreation))},
		call: cgCall("mutate_nodes", "create",
			subj("cg-root-v1", "caldav:fastmail"), subj("caldav-calendar-v1", "cal-8"),
			newSubj("caldav-object-vtodo-v1", "summary=[auto] x")),
		refusals: []string{"0:no-allowance@4"},
	},
	{id: "A-29", name: "y under cal-7", layers: []layerSrc{stencil(cgStencil(autoCreation))}, call: calCall("create", newSubj("caldav-object-vtodo-v1", "summary=y")), refusals: []string{"0:no-allowance@5"}},

	// A-30: a sticky subtree along a resource path.
	{id: "A-30", name: "read under the root", layers: []layerSrc{stencil(cgStencil(`[-> ynab:main !cg-root-v1 subtree op=[read] [-> budget/b2 !cutting_garden-ynab-budget-v1 subtree deny]]`))}, call: ynabTxn("read", "budget/b1"), admit: true},
	{id: "A-30", name: "patch under the root", layers: []layerSrc{stencil(cgStencil(`[-> ynab:main !cg-root-v1 subtree op=[read] [-> budget/b2 !cutting_garden-ynab-budget-v1 subtree deny]]`))}, call: ynabTxn("patch", "budget/b1"), refusals: []string{"0:no-allowance@3"}},
	{id: "A-30", name: "read under the denied budget", layers: []layerSrc{stencil(cgStencil(`[-> ynab:main !cg-root-v1 subtree op=[read] [-> budget/b2 !cutting_garden-ynab-budget-v1 subtree deny]]`))}, call: ynabCall("read", "budget/b2", "accounts", "a1"), refusals: []string{"0:deny@4"}},

	// A-31: a tree reference is a resource, which open under the tool does not cover.
	{
		id: "A-31", name: "mutate_nodes with open and an empty surface",
		layers:   []layerSrc{stencil(`[cutting-garden [-> cutting-garden [-> mutate_nodes [-> _ !tool-input-v1 open]] [-> resources !mcp-resources-v1]]]`)},
		call:     ynabTxn("patch", "budget/b1"),
		refusals: []string{"0:no-allowance@3"},
	},

	// A-32: a patch is one more step per field.
	{id: "A-32", name: "category", layers: []layerSrc{stencil(cgStencil(ynabTwoSiblings))}, call: withField(ynabTxn("patch", "budget/b1"), "category", "Groceries"), admit: true},
	{
		id: "A-32", name: "category and payee",
		layers: []layerSrc{stencil(cgStencil(ynabTwoSiblings))},
		call: func() Call {
			c := withField(ynabTxn("patch", "budget/b1"), "category", "Groceries")
			p := withField(ynabTxn("patch", "budget/b1"), "payee", "X")
			c.Resources = append(c.Resources, p.Resources[0])
			return c
		}(),
		refusals: []string{"0:no-allowance@7"},
	},
	{id: "A-32", name: "a field added later", layers: []layerSrc{stencil(cgStencil(ynabTwoSiblings))}, call: withField(ynabTxn("patch", "budget/b1"), "flag", "red"), refusals: []string{"0:no-allowance@7"}},

	// A-34: read wide, write narrow.
	{id: "A-34", name: "one node refuses the patch", layers: []layerSrc{stencil(cgStencil(ynabOneNode))}, call: withField(ynabTxn("patch", "budget/b1"), "category", "Groceries"), refusals: []string{"0:no-allowance@3"}},
	{id: "A-34", name: "two siblings admit it", layers: []layerSrc{stencil(cgStencil(ynabTwoSiblings))}, call: withField(ynabTxn("patch", "budget/b1"), "category", "Groceries"), admit: true},
	{id: "A-34", name: "two siblings refuse b2", layers: []layerSrc{stencil(cgStencil(ynabTwoSiblings))}, call: withField(ynabTxn("patch", "budget/b2"), "category", "Groceries"), refusals: []string{"0:no-allowance@4"}},
	{id: "A-34", name: "two siblings still read anywhere", layers: []layerSrc{stencil(cgStencil(ynabTwoSiblings))}, call: ynabTxn("read", "budget/b2"), admit: true},

	// A-35: a negated value list.
	{id: "A-35", name: "limit admitted", layers: []layerSrc{stencil(`[moxy [-> smith [-> issue-list [-> _ !tool-input-v1 ^field=[repo, host] open]]]]`)}, call: moxyCall("smith", "issue-list", nil, input("limit", "5")), admit: true},
	{id: "A-35", name: "repo refused", layers: []layerSrc{stencil(`[moxy [-> smith [-> issue-list [-> _ !tool-input-v1 ^field=[repo, host] open]]]]`)}, call: moxyCall("smith", "issue-list", nil, input("repo", "a/b")), refusals: []string{"0:no-allowance@3"}},
	{id: "A-35", name: "host refused", layers: []layerSrc{stencil(`[moxy [-> smith [-> issue-list [-> _ !tool-input-v1 ^field=[repo, host] open]]]]`)}, call: moxyCall("smith", "issue-list", nil, input("host", "h")), refusals: []string{"0:no-allowance@3"}},

	// Review findings (2026-11-10), pinned beside the FDR rows.
	{
		id: "closed", name: "an Anchor past the Path admits no resource",
		layers: []layerSrc{stencil(cgStencil(`[-> ynab:main !cg-root-v1 subtree op=[read]]`))},
		call: func() Call {
			c := ynabTxn("read", "budget/b1")
			c.Anchor = 3
			return c
		}(),
		refusals: []string{"0:no-allowance@4"},
	},
	{
		id: "closed", name: "op=create on a value is not a creation step",
		layers:   []layerSrc{stencil(`[t [-> _ !v op=[read]]]`)},
		call:     Call{Path: path(subj("", "t")), Values: [][]Subject{path(subj("v", "", "op=create"))}},
		refusals: []string{"0:no-allowance@1"},
	},

	// A-36: the create verb is not checked by op=.
	{id: "A-36", name: "create through a read-only container", layers: []layerSrc{stencil(cgStencil(`[-> caldav:fastmail !cg-root-v1 op=[read] [-> cal-7 !caldav-calendar-v1 subtree op=[read] [-> + !caldav-object-vtodo-v1 summary^="[auto]"]]]`))}, call: calCall("create", newSubj("caldav-object-vtodo-v1", "summary=[auto] x")), admit: true},
	{id: "A-36", name: "a patch there is still refused", layers: []layerSrc{stencil(cgStencil(`[-> caldav:fastmail !cg-root-v1 op=[read] [-> cal-7 !caldav-calendar-v1 subtree op=[read] [-> + !caldav-object-vtodo-v1 summary^="[auto]"]]]`))}, call: calCall("patch", subj("caldav-object-vevent-v1", "event-3")), refusals: []string{"0:no-allowance@3"}},
	{id: "A-36", name: "no + node refuses the creation", layers: []layerSrc{stencil(cgStencil(`[-> caldav:fastmail !cg-root-v1 [-> cal-7 !caldav-calendar-v1 op=[patch]]]`))}, call: calCall("create", newSubj("caldav-object-vtodo-v1", "summary=[auto] x")), refusals: []string{"0:no-allowance@5"}},
}

func TestVerdictVectors(t *testing.T) {
	for _, v := range verdictVectors {
		t.Run(v.id+": "+v.name, func(t *testing.T) {
			var layers []Layer
			for _, l := range v.layers {
				if l.kind == layerPatch {
					p, err := ParsePatch(l.src)
					if err != nil {
						t.Fatalf("ParsePatch: %v", err)
					}
					layers = append(layers, p)
				} else {
					s, err := ParseStencil(l.src)
					if err != nil {
						t.Fatalf("ParseStencil: %v", err)
					}
					layers = append(layers, s)
				}
			}
			got := Evaluate(layers, v.call)
			if got.Admitted != v.admit {
				t.Errorf("Admitted = %v, want %v\n%s", got.Admitted, v.admit, got)
			}
			if !refusalsMatch(got.Refusals, v.refusals) {
				t.Errorf("refusals = %v, want %v\n%s", spellRefusals(got.Refusals), v.refusals, got)
			}
		})
	}
}

func spellRefusals(rs []Refusal) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = fmt.Sprintf("%d:%s@%d", r.Layer, kindSlug(r.Kind), r.Depth)
	}
	return out
}

func kindSlug(k Kind) string {
	return strings.ReplaceAll(k.String(), " ", "-")
}

func refusalsMatch(got []Refusal, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i, w := range want {
		r := got[i]
		short := fmt.Sprintf("%d:%s", r.Layer, kindSlug(r.Kind))
		if w != short && w != fmt.Sprintf("%s@%d", short, r.Depth) {
			return false
		}
	}
	return true
}

// TestVerdictString pins the one-line refusal rendering (FDR 0028 "The
// verdict").
func TestVerdictString(t *testing.T) {
	m, err := ParseStencil(stencilC)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParsePatch(linkBody)
	if err != nil {
		t.Fatal(err)
	}
	v := Evaluate([]Layer{m, p}, moxyCall("folio", "read", readTool, fsPath(wt+"/infra/secrets/x")))
	want := "layer 1 (patch): deny [" + wt + "/infra/secrets !fs-path-v1 subtree deny] at moxy/folio/read"
	if !strings.Contains(v.String(), want) {
		t.Fatalf("String() = %q, want a line %q", v.String(), want)
	}
	v = Evaluate([]Layer{m}, moxyCall("folio", "read", readTool, fsPath("file:/nix/store/x")))
	want = "layer 0 (stencil): no allowance for {fs-path-v1 file:/nix/store/x} at moxy/folio/read"
	if strings.TrimSpace(v.String()) != want {
		t.Fatalf("String() = %q, want %q", v.String(), want)
	}
}

// TestEvaluate_CodeBuiltRegex pins that a Node built in code with a `~=`
// pattern that does not compile never holds, even negated (closed), and that
// an atom edited after parse is matched by its new pattern.
func TestEvaluate_CodeBuiltRegex(t *testing.T) {
	bad := trellis.FieldPred{
		Field: trellis.FieldName{Name: "value"}, Op: trellis.FieldOpRegex,
		Values: []trellis.Value{trellis.StringValue{Value: "("}},
	}
	stencil := Stencil{Roots: []Node{{
		ID: "t", HasID: true,
		Children: []Node{{Type: "v", Atoms: []trellis.FieldPred{bad}, Negated: []bool{true}}},
	}}}
	call := Call{Path: path(subj("", "t")), Values: [][]Subject{path(subj("v", "", "value=x"))}}
	if v := Evaluate([]Layer{stencil}, call); v.Admitted {
		t.Fatalf("a negated uncompilable pattern admitted the call:\n%s", v)
	}

	parsed, err := ParseStencil(`[t [-> _ !v value~="^a"]]`)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Roots[0].Children[0].Atoms[0].Values = []trellis.Value{trellis.StringValue{Value: "^b"}}
	call.Values = [][]Subject{path(subj("v", "", "value=bx"))}
	if v := Evaluate([]Layer{parsed}, call); !v.Admitted {
		t.Fatalf("an edited pattern was matched by its parse-time compile:\n%s", v)
	}
}

// TestOperatorConformance is A-16: ajour's operator semantics agree with
// trellis_eval's on one shared table (trellis.OperatorVectors, which
// internal/trellis_eval's tests run too).
func TestOperatorConformance(t *testing.T) {
	for _, v := range trellis.OperatorVectors {
		t.Run(v.Op.String()+" "+v.Got+" "+v.Want, func(t *testing.T) {
			if got := satisfies(v.Op, v.Got, v.Want, nil); got != v.Holds {
				t.Fatalf("%q %s %q = %v, want %v", v.Got, v.Op, v.Want, got, v.Holds)
			}
		})
	}
}
