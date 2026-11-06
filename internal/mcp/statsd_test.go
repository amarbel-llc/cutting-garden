package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
)

// Each mis-encoding #319 measures is told apart from a well-formed body, and
// a raw (iCalendar) create body is never mistaken for broken JSON.
func TestClassifyMutateBody(t *testing.T) {
	for _, tc := range []struct {
		name, kind, body, want string
	}{
		{"object patch", "patch", `{"summary":"x"}`, ""},
		{"empty delete", "delete", "", ""},
		{"raw ical create", "create", "BEGIN:VCALENDAR\nEND:VCALENDAR\n", ""},
		{"invalid json patch", "patch", `{"summary":`, "invalid_json"},
		{"plain text patch", "patch", `summary=x`, "invalid_json"},
		{"invalid json-looking create", "create", `{"title":`, "invalid_json"},
		{"json string wrapping json", "patch", `"{\"summary\":\"x\"}"`, "json_in_json_string"},
		{"array patch", "patch", `[{"summary":"x"}]`, "not_object"},
		{"number patch", "patch", `3`, "not_object"},
	} {
		if got := classifyMutateBody(tc.kind, tc.body); got != tc.want {
			t.Errorf("%s: classifyMutateBody(%q, %q) = %q, want %q",
				tc.name, tc.kind, tc.body, got, tc.want)
		}
	}
}

// A body sent as a JSON object instead of a string fails argument decoding;
// it is still counted, by scheme, before the error goes back.
func TestMutateNodes_CountsCallsAndBodyShapes(t *testing.T) {
	var counted []string
	saved := emitCounter
	emitCounter = func(name string) { counted = append(counted, name) }
	t.Cleanup(func() { emitCounter = saved })

	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	callMutate(t, tools, `"ops":[{"kind":"patch","id":"a.ics","body":"\"{}\""}]`)
	_, _ = tools.CallTool(context.Background(), "mutate_nodes", json.RawMessage(
		`{"groups":[{"anchor":"faketest://h/","ops":[{"kind":"patch","id":"a.ics","body":{"x":1}}]}]}`))

	want := []string{
		"cutting_garden.mcp.mutate_nodes.faketest.calls",
		"cutting_garden.mcp.mutate_nodes.faketest.body.json_in_json_string",
		"cutting_garden.mcp.mutate_nodes.faketest.calls",
		"cutting_garden.mcp.mutate_nodes.faketest.body.object_not_string",
	}
	if !slices.Equal(counted, want) {
		t.Errorf("counted %v\nwant    %v", counted, want)
	}
}
