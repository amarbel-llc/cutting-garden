package organize

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestFmtOrganizeRun_RefusesBodyCommentsBeforeAnyStoreTouch drives the run
// path: a pinned document holding a body comment is refused with the
// bad-request error, with no context, config or blob store in play (the
// refusal comes before any of them is read), and the file is left untouched.
func TestFmtOrganizeRun_RefusesBodyCommentsBeforeAnyStoreTouch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "triage.txt")
	original := tagEnvelope + "\n# work\n\n% remember this\n- [a.ics] A\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	err := (&FmtOrganize{output: &strings.Builder{}}).run(nil, nil, path)
	if err == nil {
		t.Fatalf("want a refusal")
	}
	for _, frag := range []string{path, "1 body `%` comment line (body line 3)", "cannot carry"} {
		if !strings.Contains(err.Error(), frag) {
			t.Errorf("refusal %q lacks %q", err, frag)
		}
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil || string(after) != original {
		t.Errorf("the document must be left untouched; err=%v", readErr)
	}
}

// assertSameAsUncommented pins that commented parses to exactly the document
// plain does (lines and sections), and renders to the same base bytes.
func assertSameAsUncommented(t *testing.T, commented, plain string) {
	t.Helper()
	assertSameDocument(t, commented, plain)
	got, want := parseWrapBody(t, commented), parseWrapBody(t, plain)
	if renderCanonical(got) != renderCanonical(want) {
		t.Errorf("a body comment must not reach the base:\n%s\nvs\n%s",
			renderCanonical(got), renderCanonical(want))
	}
}

// TestParseComment_BodyPositions pins that a bare `% ` body line is an inert
// comment wherever no box is open: before the first box, between boxes, under
// a heading, directly under a box line, and trailing.
func TestParseComment_BodyPositions(t *testing.T) {
	for name, tc := range map[string]struct{ commented, plain string }{
		"before the first box": {
			"\n% a note to myself\n- [a.ics] A\n",
			"\n- [a.ics] A\n",
		},
		"between boxes": {
			"\n- [a.ics] A\n\n% note\n- [b.ics] B\n",
			"\n- [a.ics] A\n\n- [b.ics] B\n",
		},
		"under a heading": {
			"\n# work\n\n% note\n- [a.ics] A\n",
			"\n# work\n\n- [a.ics] A\n",
		},
		"directly under a box line": {
			"\n- [a.ics] Grew\n% 5 percent\n- [b.ics] B\n",
			"\n- [a.ics] Grew\n- [b.ics] B\n",
		},
		"trailing": {
			"\n- [a.ics] A\n% the end\n",
			"\n- [a.ics] A\n",
		},
		"bare percent": {
			"\n- [a.ics] A\n%\n- [b.ics] B\n",
			"\n- [a.ics] A\n- [b.ics] B\n",
		},
		"percent then tab": {
			"\n- [a.ics] A\n%\tnote\n- [b.ics] B\n",
			"\n- [a.ics] A\n- [b.ics] B\n",
		},
		"indented": {
			"\n- [a.ics] A\n   % note\n",
			"\n- [a.ics] A\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			assertSameAsUncommented(t, tc.commented, tc.plain)
		})
	}
}

// TestParseComment_EndsDescriptionSpan pins that a comment closes the open
// description span rather than continuing it: the description stays `Grew`,
// and a plain line after the comment is no longer a continuation.
func TestParseComment_EndsDescriptionSpan(t *testing.T) {
	doc := parseWrapBody(t, "\n- [a.ics] Grew\n% 5 percent\n")
	if len(doc.Ungrouped) != 1 || doc.Ungrouped[0].Desc != "Grew" {
		t.Errorf("lines = %+v, want one box with description %q", doc.Ungrouped, "Grew")
	}
	assertBodyRejected(t, "\n- [a.ics] Grew\n% note\nmore text\n",
		"body line 3", `unrecognized "more text"`)
}

// TestParseComment_EscapeStillContinues pins that `\%` remains the escape that
// continues a description with a literal percent sign.
func TestParseComment_EscapeStillContinues(t *testing.T) {
	assertSameDocument(t,
		"\n- [a.ics] Grew\n\\% 5 percent\n",
		"\n- [a.ics] Grew % 5 percent\n",
	)
}

// TestParseComment_RecordsPhysicalLines pins that the parser keeps the
// physical body line numbers of the comments it dropped (the document records
// them for fmt-organize to refuse on; render and the base never see them).
func TestParseComment_RecordsPhysicalLines(t *testing.T) {
	doc := parseWrapBody(t, "\n% one\n- [a.ics\n  work] A\n% two\n")
	if want := []int{1, 4}; !reflect.DeepEqual(doc.CommentLines, want) {
		t.Errorf("CommentLines = %v, want %v", doc.CommentLines, want)
	}
	if len(parseWrapBody(t, "\n- [a.ics] A\n").CommentLines) != 0 {
		t.Errorf("a comment-free body must record no comment lines")
	}
}

// TestParseComment_StillRejected pins the percent forms that are NOT comments.
func TestParseComment_StillRejected(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want []string
	}{
		"percent glued to text":  {"\n%foo\n", []string{"body line 1", `unrecognized "%foo"`}},
		"directive form":         {"\n%:dry-run = true\n", []string{"body line 1", `unrecognized "%:dry-run = true"`}},
		"percent then punct":     {"\n%- x\n", []string{"body line 1", "unrecognized"}},
		"inside an open box":     {"\n- [a.ics\n% note\n  work] A\n", []string{"body line 1"}},
		"directive under a box":  {"\n- [a.ics] A\n%:allow-deletion\n", []string{"body line 2", "unrecognized"}},
		"percent glued, wrapped": {"\n- [a.ics] A\n%x\n", []string{"body line 2", "unrecognized"}},
	} {
		t.Run(name, func(t *testing.T) {
			assertBodyRejected(t, tc.body, tc.want...)
		})
	}
}

// TestRefuseBodyComments pins fmt-organize's refusal: regeneration cannot
// carry a body comment forward, so a document holding any is refused naming
// the count and the physical body lines, and a clean document passes.
func TestRefuseBodyComments(t *testing.T) {
	doc := parseWrapBody(t, "\n% one\n- [a.ics] A\n% two\n")
	err := refuseBodyComments("triage.txt", doc)
	if err == nil {
		t.Fatalf("want a refusal for a document with body comments")
	}
	for _, frag := range []string{
		"triage.txt", "2 body `%` comment lines", "body lines 1, 3", "cannot carry",
	} {
		if !strings.Contains(err.Error(), frag) {
			t.Errorf("refusal %q lacks %q", err, frag)
		}
	}
	one := refuseBodyComments("t.txt", parseWrapBody(t, "\n- [a.ics] A\n% only\n"))
	if one == nil || !strings.Contains(one.Error(), "1 body `%` comment line (body line 2)") {
		t.Errorf("singular refusal = %v", one)
	}
	if err := refuseBodyComments("t.txt", parseWrapBody(t, "\n- [a.ics] A\n")); err != nil {
		t.Errorf("comment-free document refused: %v", err)
	}
}
