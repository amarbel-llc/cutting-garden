package org

import (
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/config_common"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// A tag-vocabulary file encodes tag names in its headline hierarchy (#378): the
// level-1 headline is the family and each child's title continues the name.
func TestHyphenName(t *testing.T) {
	for _, tc := range []struct {
		name string
		path []string
		want string
	}{
		{"family alone", []string{"priority"}, "priority"},
		{"member", []string{"priority", "-1_should"}, "priority-1_should"},
		{"member with hyphens inside", []string{"trips", "-26-01-a"}, "trips-26-01-a"},
		{"deep continuation", []string{"proj", "-gamma", "-two", "-deep"}, "proj-gamma-two-deep"},
		{"a hyphenated level-1 name is kept whole", []string{"zz-inbox"}, "zz-inbox"},
		{"a bare child starts a new name", []string{"a", "-x", "b", "-y"}, "b-y"},
		{"a bare level-2 title is its own name", []string{"a", "b"}, "b"},
		{"every title continues: join all", []string{"-a", "-b"}, "-a-b"},
		{"no path, no name", nil, ""},
	} {
		if got := hyphenName(tc.path); got != tc.want {
			t.Errorf("%s: hyphenName(%q) = %q, want %q", tc.name, tc.path, got, tc.want)
		}
	}
}

func TestValidateAccounts_TagFiles(t *testing.T) {
	base := config_common.Account{
		Root: config_common.Root{Name: "t", URL: "http://127.0.0.1:1/"},
	}
	with := func(files ...string) []config_common.Account {
		a := base
		a.TagFiles = files
		return []config_common.Account{a}
	}

	got, err := validateAccounts(with("_tags.org", "vocab/tags.org"))
	if err != nil {
		t.Fatalf("valid tag_files: %v", err)
	}
	if !got[0].isTagFile("_tags.org") || !got[0].isTagFile("vocab/tags.org") {
		t.Errorf("both files must be tag files: %+v", got[0].tagFiles)
	}
	if got[0].isTagFile("_inbox.org") {
		t.Error("an unlisted file is not a tag file")
	}

	for name, files := range map[string][]string{
		"empty entry":     {""},
		"absolute path":   {"/_tags.org"},
		"parent segment":  {"../tags.org"},
		"not an org file": {"tags.txt"},
		"a folder":        {"vocab/"},
	} {
		if _, err := validateAccounts(with(files...)); err == nil {
			t.Errorf("%s (%q) must be rejected", name, files)
		}
	}
}

// The headline type declares exactly one name field, valid under the SDK's
// declaration check.
func TestDescribeListingFields_DeclaresTheNameField(t *testing.T) {
	sets := Plugin{}.DescribeListingFields()
	if err := cutting_garden_plugins.ValidateListingFieldSets(sets); err != nil {
		t.Fatalf("listing declaration: %v", err)
	}
	if key, ok := cutting_garden_plugins.ListingNameKey(sets, typeHeadline); !ok || key != fieldName {
		t.Errorf("headline name field = (%q, %v), want (%q, true)", key, ok, fieldName)
	}
	if _, ok := cutting_garden_plugins.ListingNameKey(sets, typeFile); ok {
		t.Error("a file has no hyphen name")
	}
}
