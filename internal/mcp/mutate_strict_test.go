package mcp

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// declaringMutator is a plugin that declares the writable keys of its nodes:
// every path under /decl/ accepts summary and priority.
type declaringMutator struct{ *fakeMutator }

func (declaringMutator) DescribePatchKeys() []cutting_garden_plugins.NodeTypePatchKeys {
	return []cutting_garden_plugins.NodeTypePatchKeys{
		{Tag: "decl-v1", Keys: []string{"priority", "summary"}},
	}
}

func (declaringMutator) PatchTarget(uri *url.URL) (string, bool) {
	if strings.HasPrefix(uri.Path, "/decl/") {
		return "decl-v1", true
	}
	return "", false
}

// strictFixture wires a declaring plugin whose BulkMutate is recorded.
func strictFixture(t *testing.T) (*Tools, *fakeBulkMutator) {
	t.Helper()
	m := declaringMutator{&fakeMutator{}}
	tools := newFakeTools(t, m.fakeMutator, "faketest://h/")
	tools.resolveLister = fakeListerResolve(m)
	bm := &fakeBulkMutator{}
	tools.resolveBulk = fakeBulkResolve(bm)
	return tools, bm
}

func callMutateTop(t *testing.T, tools *Tools, strict bool, groupBody string) string {
	t.Helper()
	args := `{"groups":[{"anchor":"faketest://h/",` + groupBody + `}]}`
	if strict {
		args = `{"strict":true,"groups":[{"anchor":"faketest://h/",` + groupBody + `}]}`
	}
	res, err := tools.CallTool(t.Context(), "mutate_nodes", json.RawMessage(args))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	return resultText(res)
}

func TestMutateNodes_TolerantDispatchesTheStrippedBodyAndReportsIgnored(t *testing.T) {
	tools, bm := strictFixture(t)
	a, _ := url.Parse("faketest://h/decl/a")
	bm.result = cutting_garden_plugins.BulkResult{
		AppliedNodes: []*url.URL{a},
		Outcomes: []cutting_garden_plugins.BulkOutcome{
			{URI: a, Status: cutting_garden_plugins.OutcomeApplied, Fields: []string{"summary"}},
		},
	}

	text := callMutateTop(t, tools, false,
		`"ops":[{"kind":"patch","id":"decl/a","body":"{\"summary\":\"x\",\"oops\":1}"}]`)

	if len(bm.gotReq.Ops) != 1 || strings.Contains(string(bm.gotReq.Ops[0].Body), "oops") {
		t.Fatalf("plugin saw %+v, want the body without the undeclared key", bm.gotReq.Ops)
	}
	if !strings.Contains(text, `"ignored"`) || !strings.Contains(text, `"oops"`) {
		t.Errorf("result does not report the ignored key: %s", text)
	}
}

func TestMutateNodes_TolerantWithoutADeclarationIsUnchanged(t *testing.T) {
	tools, bm := strictFixture(t)

	callMutateTop(t, tools, false,
		`"ops":[{"kind":"patch","id":"other/a","body":"{\"anything\":1}"}]`)

	if len(bm.gotReq.Ops) != 1 || string(bm.gotReq.Ops[0].Body) != `{"anything":1}` {
		t.Errorf("plugin saw %+v, want the body exactly as sent", bm.gotReq.Ops)
	}
}

func TestMutateNodes_StrictRefusesATypoedKeyAndDispatchesNothing(t *testing.T) {
	tools, bm := strictFixture(t)

	text := callMutateTop(t, tools, true,
		`"ops":[{"kind":"patch","id":"decl/a","body":"{\"summry\":\"x\"}"}]`)

	if bm.gotReq.Atomicity != "" {
		t.Errorf("the plugin was dispatched despite the strict refusal: %+v", bm.gotReq)
	}
	if !strings.Contains(text, "summry") || !strings.Contains(text, "nothing was written") {
		t.Errorf("refusal does not name the key: %s", text)
	}
}

func TestMutateNodes_StrictRefusesAPatchToAnUndeclaredType(t *testing.T) {
	tools, bm := strictFixture(t)

	text := callMutateTop(t, tools, true,
		`"ops":[{"kind":"patch","id":"other/a","body":"{\"summary\":\"x\"}"}]`)

	if bm.gotReq.Atomicity != "" {
		t.Errorf("dispatched an unverifiable patch under strict: %+v", bm.gotReq)
	}
	if !strings.Contains(text, "does not declare") {
		t.Errorf("refusal = %s", text)
	}
}

// Best-effort: the refused op fails alone and the clean one is applied.
func TestMutateNodes_StrictRefusalFailsOnlyThatOp(t *testing.T) {
	tools, bm := strictFixture(t)
	ok, _ := url.Parse("faketest://h/decl/ok")
	bm.result = cutting_garden_plugins.BulkResult{
		AppliedNodes: []*url.URL{ok},
		Outcomes: []cutting_garden_plugins.BulkOutcome{
			{URI: ok, Status: cutting_garden_plugins.OutcomeApplied, Fields: []string{"summary"}},
		},
	}

	text := callMutateTop(t, tools, true, `"ops":[`+
		`{"kind":"patch","id":"decl/bad","body":"{\"oops\":1}"},`+
		`{"kind":"patch","id":"decl/ok","body":"{\"summary\":\"x\"}"}]`)

	if len(bm.gotReq.Ops) != 1 || bm.gotReq.Ops[0].URI.Path != "/decl/ok" {
		t.Fatalf("plugin saw %+v, want only the clean op", bm.gotReq.Ops)
	}
	var out struct {
		Groups []mutateGroupView `json:"groups"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, text)
	}
	view := out.Groups[0]
	if len(view.Applied) != 1 || len(view.Failed) != 1 || view.Failed[0].ID != "decl/bad" {
		t.Fatalf("applied %v, failed %+v", view.Applied, view.Failed)
	}
	if len(view.Outcomes) != 2 || view.Outcomes[0].ID != "decl/bad" ||
		view.Outcomes[0].Status != "failed" || view.Outcomes[1].Status != "applied" {
		t.Errorf("outcomes not in request order: %+v", view.Outcomes)
	}
}

func TestMutateNodes_StrictAtomicRefusalWritesNothing(t *testing.T) {
	tools, bm := strictFixture(t)

	text := callMutateTop(t, tools, true, `"atomicity":"atomic","ops":[`+
		`{"kind":"patch","id":"decl/a","body":"{\"summary\":\"x\"}"},`+
		`{"kind":"patch","id":"decl/b","body":"{\"oops\":1}"}]`)

	if bm.gotReq.Atomicity != "" {
		t.Errorf("dispatched an atomic batch with a strict refusal in it: %+v", bm.gotReq)
	}
	if !strings.Contains(text, "oops") {
		t.Errorf("refusal does not name the key: %s", text)
	}
}

func TestMutateNodes_StrictRefusesAPatchSweep(t *testing.T) {
	tools, bm := strictFixture(t)

	text := callMutateTop(t, tools, true,
		`"sweep":{"filter":"","op":{"kind":"patch","body":"{\"summary\":\"x\"}"}}`)

	if bm.gotReq.Atomicity != "" || !strings.Contains(text, "patch sweep") {
		t.Errorf("sweep under strict: dispatched=%+v text=%s", bm.gotReq, text)
	}
}

// A tolerant op whose every key is undeclared is settled by the host: the
// plugin is never sent an empty body.
func TestMutateNodes_TolerantAllIgnoredIsPatchedNothingWithoutDispatch(t *testing.T) {
	tools, bm := strictFixture(t)

	text := callMutateTop(t, tools, false,
		`"ops":[{"kind":"patch","id":"decl/a","body":"{\"oops\":1}"}]`)

	if bm.gotReq.Atomicity != "" {
		t.Errorf("dispatched an op with nothing declared left: %+v", bm.gotReq)
	}
	if !strings.Contains(text, "patched_nothing") || !strings.Contains(text, "oops") {
		t.Errorf("result = %s", text)
	}
}
