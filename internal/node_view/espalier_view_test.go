package node_view

import (
	"strings"
	"testing"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// queryIDLister is a RootLister that ALSO implements NodeIDer: it ids a node
// by its `id` query parameter when the node sits directly under the anchor,
// and declines (ok=false) every other node — the shape of a plugin whose node
// identity rides outside the URI path (fastmail's `?thread=`).
type queryIDLister struct {
	interpBase
}

func (queryIDLister) RelativeNodeID(nodeURI, anchor string) (string, bool) {
	rest, under := strings.CutPrefix(nodeURI, anchor)
	id, hasID := strings.CutPrefix(rest, "?id=")
	if !under || !hasID {
		return "", false
	}
	return id, true
}

// TestRelativeID pins the default's form-independent id shortening:
// same-spelling prefix, cross-spelling (caldav:https:// anchor vs caldav://
// node URI), and unrelated. (Moved from organize with Task 5b.)
func TestRelativeID(t *testing.T) {
	if got := RelativeID("caldav:http://h/dav/cal/task1.ics", "caldav:http://h/dav/cal/"); got != "task1.ics" {
		t.Errorf("RelativeID same-form = %q, want task1.ics", got)
	}
	if got := RelativeID(
		"caldav://caldav.fastmail.com/dav/cal/x.ics",
		"caldav:https://caldav.fastmail.com/dav/cal/",
	); got != "x.ics" {
		t.Errorf("RelativeID cross-form = %q, want x.ics", got)
	}
	if got := RelativeID("caldav://other/y.ics", "caldav://host/cal/"); got != "caldav://other/y.ics" {
		t.Errorf("RelativeID unrelated = %q, want full URI", got)
	}
}

func TestRelativeIDFor(t *testing.T) {
	const anchor = "fake://acct/box/"
	cases := []struct {
		name   string
		lister cgp.RootLister
		uri    string
		want   string
	}{
		{"NodeIDer ok", queryIDLister{}, "fake://acct/box/?id=T1", "T1"},
		{"NodeIDer declines → host+path default", queryIDLister{}, "fake://acct/box/sub/", "sub/"},
		{"no capability → host+path default", interpPlainLister{}, "fake://acct/box/x.ics", "x.ics"},
		{"no capability, query identity collapses", interpPlainLister{}, "fake://acct/box/?id=T1", ""},
		{"nil lister → default", nil, "fake://acct/box/x.ics", "x.ics"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RelativeIDFor(tc.lister, tc.uri, anchor); got != tc.want {
				t.Errorf("RelativeIDFor(%q, %q) = %q, want %q", tc.uri, anchor, got, tc.want)
			}
		})
	}
}

func TestBoxIDs_BindsListerAndAnchor(t *testing.T) {
	idOf := BoxIDs(queryIDLister{}, "fake://acct/box/")
	if got := idOf("fake://acct/box/?id=T2"); got != "T2" {
		t.Errorf("BoxIDs(...)(T2 uri) = %q, want T2", got)
	}
	if got := idOf("fake://other/y"); got != "fake://other/y" {
		t.Errorf("BoxIDs(...)(foreign uri) = %q, want the full URI", got)
	}
}
