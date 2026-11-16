package fastmail

import (
	"net/url"
	"reflect"
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

func TestPatchTarget_ThreadOnly(t *testing.T) {
	for _, tc := range []struct {
		uri  string
		want bool
	}{
		{"fastmail://personal/Inbox/?thread=T1", true},
		{"fastmail://personal/area/finance/?thread=T5", true},
		{"fastmail://personal/Inbox/", false},
		{"fastmail://personal/", false},
		{"fastmail://personal/Inbox/?thread=T1&email=E1", false},
		{"fastmail://personal/Inbox/?thread=T1&email=E1&raw=1", false},
		{"caldav://personal/Inbox/?thread=T1", false},
	} {
		u, err := url.Parse(tc.uri)
		if err != nil {
			t.Fatal(err)
		}
		tag, ok := Plugin{}.PatchTarget(u)
		if ok != tc.want || (ok && tag != typeThread) {
			t.Errorf("PatchTarget(%q) = %q, %v; want ok=%v", tc.uri, tag, ok, tc.want)
		}
	}
}

// The declared keys are exactly the recognized set, so the host's strip leaves
// the plugin's behaviour unchanged.
func TestDescribePatchKeys_MatchesTheRecognizedSet(t *testing.T) {
	declared := Plugin{}.DescribePatchKeys()
	if len(declared) != 1 || declared[0].Tag != typeThread ||
		!reflect.DeepEqual(declared[0].Keys, threadPatchFields) {
		t.Fatalf("declared = %+v", declared)
	}

	u, _ := url.Parse("fastmail://personal/Inbox/?thread=T1")
	got, ok := cutting_garden_plugins.ResolvePatchKeys(Plugin{}, u)
	if !ok || got.Tag != typeThread || !reflect.DeepEqual(got.Keys, threadPatchFields) {
		t.Errorf("ResolvePatchKeys = %+v %v", got, ok)
	}
}
