package mutate

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

func put(id string) OpInput    { return OpInput{Kind: "put", ID: id, Body: "1"} }
func remove(id string) OpInput { return OpInput{Kind: "delete", ID: id} }

// Several groups apply in order, each reported under its own anchor, and the
// plugin sees the ops addressed under their group's anchor.
func TestApply_SeveralGroupsApplyInOrder(t *testing.T) {
	m := &mutFake{}
	result, err := fakeEngine(m).Apply(context.Background(), Input{Groups: []GroupInput{
		{Anchor: "faketest://h/a/", Ops: []OpInput{put("x.ics")}},
		{Anchor: "faketest://h/b/", Ops: []OpInput{remove("y.ics")}},
	}}, "")
	if err != nil {
		t.Fatal(err)
	}

	equalStrings(t, "put", m.put, []string{"faketest://h/a/x.ics"})
	equalStrings(t, "deleted", m.deleted, []string{"faketest://h/b/y.ics"})
	if len(result.Groups) != 2 ||
		result.Groups[0].Anchor != "faketest://h/a/" ||
		result.Groups[1].Anchor != "faketest://h/b/" {
		t.Errorf("groups = %+v", result.Groups)
	}
	equalStrings(t, "group 0 applied", result.Groups[0].Applied, []string{"x.ics"})
	if result.NothingApplied() {
		t.Error("NothingApplied() = true for a call that applied both groups")
	}
}

// A malformed group refuses the whole call before any group writes
// (cutting-garden#323), and names the group.
func TestApply_MalformedGroupWritesNothing(t *testing.T) {
	m := &mutFake{}
	_, err := fakeEngine(m).Apply(context.Background(), Input{Groups: []GroupInput{
		{Anchor: "faketest://h/a/", Ops: []OpInput{put("x.ics")}},
		{Ops: []OpInput{remove("y.ics")}},
	}}, "")
	if err == nil || !strings.Contains(err.Error(), "groups[1]") {
		t.Fatalf("err = %v, want one naming groups[1]", err)
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("a malformed group is the caller's fault (64), got %v", err)
	}
	if len(m.put) != 0 || len(m.deleted) != 0 {
		t.Errorf("a refused call wrote: put %v deleted %v", m.put, m.deleted)
	}
}

func TestApply_NoGroupsIsABadRequest(t *testing.T) {
	_, err := fakeEngine(&mutFake{}).Apply(context.Background(), Input{}, "")
	if err == nil || !errors.Is400BadRequest(err) {
		t.Fatalf("err = %v, want a bad request", err)
	}
}

// A group naming both ops and a sweep is refused at build.
func TestApply_OpsAndSweepTogetherIsABadRequest(t *testing.T) {
	_, err := fakeEngine(&mutFake{}).Apply(context.Background(), Input{Groups: []GroupInput{{
		Anchor: "faketest://h/a/",
		Ops:    []OpInput{put("x.ics")},
		Sweep:  &SweepInput{Op: OpInput{Kind: "delete"}},
	}}}, "")
	if err == nil || !strings.Contains(err.Error(), "exactly one of ops or sweep") {
		t.Fatalf("err = %v", err)
	}
}

// A call where every op failed reports NothingApplied so a front can refuse
// to read it as done (cutting-garden#180); one op landing clears it.
func TestResult_NothingApplied(t *testing.T) {
	create := OpInput{Kind: "create", ID: "n.ics", Body: "{}"}

	m := &mutFake{failCreate: true}
	result, err := fakeEngine(m).Apply(context.Background(), Input{Groups: []GroupInput{
		{Anchor: "faketest://h/a/", Ops: []OpInput{create}},
	}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !result.NothingApplied() {
		t.Errorf("NothingApplied() = false; groups = %+v", result.Groups)
	}
	if len(result.Groups[0].Failed) != 1 || result.Groups[0].Failed[0].ID != "n.ics" {
		t.Errorf("failed = %+v", result.Groups[0].Failed)
	}

	// The same failure beside an op that lands is a partial success.
	m = &mutFake{failCreate: true}
	result, err = fakeEngine(m).Apply(context.Background(), Input{Groups: []GroupInput{
		{Anchor: "faketest://h/a/", Ops: []OpInput{create, put("x.ics")}},
	}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.NothingApplied() {
		t.Errorf("NothingApplied() = true although x.ics applied: %+v", result.Groups)
	}
}

// Strict refuses a patch whose plugin declares no keys, and — all ops
// refused — the call applied nothing.
func TestApply_StrictRefusesAnUnverifiablePatch(t *testing.T) {
	m := &mutFake{}
	result, err := fakeEngine(m).Apply(context.Background(), Input{
		Strict: true,
		Groups: []GroupInput{{
			Anchor: "faketest://h/a/",
			Ops:    []OpInput{{Kind: "patch", ID: "x.ics", Body: `{"summary":"s"}`}},
		}},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.patched) != 0 {
		t.Errorf("a strict refusal still dispatched: %v", m.patched)
	}
	if !result.NothingApplied() {
		t.Errorf("NothingApplied() = false; groups = %+v", result.Groups)
	}
}

// Forget is called with the anchor after the dispatch, and the per-op log
// line carries the caller suffix (cutting-garden#392).
func TestApply_ForgetsTheAnchorAndLogsEachOp(t *testing.T) {
	m := &mutFake{}
	engine := fakeEngine(m)
	var forgotten []string
	engine.Forget = func(anchor string) { forgotten = append(forgotten, anchor) }
	var log bytes.Buffer
	engine.Log = &log

	if _, err := engine.Apply(context.Background(), Input{Groups: []GroupInput{
		{Anchor: "faketest://h/a/", Ops: []OpInput{put("x.ics")}},
	}}, ` principal="p"`); err != nil {
		t.Fatal(err)
	}

	equalStrings(t, "forgotten", forgotten, []string{"faketest://h/a/"})
	line := log.String()
	for _, want := range []string{"mutate_nodes faketest://h/a/", "op 1/1", "x.ics", `principal="p"`} {
		if !strings.Contains(line, want) {
			t.Errorf("log %q lacks %q", line, want)
		}
	}
}

func TestEngine_CanWrite(t *testing.T) {
	engine := fakeEngine(&mutFake{})
	if !engine.CanWrite("faketest://h/") {
		t.Error("CanWrite(faketest) = false")
	}
	if engine.CanWrite("nope://h/") {
		t.Error("CanWrite(nope) = true for a scheme no resolver knows")
	}
}

func TestTempKey(t *testing.T) {
	for _, tc := range []struct {
		id       string
		i        int
		isTemp   bool
		wantKey  string
		wantFail bool
	}{
		{id: "task.ics", i: 0, isTemp: false},
		{id: "+t", i: 0, isTemp: true, wantKey: "+t"},
		{id: `+"a b"`, i: 0, isTemp: true, wantKey: "+a b"},
		{id: "+", i: 3, isTemp: true, wantKey: "+\x003"},
		{id: "+", i: -1, isTemp: true, wantFail: true},
		{id: `+""`, i: 0, isTemp: true, wantFail: true},
	} {
		key, isTemp, err := tempKey(tc.i, tc.id)
		if isTemp != tc.isTemp || (err != nil) != tc.wantFail || (!tc.wantFail && key != tc.wantKey) {
			t.Errorf("tempKey(%d, %q) = (%q, %v, %v)", tc.i, tc.id, key, isTemp, err)
		}
	}
}
