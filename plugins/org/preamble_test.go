package org

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func readFileBody(t *testing.T, uri string) string {
	t.Helper()
	leaf, ok, err := Plugin{}.ReadLeaf(context.Background(), mustURL(t, uri))
	if err != nil || !ok {
		t.Fatalf("ReadLeaf %s: ok=%v err=%v", uri, ok, err)
	}
	raw, err := json.Marshal(leaf.Structured)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	return got.Body
}

func jsonBody(t *testing.T, body any) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"body": body})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Replacing the preamble keeps every other byte, and #+TODO / #+FILETAGS
// lines stay in the file (moved above the text).
func TestPreamble_PatchGolden(t *testing.T) {
	cases := []struct {
		name, src, body string
		clear           bool
		applied         []string
		want            string
		wantRead        string // the body read_node returns before the patch
	}{
		{
			name:     "header only",
			src:      "# old header\n* TODO A\nbody\n",
			body:     "# new header\n# second",
			applied:  []string{"body"},
			want:     "# new header\n# second\n* TODO A\nbody\n",
			wantRead: "# old header",
		},
		{
			name:     "no header",
			src:      "* TODO A\nbody\n",
			body:     "# fresh",
			applied:  []string{"body"},
			want:     "# fresh\n* TODO A\nbody\n",
			wantRead: "",
		},
		{
			name:     "header plus keywords",
			src:      "#+TODO: TODO NEXT | DONE\n#+FILETAGS: :a:\n# old\n* NEXT A\n",
			body:     "# new",
			applied:  []string{"body"},
			want:     "#+TODO: TODO NEXT | DONE\n#+FILETAGS: :a:\n# new\n* NEXT A\n",
			wantRead: "# old",
		},
		{
			name:     "keywords interleaved with free text come out above the text",
			src:      "#+TITLE: t\n#+TODO: TODO | DONE\n# note\n* TODO A\n",
			body:     "#+TITLE: u", // #+TITLE is text; only TODO/FILETAGS-style lines are kept apart
			applied:  []string{"body"},
			want:     "#+TODO: TODO | DONE\n#+TITLE: u\n* TODO A\n",
			wantRead: "#+TITLE: t\n# note",
		},
		{
			name:     "clearing with null keeps the keywords",
			src:      "#+TODO: TODO | DONE\n# gone\n* TODO A\n",
			clear:    true,
			applied:  []string{"body"},
			want:     "#+TODO: TODO | DONE\n* TODO A\n",
			wantRead: "# gone",
		},
		{
			name:     "no headlines at all",
			src:      "# only text\n",
			body:     "# other",
			applied:  []string{"body"},
			want:     "# other\n",
			wantRead: "# only text",
		},
		{
			name:     "no headlines, no trailing newline",
			src:      "# only text",
			body:     "# other\nmore",
			applied:  []string{"body"},
			want:     "# other\nmore\n",
			wantRead: "# only text",
		},
		{
			name:     "empty file",
			src:      "",
			body:     "# hello",
			applied:  []string{"body"},
			want:     "# hello\n",
			wantRead: "",
		},
		{
			name:     "CRLF is kept",
			src:      "# old\r\n* TODO A\r\nbody\r\n",
			body:     "# new\n# two",
			applied:  []string{"body"},
			want:     "# new\r\n# two\r\n* TODO A\r\nbody\r\n",
			wantRead: "# old",
		},
		{
			name:     "a blank line before the first headline is text",
			src:      "# old\n\n* TODO A\n",
			body:     "# new\n",
			applied:  []string{"body"},
			want:     "# new\n\n* TODO A\n",
			wantRead: "# old\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := setup(t)
			srv.Put("p.org", []byte(c.src))
			const uri = "org://test/p.org"
			if got := readFileBody(t, uri); got != c.wantRead {
				t.Errorf("read body = %q, want %q", got, c.wantRead)
			}
			var body string
			if c.clear {
				body = jsonBody(t, nil)
			} else {
				body = jsonBody(t, c.body)
			}
			mustPatch(t, uri, body, c.applied...)
			if got := string(srv.Content("p.org")); got != c.want {
				t.Errorf("file\n got  %q\n want %q", got, c.want)
			}
			wantBody := c.body
			if c.clear {
				wantBody = ""
			}
			if got := readFileBody(t, uri); got != wantBody {
				t.Errorf("read after patch = %q, want %q", got, wantBody)
			}
		})
	}
}

// Reading the body and patching it back is no change: patched_nothing and
// no write.
func TestPreamble_RoundTripIsNoOp(t *testing.T) {
	for name, src := range map[string]string{
		"header":         "# h\n\n# i\n* A\n",
		"keywords":       "#+TODO: TODO | DONE\n#+TITLE: t\ntext\n\n* A\n",
		"empty":          "* A\n",
		"crlf":           "# h\r\n* A\r\n",
		"no headlines":   "text\n",
		"trailing blank": "text\n\n\n* A\n",
	} {
		t.Run(name, func(t *testing.T) {
			srv := setup(t)
			srv.Put("r.org", []byte(src))
			body := readFileBody(t, "org://test/r.org")
			mustPatch(t, "org://test/r.org", jsonBody(t, body))
			if n := srv.Requests("PUT"); n != 0 {
				t.Errorf("PUT requests = %d, want 0", n)
			}
			if got := string(srv.Content("r.org")); got != src {
				t.Errorf("file changed: %q", got)
			}
		})
	}
}

func TestPreamble_Refusals(t *testing.T) {
	srv := setup(t)
	const src = "#+TODO: TODO | DONE\n# h\n* TODO A\n"
	srv.Put("f.org", []byte(src))
	for _, c := range []struct{ body, wantErr string }{
		{jsonBody(t, "# ok\n* sneaky headline"), "headline"},
		{jsonBody(t, "#+TODO: A B | C"), "settings"},
		{jsonBody(t, "#+FILETAGS: :x:"), "settings"},
		{jsonBody(t, "#+BEGIN_SRC\nunclosed"), "headline"},
		{`{"body": 3}`, "string or null"},
		{`{"body": "x", "state": "DONE"}`, "only body"},
		{`{"state": "DONE"}`, "only body"},
	} {
		_, err := patch(t, "org://test/f.org", c.body)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want it to mention %q", c.body, err, c.wantErr)
		}
	}
	if got := string(srv.Content("f.org")); got != src {
		t.Errorf("a refused patch must not write: %q", got)
	}
}

// A block in the preamble may hold what looks like a headline or a
// setting; it is text.
func TestPreamble_BlockContentIsText(t *testing.T) {
	srv := setup(t)
	srv.Put("b.org", []byte("* TODO A\n"))
	body := "#+BEGIN_EXAMPLE\n* not a headline\n#+TODO: X | Y\n#+END_EXAMPLE"
	mustPatch(t, "org://test/b.org", jsonBody(t, body), "body")
	if got := readFileBody(t, "org://test/b.org"); got != body {
		t.Errorf("read = %q", got)
	}
}

func TestPreamble_LostToConcurrentSync(t *testing.T) {
	srv := setup(t)
	before := srv.Content("_inbox.org")
	srv.UploadStaleOnUnlock("_inbox.org", before)
	_, err := patch(t, "org://test/_inbox.org", jsonBody(t, "# instructions"))
	if err == nil || !strings.Contains(err.Error(), "lost_to_concurrent_sync") {
		t.Fatalf("err = %v, want lost_to_concurrent_sync", err)
	}
}

func TestPreamble_DescribedWritable(t *testing.T) {
	var found bool
	for _, b := range (Plugin{}).DescribeBodies() {
		if b.Tag == typeFile {
			found = true
			if len(b.Accepts) == 0 || b.Example == nil {
				t.Errorf("file body needs accepts and an example: %+v", b)
			}
		}
	}
	if !found {
		t.Error("the file type must be described writable")
	}
}

func TestPreamble_ReadShowsBody(t *testing.T) {
	srv := setup(t)
	srv.Put("v.org", []byte("#+TODO: TODO | DONE\n# conventions\nsecond\n* A\n"))
	if diff := cmp.Diff("# conventions\nsecond", readFileBody(t, "org://test/v.org")); diff != "" {
		t.Error(diff)
	}
}
