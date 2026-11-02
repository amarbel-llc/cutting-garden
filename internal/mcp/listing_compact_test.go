package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Listings are emitted as compact JSON: every list_nodes path and the
// resources/read container listing, which stay byte-identical to each other on
// the default path.
func TestListings_AreCompactJSON(t *testing.T) {
	tools := newFakeTools(t, &fakeMutator{}, "faketest://h/")
	tools.resolveLister = listerResolve(projLister{})
	r := newFakeResources(t, "faketest://h/")
	r.resolve = tools.resolveLister
	tools.reader = r
	for name, args := range map[string]string{
		"default":  `{"uri":"faketest://h/work"}`,
		"paged":    `{"uri":"faketest://h/work","limit":1}`,
		"bare":     `{"uri":"faketest://h/work","bare":true}`,
		"filtered": `{"uri":"faketest://h/work","filter":"priority=3_unspecified"}`,
		"fields":   `{"uri":"faketest://h/work","fields":["summary"]}`,
		"query":    `{"uri":"faketest://h/","query":"!test-calendar-v1 -> !test-object-v1"}`,
		"roots":    `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			res, err := tools.CallTool(context.Background(), "list_nodes", json.RawMessage(args))
			if err != nil || res.IsError {
				t.Fatalf("err=%v res=%+v", err, res)
			}
			text := res.Content[0].Text
			if strings.Contains(text, "\n") || strings.Contains(text, `": `) {
				t.Errorf("listing is indented: %.160q", text)
			}
		})
	}

	// resources/read of the container is byte-identical to list_nodes' default.
	got, err := r.ReadResource(context.Background(), "faketest://h/work")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	res, _ := tools.CallTool(context.Background(), "list_nodes", json.RawMessage(`{"uri":"faketest://h/work"}`))
	if got.Contents[0].Text != res.Content[0].Text {
		t.Errorf("resources/read and list_nodes default differ:\n%.200q\n%.200q", got.Contents[0].Text, res.Content[0].Text)
	}
}
