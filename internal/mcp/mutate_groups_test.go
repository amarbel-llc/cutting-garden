package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"code.linenisgreat.com/purse-first/libs/go-mcp/protocol"
)

func callMutateGroups(t *testing.T, tools *Tools, groups string) *protocol.ToolCallResult {
	t.Helper()
	res, err := tools.CallTool(context.Background(), "mutate_nodes",
		json.RawMessage(`{"groups":[`+groups+`]}`))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	return res
}

type groupsJSON struct {
	Groups []struct {
		Anchor  string   `json:"anchor"`
		Applied []string `json:"applied"`
		Error   string   `json:"error"`
	} `json:"groups"`
}

// Several groups apply in order, each reported under its own anchor
// (cutting-garden#323).
func TestMutateNodes_SeveralGroupsApplyInOrder(t *testing.T) {
	m := &fakeMutator{}
	tools := newFakeTools(t, m, "faketest://h/")

	res := callMutateGroups(t, tools,
		`{"anchor":"faketest://h/a/","ops":[{"kind":"put","id":"x.ics","body":"1"}]},`+
			`{"anchor":"faketest://h/b/","ops":[{"kind":"delete","id":"y.ics"}]}`)
	if res.IsError {
		t.Fatalf("errored: %+v", res.Content)
	}
	if !slices.Equal(m.put, []string{"faketest://h/a/x.ics"}) ||
		!slices.Equal(m.deleted, []string{"faketest://h/b/y.ics"}) {
		t.Errorf("put %v deleted %v", m.put, m.deleted)
	}
	var got groupsJSON
	if err := json.Unmarshal([]byte(res.Content[0].Text), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Groups) != 2 || got.Groups[0].Anchor != "faketest://h/a/" ||
		got.Groups[1].Anchor != "faketest://h/b/" {
		t.Errorf("groups = %+v", got.Groups)
	}
}

// A malformed group refuses the whole call before any group writes.
func TestMutateNodes_MalformedGroupWritesNothing(t *testing.T) {
	m := &fakeMutator{}
	tools := newFakeTools(t, m, "faketest://h/")

	res := callMutateGroups(t, tools,
		`{"anchor":"faketest://h/a/","ops":[{"kind":"put","id":"x.ics","body":"1"}]},`+
			`{"ops":[{"kind":"delete","id":"y.ics"}]}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "groups[1]") {
		t.Fatalf("want an error naming groups[1], got %+v", res.Content)
	}
	if len(m.put) != 0 || len(m.deleted) != 0 {
		t.Errorf("a refused call wrote: put %v deleted %v", m.put, m.deleted)
	}
}

// A group refused at dispatch reports its error; the others still apply,
// and the call succeeds because something landed.
func TestMutateNodes_RefusedGroupDoesNotStopTheNext(t *testing.T) {
	m := &fakeMutator{}
	tools := newFakeTools(t, m, "faketest://h/")

	res := callMutateGroups(t, tools,
		`{"anchor":"faketest://h/a/","atomicity":"atomic","ops":[{"kind":"put","id":"x.ics","body":"1"}]},`+
			`{"anchor":"faketest://h/b/","ops":[{"kind":"delete","id":"y.ics"}]}`)
	if res.IsError {
		t.Fatalf("errored: %+v", res.Content)
	}
	var got groupsJSON
	if err := json.Unmarshal([]byte(res.Content[0].Text), &got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Groups[0].Error, "atomically") || len(got.Groups[0].Applied) != 0 {
		t.Errorf("refused group = %+v", got.Groups[0])
	}
	if !slices.Equal(got.Groups[1].Applied, []string{"y.ics"}) {
		t.Errorf("second group = %+v", got.Groups[1])
	}
}

// When every group is refused, the call is an error result.
func TestMutateNodes_AllGroupsRefusedIsAnError(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")

	res := callMutateGroups(t, tools,
		`{"anchor":"faketest://h/a/","atomicity":"atomic","ops":[{"kind":"put","id":"x.ics","body":"1"}]},`+
			`{"anchor":"faketest://h/b/","atomicity":"atomic","ops":[{"kind":"delete","id":"y.ics"}]}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "nothing was applied") {
		t.Errorf("want a nothing-applied error, got %+v", res.Content)
	}
}
