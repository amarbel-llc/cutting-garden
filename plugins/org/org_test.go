package org

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"code.linenisgreat.com/cutting-garden/pkgs/config_common"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/org/orgtestserver"
)

const inboxOrg = `#+FILETAGS: :zz-inbox:
* TODO Call the plumber                        :project-house:
  DEADLINE: <2000-01-01 Sat>
  Ask about the valve.
* NEXT Draft the doc                           :project-garden:
  :PROPERTIES:
  :ID:       abc-123
  :END:
** Ask about appends
* Dup
* Dup
* DONE Renew passport                          :zz-agent_quoted:
** Child of quoted
`

func setup(t *testing.T) *orgtestserver.Server {
	t.Helper()
	srv := orgtestserver.Start()
	t.Cleanup(srv.Close)
	srv.Put("_inbox.org", []byte(inboxOrg))
	srv.Put("projects/garden.org", []byte("#+TODO: TODO WAIT | DONE DROPPED\n* WAIT Order seeds :project-garden:\n"))
	srv.Put("notes.txt", []byte("not org\n"))
	srv.Put("plain.org", []byte("Shopping list\n  milk\n"))

	t.Setenv("CG_TEST_ORG_PASSWORD", orgtestserver.Password)
	accounts, err := validateAccounts([]config_common.Account{{
		Root:           config_common.Root{Name: "test", URL: srv.URL()},
		Username:       orgtestserver.Username,
		PasswordSource: "env",
		PasswordKey:    "CG_TEST_ORG_PASSWORD",
		TodoKeywords:   "TODO NEXT | DONE CANCELLED",
	}})
	if err != nil {
		t.Fatal(err)
	}
	setConfiguredAccounts(accounts)
	t.Cleanup(func() { setConfiguredAccounts(nil) })
	savedDelay := readBackDelay
	readBackDelay = 0
	t.Cleanup(func() { readBackDelay = savedDelay })
	return srv
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func uris(nodes []cutting_garden_plugins.Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.URIString()
	}
	return out
}

func TestListRoots_Folder(t *testing.T) {
	setup(t)
	nodes, err := Plugin{}.ListRoots(context.Background(), mustURL(t, "org://test/"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"org://test/_inbox.org", "org://test/plain.org", "org://test/projects/"}
	if diff := cmp.Diff(want, uris(nodes)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}

func TestListRoots_FileAndHeadline(t *testing.T) {
	setup(t)
	ctx := context.Background()
	nodes, err := Plugin{}.ListRoots(ctx, mustURL(t, "org://test/_inbox.org"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"org://test/_inbox.org?h=Call+the+plumber",
		"org://test/_inbox.org?id=abc-123",
		"org://test/_inbox.org?h=Dup",
		"org://test/_inbox.org?h=Dup&n=2",
		"org://test/_inbox.org?h=Renew+passport",
	}
	if diff := cmp.Diff(want, uris(nodes)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}

	plumber := nodes[0]
	if plumber.Fields[fieldState] != "TODO" || plumber.Fields[fieldDeadline] != "2000-01-01" {
		t.Errorf("fields = %v", plumber.Fields)
	}
	if diff := cmp.Diff([]string{"project-house", "zz-inbox"}, plumber.Fields[fieldTags]); diff != "" {
		t.Errorf("tags (-want +got):\n%s", diff)
	}
	if got := plumber.Facets[fieldDeadlineBand][0].Key; got != bandOverdue {
		t.Errorf("deadline_band = %s", got)
	}

	children, err := Plugin{}.ListRoots(ctx, mustURL(t, "org://test/_inbox.org?id=abc-123"))
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"org://test/_inbox.org?h=Draft+the+doc%2FAsk+about+appends"}, uris(children)); diff != "" {
		t.Errorf("children (-want +got):\n%s", diff)
	}
}

func TestQuotedTagNotInherited(t *testing.T) {
	setup(t)
	nodes, err := Plugin{}.ListRoots(context.Background(), mustURL(t, "org://test/_inbox.org?h=Renew+passport"))
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"zz-inbox"}, nodes[0].Fields[fieldTags]); diff != "" {
		t.Errorf("child tags (-want +got):\n%s", diff)
	}
}

func TestReadLeaf_Headline(t *testing.T) {
	setup(t)
	content, ok, err := Plugin{}.ReadLeaf(context.Background(), mustURL(t, "org://test/_inbox.org?h=Call+the+plumber"))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	hc := content.Structured.(headlineContent)
	if hc.Body != "Ask about the valve." {
		t.Errorf("body = %q", hc.Body)
	}
	if want := "* TODO Call the plumber                        :project-house:\n  DEADLINE: <2000-01-01 Sat>\n  Ask about the valve.\n"; string(content.Raw) != want {
		t.Errorf("raw = %q", content.Raw)
	}
}

// A walk that lists and reads every headline of a file confirms its ETag
// once, not once per headline; after the freshness window it asks again,
// and an Orgzly upload is then picked up.
func TestFreshnessWindowSkipsRepeatPropfind(t *testing.T) {
	srv := setup(t)
	ctx := context.Background()
	read := func(uri string) {
		t.Helper()
		if _, _, err := (Plugin{}).ReadLeaf(ctx, mustURL(t, uri)); err != nil {
			t.Fatal(err)
		}
	}

	read("org://test/_inbox.org?h=Call+the+plumber")
	if _, err := (Plugin{}).ListRoots(ctx, mustURL(t, "org://test/_inbox.org?id=abc-123")); err != nil {
		t.Fatal(err)
	}
	read("org://test/_inbox.org?h=Dup&n=2")
	if got := srv.Requests("PROPFIND"); got != 1 {
		t.Errorf("PROPFIND within the window = %d, want 1", got)
	}
	if got := srv.Requests("GET"); got != 1 {
		t.Errorf("GET = %d, want 1", got)
	}

	saved := freshnessWindow
	freshnessWindow = 0
	t.Cleanup(func() { freshnessWindow = saved })
	srv.Put("_inbox.org", []byte("* Renamed by the phone\n"))
	if _, _, err := (Plugin{}).ReadLeaf(ctx, mustURL(t, "org://test/_inbox.org?h=Renamed+by+the+phone")); err != nil {
		t.Errorf("an expired window must re-check the ETag and see the upload: %v", err)
	}
	if got := srv.Requests("PROPFIND"); got != 2 {
		t.Errorf("PROPFIND after the window = %d, want 2", got)
	}
}

func TestReadLeaf_UnknownHeadlineIsBadRequest(t *testing.T) {
	setup(t)
	_, _, err := Plugin{}.ReadLeaf(context.Background(), mustURL(t, "org://test/_inbox.org?h=Renamed"))
	if err == nil {
		t.Fatal("want an error for a stale reference")
	}
}

func TestSynthesizedHeadlineForPlainFile(t *testing.T) {
	setup(t)
	nodes, err := Plugin{}.ListRoots(context.Background(), mustURL(t, "org://test/plain.org"))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Name != "Shopping list" {
		t.Errorf("got %v", uris(nodes))
	}
}

func TestFacetCounts_FolderWithFilter(t *testing.T) {
	setup(t)
	filter, err := cutting_garden_plugins.ParseFacetFilter("state=TODO")
	if err != nil {
		t.Fatal(err)
	}
	res, ok, err := Plugin{}.FacetCounts(context.Background(), mustURL(t, "org://test/"), filter)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got := res.Summary[fieldState]["TODO"]; got != 1 {
		t.Errorf("state TODO = %d, summary %v", got, res.Summary)
	}
	if got := res.Summary[fieldDeadlineBand][bandToday]; got != 0 {
		t.Errorf("today band = %d", got)
	}
	if _, present := res.Summary[fieldDeadlineBand][bandLater]; !present {
		t.Error("volatile band must carry informative zeros")
	}
	if len(res.ByContainer) != 1 || res.ByContainer[0].URI != "org://test/_inbox.org" {
		t.Errorf("byContainer = %+v", res.ByContainer)
	}

	all, _, err := Plugin{}.FacetCounts(context.Background(), mustURL(t, "org://test/"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := all.Summary[fieldState]["WAIT"]; got != 1 {
		t.Errorf("subfolder file's #+TODO keyword WAIT = %d", got)
	}
}

func TestRelativeNodeID(t *testing.T) {
	cases := []struct{ node, anchor, want string }{
		{"org://test/_inbox.org?id=abc", "org://test/", "_inbox.org?id=abc"},
		{"org://test/p/a.org?h=X", "org://test/p/", "a.org?h=X"},
		{"org://test/_inbox.org?h=X", "org://test/_inbox.org", "h=X"},
	}
	for _, c := range cases {
		got, ok := Plugin{}.RelativeNodeID(c.node, c.anchor)
		if !ok || got != c.want {
			t.Errorf("RelativeNodeID(%q, %q) = %q, %v", c.node, c.anchor, got, ok)
		}
	}
	if _, ok := (Plugin{}).RelativeNodeID("org://test/_inbox.org", "org://test/"); ok {
		t.Error("files defer to the default id")
	}
}

func TestClassifyURI_Errors(t *testing.T) {
	for _, raw := range []string{
		"org:///x.org", "org://test/notes.txt", "org://test/a.org?id=x&h=y", "org://test/?h=x",
		"org://test/../../secret.org", "org://test/a/./b.org",
	} {
		if _, err := classifyURI(mustURL(t, raw)); err == nil {
			t.Errorf("%q: want error", raw)
		}
	}
}

func TestDateBand(t *testing.T) {
	now := time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)
	cases := map[string]string{
		"2026-10-13": bandOverdue, "2026-10-14": bandToday,
		"2026-10-20": bandThisWeek, "2026-10-21": bandLater, "": valueNone,
	}
	for day, want := range cases {
		if got := dateBand(day, false, now); got != want {
			t.Errorf("dateBand(%q) = %s, want %s", day, got, want)
		}
	}
	if got := dateBand("2026-10-13", true, now); got != valueNone {
		t.Errorf("done headline band = %s", got)
	}

	// Across a DST change the day count must not shift.
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	springForward := time.Date(2026, 3, 8, 10, 0, 0, 0, ny)
	for day, want := range map[string]string{
		"2026-03-09": bandThisWeek, "2026-03-07": bandOverdue, "2026-03-15": bandLater,
	} {
		if got := dateBand(day, false, springForward); got != want {
			t.Errorf("DST: dateBand(%q) = %s, want %s", day, got, want)
		}
	}
}

func TestDuplicateIDStaysReachable(t *testing.T) {
	srv := setup(t)
	srv.Put("dup.org", []byte("* A\n:PROPERTIES:\n:ID: same\n:END:\n* B\n:PROPERTIES:\n:ID: same\n:END:\n"))
	nodes, err := Plugin{}.ListRoots(context.Background(), mustURL(t, "org://test/dup.org"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"org://test/dup.org?id=same", "org://test/dup.org?h=B"}
	if diff := cmp.Diff(want, uris(nodes)); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}
