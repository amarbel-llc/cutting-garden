package organize

import (
	"reflect"
	"strings"
	"testing"
)

// The wrapped-box lane (cutting-garden#261, RFC 0015 §Object lines): a box may
// span several physical lines. While its `[` group is unbalanced every
// following line continues the INTERIOR; after the closing `]` the
// DESCRIPTION span continues on any line that starts with neither `-` nor `#`,
// and on a `\-` / `\#` escaped line (backslash stripped). A bare `-` always
// opens a new box; a blank line or heading ends the span. Every vector pins a
// wrapped body against its unwrapped twin, so a wrapped document applies
// exactly like the single-line one.

// parseWrapBody parses tagEnvelope + body.
func parseWrapBody(t *testing.T, body string) document {
	t.Helper()
	doc, err := parseDocument(tagEnvelope + body)
	if err != nil {
		t.Fatalf("parse %q: %v", body, err)
	}
	return doc
}

// assertSameDocument pins that the wrapped body parses to exactly the lines
// (and sections) its single-line twin does.
func assertSameDocument(t *testing.T, wrapped, unwrapped string) {
	t.Helper()
	got, want := parseWrapBody(t, wrapped), parseWrapBody(t, unwrapped)
	if !reflect.DeepEqual(got.Ungrouped, want.Ungrouped) {
		t.Errorf("ungrouped lines differ:\n got %+v\nwant %+v", got.Ungrouped, want.Ungrouped)
	}
	if !reflect.DeepEqual(got.Sections, want.Sections) {
		t.Errorf("sections differ:\n got %+v\nwant %+v", got.Sections, want.Sections)
	}
}

// assertBodyRejected pins that body is a bad request whose message carries
// every fragment in want.
func assertBodyRejected(t *testing.T, body string, want ...string) {
	t.Helper()
	_, err := parseDocument(tagEnvelope + body)
	if err == nil {
		t.Fatalf("parse %q: want an error", body)
	}
	for _, frag := range want {
		if !strings.Contains(err.Error(), frag) {
			t.Errorf("parse %q: error %q lacks %q", body, err, frag)
		}
	}
}

func TestParseWrap_InteriorTwoLines(t *testing.T) {
	assertSameDocument(t,
		"\n- [a.ics from=someone\n  trips-26-09] Final meet-up\n- [b.ics] B\n",
		"\n- [a.ics from=someone trips-26-09] Final meet-up\n- [b.ics] B\n",
	)
}

func TestParseWrap_InteriorThreeLines(t *testing.T) {
	assertSameDocument(t,
		"\n# work\n\n- [a.ics\n    work\n    x=\"quoted value\"] A\n",
		"\n# work\n\n- [a.ics work x=\"quoted value\"] A\n",
	)
}

// TestParseWrap_InteriorWrapInsideQuote pins that a wrap inside a quoted
// value joins with ONE space, exactly as a description wrap does.
func TestParseWrap_InteriorWrapInsideQuote(t *testing.T) {
	assertSameDocument(t,
		"\n- [a.ics x=\"quoted\n     value\"] A\n",
		"\n- [a.ics x=\"quoted value\"] A\n",
	)
}

func TestParseWrap_DescriptionPlainContinuation(t *testing.T) {
	assertSameDocument(t,
		"\n- [a.ics] Final Zoom\n  meet-up is tonight\n- [b.ics] B\n",
		"\n- [a.ics] Final Zoom meet-up is tonight\n- [b.ics] B\n",
	)
}

func TestParseWrap_DescriptionAfterInteriorWrap(t *testing.T) {
	assertSameDocument(t,
		"\n- [a.ics\n  work] Final\n  \\- meet-up\n",
		"\n- [a.ics work] Final - meet-up\n",
	)
}

func TestParseWrap_DescriptionDashEscape(t *testing.T) {
	assertSameDocument(t,
		"\n- [a.ics] Budget\n  \\-5 dollars\n",
		"\n- [a.ics] Budget -5 dollars\n",
	)
}

func TestParseWrap_DescriptionHashEscape(t *testing.T) {
	assertSameDocument(t,
		"\n# work\n\n- [a.ics] Ticket\n\\#42 follow-up\n",
		"\n# work\n\n- [a.ics] Ticket #42 follow-up\n",
	)
}

// TestParseWrap_EmptyTrailerThenContinuation pins that a box with no inline
// description takes its continuation as the whole description (no leading
// space from the join).
func TestParseWrap_EmptyTrailerThenContinuation(t *testing.T) {
	assertSameDocument(t,
		"\n- [a.ics]\n  Final meet-up\n",
		"\n- [a.ics] Final meet-up\n",
	)
}

// TestParseWrap_BareDashIsNewBox pins that a line-leading `-` in the
// description span is ALWAYS a new box — never a continuation.
func TestParseWrap_BareDashIsNewBox(t *testing.T) {
	doc := parseWrapBody(t, "\n- [a.ics] First\n- [b.ics] Second\n")
	if len(doc.Ungrouped) != 2 || doc.Ungrouped[0].Desc != "First" || doc.Ungrouped[1].ID != "b.ics" {
		t.Errorf("lines = %+v, want two boxes a.ics/First and b.ics/Second", doc.Ungrouped)
	}
	// A bare `-` that is not a box is a loud box error, not a continuation.
	assertBodyRejected(t, "\n- [a.ics] First\n- 5 dollars\n", "body line 2")
	assertBodyRejected(t, "\n- [a.ics] First\n-\n", "body line 2")
}

// TestParseWrap_BlankLineEndsDescription pins that a blank line closes the
// span: a following plain line is unrecognized, as it was before wrapping.
func TestParseWrap_BlankLineEndsDescription(t *testing.T) {
	assertBodyRejected(t, "\n- [a.ics] First\n\nstray text\n", "body line 3", `unrecognized "stray text"`)
}

func TestParseWrap_EscapeOutsideSpanRejected(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		line string
	}{
		"document start": {"\n\\- orphan\n- [a.ics] A\n", "body line 1"},
		"after heading":  {"\n# work\n\\# orphan\n", "body line 2"},
		"after blank":    {"\n- [a.ics] A\n\n\\- orphan\n", "body line 3"},
	} {
		t.Run(name, func(t *testing.T) {
			assertBodyRejected(t, tc.body, tc.line, "no box description is open")
		})
	}
}

func TestParseWrap_UnbalancedInteriorRejected(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want []string
	}{
		"reaches blank line": {
			"\n- [a.ics\n  work\n\n- [b.ics] B\n",
			[]string{"body line 1", "unterminated box", "blank line at body line 3"},
		},
		"reaches heading": {
			"\n- [a.ics\n# work\n",
			[]string{"body line 1", "unterminated box", "heading at body line 2"},
		},
		"reaches end of document": {
			"\n- [a.ics\n  work",
			[]string{"body line 1", "unterminated box", "end of document"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			assertBodyRejected(t, tc.body, tc.want...)
		})
	}
}

// TestParseWrap_ErrorsNamePhysicalLine pins that line numbers stay physical
// after a wrapped box consumed several lines.
func TestParseWrap_ErrorsNamePhysicalLine(t *testing.T) {
	assertBodyRejected(t, "\n- [a.ics\n  work] A\n  more\n\n- [b.ics x*=y] B\n", "body line 5")
}
