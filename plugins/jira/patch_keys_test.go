package jira

import (
	"net/url"
	"reflect"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

func TestPatchTarget_IssueOnly(t *testing.T) {
	for _, tc := range []struct {
		uri  string
		want bool
	}{
		{"jira://example.atlassian.net/PROJ/PROJ-1", true},
		{"jira:http://localhost:8080/PROJ/PROJ-1", true},
		{"jira://example.atlassian.net/PROJ", false},
		{"jira://example.atlassian.net", false},
		{"jira://example.atlassian.net/PROJ/PROJ-1/extra", false},
	} {
		u, err := url.Parse(tc.uri)
		if err != nil {
			t.Fatal(err)
		}
		tag, ok := Plugin{}.PatchTarget(u)
		if ok != tc.want || (ok && tag != typeIssue) {
			t.Errorf("PatchTarget(%q) = %q, %v; want ok=%v", tc.uri, tag, ok, tc.want)
		}
	}
}

// The declared keys are exactly the ones PatchNode recognizes, so the host's
// strip leaves the plugin's behaviour unchanged.
func TestDescribePatchKeys_MatchesTheRecognizedSet(t *testing.T) {
	declared := Plugin{}.DescribePatchKeys()
	if len(declared) != 1 || declared[0].Tag != typeIssue ||
		!reflect.DeepEqual(declared[0].Keys, issuePatchFields) {
		t.Fatalf("declared = %+v", declared)
	}

	u, _ := url.Parse("jira://example.atlassian.net/PROJ/PROJ-1")
	got, ok := cutting_garden_plugins.ResolvePatchKeys(Plugin{}, u)
	if !ok || got.Tag != typeIssue || !reflect.DeepEqual(got.Keys, issuePatchFields) {
		t.Errorf("ResolvePatchKeys = %+v %v", got, ok)
	}
}
