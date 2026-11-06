package mcp

import (
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

type outcomeJSON struct {
	ID     string   `json:"id"`
	Status string   `json:"status"`
	Fields []string `json:"fields"`
	URI    string   `json:"uri"`
	Name   string   `json:"name"`
	Error  string   `json:"error"`
}

func outcomesOf(t *testing.T, text string) []outcomeJSON {
	t.Helper()
	var v struct {
		Groups []struct {
			Outcomes []outcomeJSON `json:"outcomes"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("decode %q: %v", text, err)
	}
	return v.Groups[0].Outcomes
}

func mustU(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// mutate_nodes reports each op's outcome in the caller's ids: the applied
// fields (#320), the new URI only when it differs (#359), and a delete's name
// and not-found status (#362).
func TestMutateNodes_ReportsOutcomes(t *testing.T) {
	bm := &fakeBulkMutator{}
	bm.result = cutting_garden_plugins.BulkResult{
		AppliedNodes: []*url.URL{mustU(t, "faketest://h/p.ics"), mustU(t, "faketest://h/d.ics")},
		Failed: []cutting_garden_plugins.BulkFailure{
			{URI: mustU(t, "faketest://h/gone.ics"), Err: "no such node"},
		},
		Outcomes: []cutting_garden_plugins.BulkOutcome{
			{
				URI: mustU(t, "faketest://h/p.ics"), Status: cutting_garden_plugins.OutcomeApplied,
				Fields: []string{"summary", "priority"}, NewURI: mustU(t, "faketest://h/p.ics?id=7"),
			},
			{URI: mustU(t, "faketest://h/d.ics"), Status: cutting_garden_plugins.OutcomeDeleted, Name: "Dee"},
			{
				URI: mustU(t, "faketest://h/gone.ics"), Status: cutting_garden_plugins.OutcomeNotFound,
				Err: "no such node",
			},
		},
	}
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.resolveBulk = fakeBulkResolve(bm)

	res := callMutate(t, tools, `"ops":[`+
		`{"kind":"patch","id":"p.ics","body":"{}"},`+
		`{"kind":"delete","id":"d.ics"},`+
		`{"kind":"delete","id":"gone.ics"}]`)
	if res.IsError {
		t.Fatalf("errored: %+v", res.Content)
	}
	got := outcomesOf(t, res.Content[0].Text)
	want := []outcomeJSON{
		{ID: "p.ics", Status: "applied", Fields: []string{"summary", "priority"}, URI: "faketest://h/p.ics?id=7"},
		{ID: "d.ics", Status: "deleted", Name: "Dee"},
		{ID: "gone.ics", Status: "not_found", Error: "no such node"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("outcomes\n got %+v\nwant %+v", got, want)
	}
}

// Each op's outcome is logged as it lands, so a reply lost with its
// connection leaves a record of what applied (cutting-garden#392).
func TestMutateNodes_LogsEachOutcomeAsItLands(t *testing.T) {
	var log strings.Builder
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.log = &log

	res := callMutate(t, tools, `"ops":[`+
		`{"kind":"put","id":"a.ics","body":"x"},`+
		`{"kind":"delete","id":"b.ics"}]`)
	if res.IsError {
		t.Fatalf("errored: %+v", res.Content)
	}
	for _, want := range []string{
		"mutate_nodes faketest://h/ op 1/2: applied a.ics",
		"mutate_nodes faketest://h/ op 2/2: deleted b.ics",
	} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, log.String())
		}
	}
}

// A plugin that reports no outcomes gets no outcomes key: the four lists are
// all there is, and rebuilding an entry per id from them would only repeat
// them.
func TestMutateNodes_NoOutcomesKeyWhenThePluginReportedNone(t *testing.T) {
	bm := &fakeBulkMutator{}
	bm.result = cutting_garden_plugins.BulkResult{
		AppliedNodes:   []*url.URL{mustU(t, "faketest://h/a.ics")},
		PatchedNothing: []*url.URL{mustU(t, "faketest://h/b.ics")},
		Failed: []cutting_garden_plugins.BulkFailure{
			{URI: mustU(t, "faketest://h/c.ics"), Err: "boom"},
		},
	}
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.resolveBulk = fakeBulkResolve(bm)

	res := callMutate(t, tools, `"ops":[`+
		`{"kind":"patch","id":"a.ics","body":"{}"},`+
		`{"kind":"patch","id":"b.ics","body":"{}"},`+
		`{"kind":"patch","id":"c.ics","body":"{}"}]`)
	if res.IsError {
		t.Fatalf("errored: %+v", res.Content)
	}
	if strings.Contains(res.Content[0].Text, `"outcomes"`) {
		t.Errorf("an outcomes key without plugin detail: %s", res.Content[0].Text)
	}
	for _, key := range []string{`"applied"`, `"patched_nothing"`, `"failed"`} {
		if !strings.Contains(res.Content[0].Text, key) {
			t.Errorf("the lists lost %s: %s", key, res.Content[0].Text)
		}
	}
}
