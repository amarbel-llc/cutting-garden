package org

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

var createdStampRe = regexp.MustCompile(`:CREATED:  \[\d{4}-\d{2}-\d{2} [A-Z][a-z]{2} \d{2}:\d{2}\]`)

func create(t *testing.T, container, body string) (string, error) {
	t.Helper()
	u, err := Plugin{}.CreateChild(context.Background(), mustURL(t, container), strings.NewReader(body), "")
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func normalizeCapture(s string) string {
	return createdStampRe.ReplaceAllString(replaceUUID(s), ":CREATED:  <now>")
}

func TestCapture(t *testing.T) {
	cases := []struct {
		name, src, container, body, want string
	}{
		{
			"in a file: a level-1 headline at its end",
			"* A\n** A1\n",
			"org://test/c.org", `{"title": "New thing"}`,
			"* A\n** A1\n" +
				"* New thing :zz-agent_captured:\n:PROPERTIES:\n:ID:       <uuid>\n:CREATED:  <now>\n:END:\n",
		},
		{
			"under a headline: its last child, with metadata and body",
			"* A\n:PROPERTIES:\n:ID: a\n:END:\n** A1\n* B\n",
			"org://test/c.org?id=a",
			`{"title": "Call back", "state": "TODO", "priority": "A", "own_tags": ["area-home"], ` +
				`"scheduled": "2026-10-20", "body": "Ask about the leak."}`,
			"* A\n:PROPERTIES:\n:ID: a\n:END:\n** A1\n" +
				"** TODO [#A] Call back :area-home:zz-agent_captured:\nSCHEDULED: <2026-10-20 Tue>\n" +
				":PROPERTIES:\n:ID:       <uuid>\n:CREATED:  <now>\n:END:\nAsk about the leak.\n" +
				"* B\n",
		},
		{
			"an empty file",
			"",
			"org://test/c.org", `{"title": "First"}`,
			"* First :zz-agent_captured:\n:PROPERTIES:\n:ID:       <uuid>\n:CREATED:  <now>\n:END:\n",
		},
		{
			"a file not ending in a newline, CRLF kept",
			"* A\r\n* B",
			"org://test/c.org", `{"title": "C"}`,
			"* A\r\n* B\r\n* C :zz-agent_captured:\r\n:PROPERTIES:\r\n:ID:       <uuid>\r\n" +
				":CREATED:  <now>\r\n:END:\r\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := setup(t)
			srv.Put("c.org", []byte(c.src))
			uri, err := create(t, c.container, c.body)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if got := normalizeCapture(string(srv.Content("c.org"))); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
			id := uuidRe.FindString(string(srv.Content("c.org")))
			if uri != "org://test/c.org?id="+id {
				t.Errorf("created URI = %q, want the minted ?id=%s", uri, id)
			}
		})
	}
}

// The capture tag is the new headline's own; its children do not inherit it.
func TestCapture_TagNotInherited(t *testing.T) {
	srv := setup(t)
	srv.Put("c.org", []byte(""))
	parent, err := create(t, "org://test/c.org", `{"title": "Parent"}`)
	if err != nil {
		t.Fatal(err)
	}
	child, err := create(t, parent, `{"title": "Child", "own_tags": ["x"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(srv.Content("c.org")), "** Child :x:zz-agent_captured:\n") {
		t.Errorf("child not created under the parent:\n%s", srv.Content("c.org"))
	}
	n, err := Plugin{}.ListRoots(context.Background(), mustURL(t, parent))
	if err != nil || len(n) != 1 || n[0].URIString() != child {
		t.Fatalf("parent's children = %v, %v; want [%s]", n, err, child)
	}
}

func TestCapture_Refusals(t *testing.T) {
	srv := setup(t)
	srv.Put("c.org", []byte("* A\n"))
	for _, c := range []struct{ container, body, wantErr string }{
		{"org://test/c.org", `{"state": "TODO"}`, `needs a "title"`},
		{"org://test/c.org", `{"title": "TODO sneaky"}`, "would not read back"},
		{"org://test/c.org", `{"title": "x", "quote": true}`, "not a create key"},
		{"org://test/c.org", `{"title": "x", "parent": "c.org"}`, "not a create key"},
		{"org://test/c.org", `{"title": "x", "state": "WAIT"}`, "not a TODO keyword"},
		{"org://test/", `{"title": "x"}`, "in a file or under a headline"},
		{"org://test/c.org?id=nope", `{"title": "x"}`, "no such headline"},
	} {
		if _, err := create(t, c.container, c.body); err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s %s: err = %v, want it to mention %q", c.container, c.body, err, c.wantErr)
		}
	}
	if got := string(srv.Content("c.org")); got != "* A\n" {
		t.Errorf("a refused create must not write: %q", got)
	}
	if _, err := (Plugin{}).CreateChild(context.Background(), mustURL(t, "org://test/c.org"),
		strings.NewReader(`{"title": "x"}`), "cutting_garden-org-file-v1"); err == nil {
		t.Error("only headlines can be created")
	}
}
