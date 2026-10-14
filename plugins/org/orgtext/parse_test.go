package orgtext

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const inbox = `#+FILETAGS: :zz-inbox:
* TODO Call the plumber about the leak              :project-reno-phase_1:
  DEADLINE: <2026-10-10 Fri>
  Ask about the shutoff valve too.
* NEXT [#A] Draft FDR 0026 UX doc                   :project-circus:req-internet:
  SCHEDULED: <2026-10-14 Tue>
  :PROPERTIES:
  :ID:       3f9c1e2a-7b44-4c1e-9a0d-2f1f6c0b8e11
  :CREATED:  [2026-10-14 Tue 09:00]
  :END:
** Ask the circus session about pebble appends
* Idea: org plugin should expose CLOSED dates
* DONE Renew passport                               :area-travel:
  CLOSED: [2026-10-02 Thu 09:14]
  :LOGBOOK:
  - State "DONE" from "TODO" [2026-10-02 Thu 09:14]
  :END:
  Picked up at the counter.
`

func orgzlyKeywords(t *testing.T) Keywords {
	t.Helper()
	kw, err := ParseKeywords("TODO NEXT | DONE CANCELLED")
	if err != nil {
		t.Fatal(err)
	}
	return kw
}

type headlineView struct {
	Level       int
	Keyword     string
	Priority    string
	Title       string
	OwnTags     []string
	Tags        []string
	Planning    Planning
	Properties  []Property
	Body        string
	OutlinePath []string
}

func view(hs []*Headline) []headlineView {
	var out []headlineView
	var walk func([]*Headline)
	walk = func(hs []*Headline) {
		for _, h := range hs {
			out = append(out, headlineView{
				h.Level, h.Keyword, h.Priority, h.Title, h.OwnTags, h.Tags,
				h.Planning, h.Properties, h.Body, h.OutlinePath,
			})
			walk(h.Children)
		}
	}
	walk(hs)
	return out
}

func TestParse_Inbox(t *testing.T) {
	f := Parse([]byte(inbox), Options{Keywords: orgzlyKeywords(t)})
	want := []headlineView{
		{
			Level: 1, Keyword: "TODO", Title: "Call the plumber about the leak",
			OwnTags:     []string{"project-reno-phase_1"},
			Tags:        []string{"project-reno-phase_1", "zz-inbox"},
			Planning:    Planning{Deadline: "2026-10-10"},
			Body:        "Ask about the shutoff valve too.",
			OutlinePath: []string{"Call the plumber about the leak"},
		},
		{
			Level: 1, Keyword: "NEXT", Priority: "A", Title: "Draft FDR 0026 UX doc",
			OwnTags:  []string{"project-circus", "req-internet"},
			Tags:     []string{"project-circus", "req-internet", "zz-inbox"},
			Planning: Planning{Scheduled: "2026-10-14"},
			Properties: []Property{
				{Key: "ID", Value: "3f9c1e2a-7b44-4c1e-9a0d-2f1f6c0b8e11"},
				{Key: "CREATED", Value: "[2026-10-14 Tue 09:00]"},
			},
			OutlinePath: []string{"Draft FDR 0026 UX doc"},
		},
		{
			Level: 2, Title: "Ask the circus session about pebble appends",
			Tags:        []string{"project-circus", "req-internet", "zz-inbox"},
			OutlinePath: []string{"Draft FDR 0026 UX doc", "Ask the circus session about pebble appends"},
		},
		{
			Level: 1, Title: "Idea: org plugin should expose CLOSED dates",
			Tags:        []string{"zz-inbox"},
			OutlinePath: []string{"Idea: org plugin should expose CLOSED dates"},
		},
		{
			Level: 1, Keyword: "DONE", Title: "Renew passport",
			OwnTags:     []string{"area-travel"},
			Tags:        []string{"area-travel", "zz-inbox"},
			Planning:    Planning{Closed: "2026-10-02"},
			Body:        "Picked up at the counter.",
			OutlinePath: []string{"Renew passport"},
		},
	}
	if diff := cmp.Diff(want, view(f.Headlines)); diff != "" {
		t.Errorf("headlines (-want +got):\n%s", diff)
	}
	if len(f.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", f.Warnings)
	}
	if got := f.Headlines[1].ID(); got != "3f9c1e2a-7b44-4c1e-9a0d-2f1f6c0b8e11" {
		t.Errorf("ID() = %q", got)
	}
}

func TestParse_Spans(t *testing.T) {
	f := Parse([]byte(inbox), Options{Keywords: orgzlyKeywords(t)})
	next := f.Headlines[1]
	cases := []struct {
		name string
		span Span
		want string
	}{
		{"line", next.LineSpan, "* NEXT [#A] Draft FDR 0026 UX doc                   :project-circus:req-internet:"},
		{"planning", next.PlanningSpan, "  SCHEDULED: <2026-10-14 Tue>\n"},
		{"drawer", next.DrawerSpan, "  :PROPERTIES:\n  :ID:       3f9c1e2a-7b44-4c1e-9a0d-2f1f6c0b8e11\n  :CREATED:  [2026-10-14 Tue 09:00]\n  :END:\n"},
		{"body", next.BodySpan, ""},
		{"subtree", next.SubtreeSpan, strings.Join([]string{
			"* NEXT [#A] Draft FDR 0026 UX doc                   :project-circus:req-internet:",
			"  SCHEDULED: <2026-10-14 Tue>",
			"  :PROPERTIES:",
			"  :ID:       3f9c1e2a-7b44-4c1e-9a0d-2f1f6c0b8e11",
			"  :CREATED:  [2026-10-14 Tue 09:00]",
			"  :END:",
			"** Ask the circus session about pebble appends",
			"",
		}, "\n")},
	}
	for _, c := range cases {
		if got := string(f.Source(c.span)); got != c.want {
			t.Errorf("%s span = %q, want %q", c.name, got, c.want)
		}
	}

	plumber := f.Headlines[0]
	if !plumber.DrawerSpan.Empty() || plumber.DrawerSpan.Start != plumber.PlanningSpan.End {
		t.Errorf("absent drawer should be an insertion point after planning, got %+v (planning %+v)",
			plumber.DrawerSpan, plumber.PlanningSpan)
	}
	idea := f.Headlines[2]
	if !idea.PlanningSpan.Empty() || idea.PlanningSpan.Start != idea.LineSpan.End+1 {
		t.Errorf("absent planning should be an insertion point after the headline line, got %+v", idea.PlanningSpan)
	}
}

func TestParse_FileKeywordsOverrideDefault(t *testing.T) {
	src := "#+TODO: WAIT | DROPPED\n* WAIT On hold\n* NEXT Not a keyword here\n* DROPPED Gone\n"
	f := Parse([]byte(src), Options{Keywords: orgzlyKeywords(t)})
	got := []string{}
	for _, h := range f.Headlines {
		got = append(got, h.Keyword+"|"+h.Title)
	}
	want := []string{"WAIT|On hold", "|NEXT Not a keyword here", "DROPPED|Gone"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	if !f.Keywords.IsDone("DROPPED") || f.Keywords.IsDone("WAIT") {
		t.Errorf("Keywords = %+v", f.Keywords)
	}
}

func TestParse_DefaultKeywordsWhenUnset(t *testing.T) {
	f := Parse([]byte("* TODO a\n* NEXT b\n"), Options{})
	if f.Headlines[0].Keyword != "TODO" || f.Headlines[1].Keyword != "" {
		t.Errorf("got %q, %q", f.Headlines[0].Keyword, f.Headlines[1].Keyword)
	}
}

func TestParse_NoInheritExemption(t *testing.T) {
	src := "#+FILETAGS: :zz-inbox:zz-agent_quoted:\n* Parent :zz-agent_quoted:project-x:\n** Child\n"
	f := Parse([]byte(src), Options{NoInherit: []string{"zz-agent_quoted"}})
	parent := f.Headlines[0]
	child := parent.Children[0]
	if diff := cmp.Diff([]string{"zz-agent_quoted", "project-x", "zz-inbox"}, parent.Tags); diff != "" {
		t.Errorf("parent tags (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"project-x", "zz-inbox"}, child.Tags); diff != "" {
		t.Errorf("child tags (-want +got):\n%s", diff)
	}
}

func TestParse_CRLF(t *testing.T) {
	src := "* TODO Crlf headline :a:\r\n  DEADLINE: <2026-01-02 Fri>\r\n  body line\r\n"
	f := Parse([]byte(src), Options{})
	h := f.Headlines[0]
	if h.Title != "Crlf headline" || h.Planning.Deadline != "2026-01-02" || h.Body != "body line" {
		t.Errorf("got %+v", view(f.Headlines))
	}
	if diff := cmp.Diff([]string{"a"}, h.OwnTags); diff != "" {
		t.Errorf("tags (-want +got):\n%s", diff)
	}
}

func TestParse_UnclosedDrawerWarnsAndKeepsBody(t *testing.T) {
	src := "* Note\n  :PROPERTIES:\n  :ID: x\n  text\n* Next\n"
	f := Parse([]byte(src), Options{})
	h := f.Headlines[0]
	if h.ID() != "" || !h.DrawerSpan.Empty() {
		t.Errorf("unclosed drawer must not count as properties: %+v", h.Properties)
	}
	if len(f.Warnings) != 1 || f.Warnings[0].Line != 2 {
		t.Errorf("warnings = %+v, want one on line 2", f.Warnings)
	}
	if h.Body != ":PROPERTIES:\n:ID: x\ntext" {
		t.Errorf("body = %q", h.Body)
	}
}

func TestParse_TitleEdgeCases(t *testing.T) {
	cases := []struct {
		line           string
		keyword, title string
		tags           []string
	}{
		{"* TODO", "TODO", "", nil},
		{"* Ratio 1:2 is fine", "", "Ratio 1:2 is fine", nil},
		{"* Time 10:30 :a:b:", "", "Time 10:30", []string{"a", "b"}},
		{"* TODOS are not a keyword", "", "TODOS are not a keyword", nil},
	}
	for _, c := range cases {
		f := Parse([]byte(c.line+"\n"), Options{})
		h := f.Headlines[0]
		if h.Keyword != c.keyword || h.Title != c.title || !cmp.Equal(h.OwnTags, c.tags) {
			t.Errorf("%q: got keyword=%q title=%q tags=%v", c.line, h.Keyword, h.Title, h.OwnTags)
		}
	}
}

func TestParse_KeywordThenTab(t *testing.T) {
	h := Parse([]byte("* TODO\tBuy milk\n"), Options{}).Headlines[0]
	if h.Keyword != "TODO" || h.Title != "Buy milk" {
		t.Errorf("got keyword=%q title=%q", h.Keyword, h.Title)
	}
}

func TestParse_PlanningKeywordWithoutTimestampStaysBody(t *testing.T) {
	h := Parse([]byte("* TODO x\nDEADLINE: talk to Bob first\n"), Options{}).Headlines[0]
	if h.Planning.Deadline != "" || !h.PlanningSpan.Empty() || h.Body != "DEADLINE: talk to Bob first" {
		t.Errorf("planning=%+v span=%+v body=%q", h.Planning, h.PlanningSpan, h.Body)
	}
}

func TestParse_Priorities(t *testing.T) {
	cases := map[string]struct{ priority, title string }{
		"* [#B] x":  {"B", "x"},
		"* [#10] x": {"10", "x"},
		"* [#a] x":  {"", "[#a] x"},
	}
	for src, want := range cases {
		h := Parse([]byte(src+"\n"), Options{}).Headlines[0]
		if h.Priority != want.priority || h.Title != want.title {
			t.Errorf("%q: priority=%q title=%q", src, h.Priority, h.Title)
		}
	}
}

func TestParse_BlocksHideHeadlinesAndSettings(t *testing.T) {
	src := "* Note\n#+BEGIN_EXAMPLE\n#+TODO: A | B\n* not a heading\n#+END_EXAMPLE\n* TODO Real\n"
	f := Parse([]byte(src), Options{})
	if len(f.Headlines) != 2 || f.Headlines[1].Keyword != "TODO" {
		t.Fatalf("got %+v", view(f.Headlines))
	}
	if want := "#+BEGIN_EXAMPLE\n#+TODO: A | B\n* not a heading\n#+END_EXAMPLE"; f.Headlines[0].Body != want {
		t.Errorf("body = %q", f.Headlines[0].Body)
	}
}

func TestSynthesizeHeadline_WarningsNotDuplicated(t *testing.T) {
	f := Parse([]byte("#+TITLE: x\n:LOGBOOK:\nfoo\n"), Options{})
	f.SynthesizeHeadline("x")
	f.SynthesizeHeadline("x")
	if len(f.Warnings) != 0 {
		t.Errorf("SynthesizeHeadline must not append warnings: %+v", f.Warnings)
	}
}

func TestParse_NotAHeadline(t *testing.T) {
	f := Parse([]byte("*bold* text\n*\n"), Options{})
	if len(f.Headlines) != 0 {
		t.Errorf("got %d headlines", len(f.Headlines))
	}
}

func TestSynthesizeHeadline(t *testing.T) {
	f := Parse([]byte("#+FILETAGS: :zz-inbox:\n\nShopping list\n  milk\n"), Options{})
	h := f.SynthesizeHeadline("shopping")
	if h == nil || !h.Synthetic || h.Title != "Shopping list" {
		t.Fatalf("got %+v", h)
	}
	if diff := cmp.Diff([]string{"zz-inbox"}, h.Tags); diff != "" {
		t.Errorf("tags (-want +got):\n%s", diff)
	}
	if h.Body != "Shopping list\n  milk" {
		t.Errorf("body = %q", h.Body)
	}
	if empty := Parse(nil, Options{}).SynthesizeHeadline("empty"); empty.Title != "empty" {
		t.Errorf("empty file title = %q, want stem", empty.Title)
	}
	if Parse([]byte("* a\n"), Options{}).SynthesizeHeadline("x") != nil {
		t.Error("a file with headlines must not synthesize one")
	}
}

func TestParseKeywords(t *testing.T) {
	cases := []struct {
		spec string
		want Keywords
	}{
		{"TODO NEXT | DONE CANCELLED", Keywords{Active: []string{"TODO", "NEXT"}, Done: []string{"DONE", "CANCELLED"}}},
		{"TODO(t) WAIT(w@/!) | DONE(d!)", Keywords{Active: []string{"TODO", "WAIT"}, Done: []string{"DONE"}}},
		{"TODO STARTED DONE", Keywords{Active: []string{"TODO", "STARTED"}, Done: []string{"DONE"}}},
	}
	for _, c := range cases {
		got, err := ParseKeywords(c.spec)
		if err != nil {
			t.Fatalf("%q: %v", c.spec, err)
		}
		if diff := cmp.Diff(c.want, got); diff != "" {
			t.Errorf("%q (-want +got):\n%s", c.spec, diff)
		}
	}
	if _, err := ParseKeywords(" | "); err == nil {
		t.Error("empty spec must error")
	}
}
