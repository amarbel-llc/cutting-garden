package ajour

import (
	"fmt"
	"strings"
	"testing"
)

// The `perm` decision half of FDR 0028's vector suite ("Facts are pins, perm
// is a decision"): a row is a stack, a call, the outcome, and for an
// admitted call whether the stack overrides the enforcer's ask tier.

type askVector struct {
	id      string
	name    string
	layers  []layerSrc
	call    Call
	outcome Outcome
	perm    Perm // checked when outcome is OutcomeAdmitted
	// asks lists the expected asks in stack order, each "layer@depth".
	asks     []string
	refusals []string
}

const (
	askTool = `[moxy !clown-plugin-v1
  [-> folio !mcp-server-v1
    [-> read !mcp-tool-v1 kind=read perm=ask
      [-> file:circus/.worktrees/swift-plum !fs-path-v1 subtree]]]]`
	dynamicTool = `[moxy !clown-plugin-v1
  [-> folio !mcp-server-v1
    [-> read !mcp-tool-v1 kind=read perm=dynamic
      [-> file:circus/.worktrees/swift-plum !fs-path-v1 subtree]]]]`
	askInfra = `[moxy !clown-plugin-v1
  [-> folio !mcp-server-v1
    [-> read !mcp-tool-v1 perm=allow
      [-> file:circus/.worktrees/swift-plum !fs-path-v1 subtree]
      [-> file:circus/.worktrees/swift-plum/infra !fs-path-v1 subtree perm=ask]]]]`
	denyReadTool = `[moxy !clown-plugin-v1 [-> folio !mcp-server-v1 [-> read !mcp-tool-v1 deny]]]`
)

var manifestAsk = []string{"kind=read", "perm=ask"}

var askVectors = []askVector{
	// A-7 (rewritten): perm is the profile's decision, not a pin of the
	// manifest, so the manifest's ask does not refuse and is overridden.
	{id: "A-7", name: "perm=allow over a manifest saying ask", layers: []layerSrc{stencil(stencilA)}, call: moxyCall("folio", "read", manifestAsk, fsPath(wt+"/a.go")), outcome: OutcomeAdmitted, perm: PermAllow},
	// V-D2-11 (rewritten): kind is a fact and stays a pin.
	{id: "V-D2-11", name: "kind=read against a manifest saying write", layers: []layerSrc{stencil(stencilA)}, call: moxyCall("folio", "read", []string{"kind=write", "perm=allow"}, fsPath(wt+"/a.go")), outcome: OutcomeRefused, refusals: []string{"0:no-allowance@2"}},
	{id: "A-37", name: "perm=ask on the tool", layers: []layerSrc{stencil(askTool)}, call: moxyCall("folio", "read", readTool, fsPath(wt+"/a.go")), outcome: OutcomeAsk, asks: []string{"0@2"}},
	{id: "A-38", name: "no perm atom", layers: []layerSrc{stencil(stencilM)}, call: moxyCall("folio", "read", manifestAsk, fsPath(wt+"/a.go")), outcome: OutcomeAdmitted, perm: PermDefault},
	{id: "A-38", name: "perm=dynamic", layers: []layerSrc{stencil(dynamicTool)}, call: moxyCall("folio", "read", manifestAsk, fsPath(wt+"/a.go")), outcome: OutcomeAdmitted, perm: PermDefault},
	{id: "A-39", name: "a child restating without perm puts the tier back", layers: []layerSrc{stencil(stencilA), stencil(stencilM)}, call: moxyCall("folio", "read", manifestAsk, fsPath(wt+"/a.go")), outcome: OutcomeAdmitted, perm: PermDefault},
	{id: "A-40", name: "a child cannot loosen ask", layers: []layerSrc{stencil(askTool), stencil(stencilA)}, call: moxyCall("folio", "read", readTool, fsPath(wt+"/a.go")), outcome: OutcomeAsk, asks: []string{"0@2"}},
	{id: "A-42", name: "deny outranks ask; the ask is still listed", layers: []layerSrc{stencil(askTool), patch(denyReadTool)}, call: moxyCall("folio", "read", readTool, fsPath(wt+"/a.go")), outcome: OutcomeRefused, asks: []string{"0@2"}, refusals: []string{"1:deny@2"}},
	{id: "A-43", name: "ask on a path node, under it", layers: []layerSrc{stencil(askInfra)}, call: moxyCall("folio", "read", readTool, fsPath(wt+"/infra/x")), outcome: OutcomeAsk, asks: []string{"0@3"}},
	{id: "A-43", name: "ask on a path node, beside it", layers: []layerSrc{stencil(askInfra)}, call: moxyCall("folio", "read", readTool, fsPath(wt+"/src/x")), outcome: OutcomeAdmitted, perm: PermAllow},
	{id: "A-44", name: "ask on a sticky node, three steps below", layers: []layerSrc{stencil(cgStencil(`[-> ynab:main !cg-root-v1 subtree op=[read] perm=ask]`))}, call: ynabTxn("read", "budget/b1"), outcome: OutcomeAsk, asks: []string{"0@3"}},
	{id: "A-45", name: "no allowance outranks ask", layers: []layerSrc{stencil(askTool), stencil(stencilC)}, call: moxyCall("folio", "read", readTool, fsPath("file:/nix/store/x")), outcome: OutcomeRefused, refusals: []string{"0:no-allowance@3", "1:no-allowance@3"}},
}

func TestAskVectors(t *testing.T) {
	for _, v := range askVectors {
		t.Run(v.id+": "+v.name, func(t *testing.T) {
			got := Evaluate(parseLayers(t, v.layers), v.call)
			if got.Outcome != v.outcome {
				t.Errorf("Outcome = %v, want %v\n%s", got.Outcome, v.outcome, got)
			}
			if got.Admitted != (v.outcome == OutcomeAdmitted) {
				t.Errorf("Admitted = %v with Outcome %v; Admitted must be false for anything but Admitted", got.Admitted, got.Outcome)
			}
			if v.outcome == OutcomeAdmitted && got.Perm != v.perm {
				t.Errorf("Perm = %v, want %v", got.Perm, v.perm)
			}
			if spelled := spellAsks(got.Asks); strings.Join(spelled, " ") != strings.Join(v.asks, " ") {
				t.Errorf("asks = %v, want %v\n%s", spelled, v.asks, got)
			}
			if !refusalsMatch(got.Refusals, v.refusals) {
				t.Errorf("refusals = %v, want %v\n%s", spellRefusals(got.Refusals), v.refusals, got)
			}
		})
	}
}

func parseLayers(t *testing.T, srcs []layerSrc) []Layer {
	t.Helper()
	var layers []Layer
	for _, l := range srcs {
		if l.kind == layerPatch {
			p, err := ParsePatch(l.src)
			if err != nil {
				t.Fatalf("ParsePatch: %v", err)
			}
			layers = append(layers, p)
			continue
		}
		s, err := ParseStencil(l.src)
		if err != nil {
			t.Fatalf("ParseStencil: %v", err)
		}
		layers = append(layers, s)
	}
	return layers
}

func spellAsks(asks []Ask) []string {
	out := make([]string, len(asks))
	for i, a := range asks {
		out[i] = fmt.Sprintf("%d@%d", a.Layer, a.Depth)
	}
	return out
}

// TestPermParse is A-46 (deny-only half) and A-47: what a perm atom may not
// be. Each case errors naming the atom or the node.
func TestPermParse(t *testing.T) {
	for _, c := range []struct {
		id, name string
		layer    layerSrc
		mention  string
	}{
		{"A-46", "a patch cannot loosen", patch(`[moxy [-> folio [-> read perm=allow deny]]]`), "perm=allow"},
		{"A-46", "a patch leaf asking (ask-in-patches pending a ruling)", patch(`[moxy [-> folio [-> read perm=ask]]]`), "perm=ask"},
		{"A-47", "an unknown value", stencil(`[moxy [-> folio [-> read perm=bogus]]]`), "perm=bogus"},
		{"A-47", "a list", stencil(`[moxy [-> folio [-> read perm=[allow, ask]]]]`), "perm=[allow, ask]"},
		{"A-47", "negated", stencil(`[moxy [-> folio [-> read ^perm=ask]]]`), "^perm=ask"},
		{"A-47", "an operator other than =", stencil(`[moxy [-> folio [-> read perm^=a]]]`), "perm^=a"},
		{"A-47", "perm and deny on one node", stencil(`[moxy [-> folio [-> read perm=ask deny]]]`), "deny"},
		{"A-47", "two perm atoms", stencil(`[moxy [-> folio [-> read perm=ask perm=allow]]]`), "perm"},
		{"A-47", "perm matches nothing, so a typed _ with only perm is a wildcard", stencil(`[moxy [-> folio [-> _ !mcp-tool-v1 perm=ask]]]`), "open"},
	} {
		t.Run(c.id+": "+c.name, func(t *testing.T) {
			var err error
			if c.layer.kind == layerPatch {
				_, err = ParsePatch(c.layer.src)
			} else {
				_, err = ParseStencil(c.layer.src)
			}
			if err == nil {
				t.Fatal("parsed; want an error")
			}
			if !strings.Contains(err.Error(), c.mention) {
				t.Fatalf("error %q does not name %q", err, c.mention)
			}
		})
	}
	// A quoted field name is opaque: `"perm"=x` is an ordinary atom.
	if _, err := ParseStencil(`[moxy [-> folio [-> read "perm"=x]]]`); err != nil {
		t.Fatalf("a quoted perm field was reserved: %v", err)
	}
}

// TestPermRoundTrip is A-48: Write spells perm exactly as read, on any node.
func TestPermRoundTrip(t *testing.T) {
	for _, src := range []string{askInfra, dynamicTool, askTool} {
		s, err := ParseStencil(src)
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		s.Write(&b)
		for _, atom := range []string{"perm=ask", "perm=allow", "perm=dynamic"} {
			if strings.Count(src, atom) != strings.Count(b.String(), atom) {
				t.Errorf("%s: Write changed the count of %s:\n%s", src, atom, b.String())
			}
		}
		again, err := ParseStencil(b.String())
		if err != nil {
			t.Fatalf("re-parse: %v", err)
		}
		var b2 strings.Builder
		again.Write(&b2)
		if b.String() != b2.String() {
			t.Errorf("not a fixed point:\n%s\n---\n%s", b.String(), b2.String())
		}
	}
}

// TestVerdictString_Ask pins an ask's one-line rendering.
func TestVerdictString_Ask(t *testing.T) {
	v := Evaluate(parseLayers(t, []layerSrc{stencil(askTool)}), moxyCall("folio", "read", readTool, fsPath(wt+"/a.go")))
	want := "layer 0 (stencil): ask [read !mcp-tool-v1 kind=read perm=ask] at moxy/folio/read"
	if strings.TrimSpace(v.String()) != want {
		t.Fatalf("String() = %q, want %q", v.String(), want)
	}
}

// TestResolve is A-49: the enforcer applies the session posture, and Ask
// never silently becomes Run.
func TestResolve(t *testing.T) {
	for _, c := range []struct {
		outcome     Outcome
		posture     Posture
		canEscalate bool
		want        Action
	}{
		{OutcomeAdmitted, PostureUnattended, false, ActionRun},
		{OutcomeRefused, PostureAttended, true, ActionRefuse},
		{OutcomeAsk, PostureAttended, false, ActionPrompt},
		{OutcomeAsk, PostureAttended, true, ActionPrompt},
		{OutcomeAsk, PostureUnattended, true, ActionEscalate},
		{OutcomeAsk, PostureUnattended, false, ActionRefuse},
	} {
		if got := (Verdict{Outcome: c.outcome}).Resolve(c.posture, c.canEscalate); got != c.want {
			t.Errorf("Resolve(%v, %v) of %v = %v, want %v", c.posture, c.canEscalate, c.outcome, got, c.want)
		}
	}
	// The zero Verdict refuses: an outcome nobody set is never Run.
	if got := (Verdict{}).Resolve(PostureAttended, true); got != ActionRefuse {
		t.Errorf("the zero Verdict resolves to %v, want refuse", got)
	}
}
