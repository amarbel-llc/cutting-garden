package caldav

import (
	"net/url"
	"reflect"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

func TestPatchTarget_ObjectURIsOnly(t *testing.T) {
	for _, tc := range []struct {
		uri  string
		want bool
	}{
		{"caldav://acct/personal/abc.ics", true},
		{"caldav:https://h.example/dav/u/cal/abc.ics", true},
		{"caldav://acct/personal/", false},
		{"caldav://acct/", false},
		{"caldav://acct/personal/abc.ics?recurrence-id=20260101T000000Z", false},
	} {
		u, err := url.Parse(tc.uri)
		if err != nil {
			t.Fatal(err)
		}
		tag, ok := Plugin{}.PatchTarget(u)
		if ok != tc.want || (ok && tag != typePatchObject) {
			t.Errorf("PatchTarget(%q) = %q, %v; want ok=%v", tc.uri, tag, ok, tc.want)
		}
	}
}

// The declared section keys are exactly the component field sets PatchNode
// recognizes, under the section names its wrapped body uses.
func TestDescribePatchKeys_SectionsMatchTheComponentFieldSets(t *testing.T) {
	declared := Plugin{}.DescribePatchKeys()
	if len(declared) != 1 || declared[0].Tag != typePatchObject ||
		declared[0].Discriminator != "component" {
		t.Fatalf("declared = %+v", declared)
	}
	want := map[string]struct {
		section string
		keys    []string
	}{
		"VEVENT":   {"event", eventPatchFields},
		"VTODO":    {"task", taskPatchFields},
		"VJOURNAL": {"journal", journalPatchFields},
	}
	for _, s := range declared[0].Sections {
		w, ok := want[s.Value]
		if !ok || s.Section != w.section || !reflect.DeepEqual(s.Keys, w.keys) {
			t.Errorf("section %+v, want %+v", s, w)
		}
		delete(want, s.Value)
	}
	if len(want) != 0 {
		t.Errorf("components not declared: %v", want)
	}

	u, _ := url.Parse("caldav://acct/personal/abc.ics")
	got, ok := cutting_garden_plugins.ResolvePatchKeys(Plugin{}, u)
	if !ok || got.Tag != typePatchObject || len(got.Sections) != 3 {
		t.Errorf("ResolvePatchKeys = %+v %v", got, ok)
	}
}
