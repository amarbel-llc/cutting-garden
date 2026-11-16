package org

import (
	"net/url"
	"reflect"
	"slices"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

func TestPatchTarget_HeadlineAndFileOnly(t *testing.T) {
	for _, tc := range []struct {
		uri  string
		want string
	}{
		{"org://acct/inbox.org?id=ABC", typeHeadline},
		{"org://acct/inbox.org?h=Projects/Alpha", typeHeadline},
		{"org://acct/inbox.org?h=Projects/Alpha&n=2", typeHeadline},
		{"org://acct/inbox.org", typeFile},
		{"org://acct/sub/", ""},
		{"org://acct/", ""},
		{"org://acct/notes.txt", ""},
		{"fastmail://acct/inbox.org", ""},
	} {
		u, err := url.Parse(tc.uri)
		if err != nil {
			t.Fatal(err)
		}
		tag, ok := Plugin{}.PatchTarget(u)
		if tag != tc.want || ok != (tc.want != "") {
			t.Errorf("PatchTarget(%q) = %q, %v; want %q", tc.uri, tag, ok, tc.want)
		}
	}
}

// Every key parseEdit reads is declared, and `tags` (the listing's name for the
// own_tags key) is not: a direct {"tags": ...} patch is dropped and reported.
func TestDescribePatchKeys_AreTheKeysParseEditReads(t *testing.T) {
	declared := Plugin{}.DescribePatchKeys()
	if len(declared) != 2 {
		t.Fatalf("declared = %+v, want headline and file", declared)
	}
	for _, d := range declared {
		if !reflect.DeepEqual(d.Keys, patchKeys) {
			t.Errorf("%s keys = %v, want %v", d.Tag, d.Keys, patchKeys)
		}
		if slices.Contains(d.Keys, "tags") {
			t.Errorf("%s declares tags, which PatchNode does not read", d.Tag)
		}
		if !slices.Contains(d.Keys, fieldPatchOwnTags) {
			t.Errorf("%s does not declare %s", d.Tag, fieldPatchOwnTags)
		}
	}

	u, _ := url.Parse("org://acct/inbox.org?id=ABC")
	tag, keys, ok := cutting_garden_plugins.ResolvePatchKeys(Plugin{}, u)
	if !ok || tag != typeHeadline || !reflect.DeepEqual(keys, patchKeys) {
		t.Errorf("ResolvePatchKeys = %q %v %v", tag, keys, ok)
	}
}
