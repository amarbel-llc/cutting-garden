package cutting_garden_plugins

import (
	"context"
	"net/url"
	"testing"
)

// typesOnlyLister is the minimal RootLister for exercising the
// NodeType resolution contract.
type typesOnlyLister struct{}

func (typesOnlyLister) Schemes() []string                     { return []string{"typestest"} }
func (typesOnlyLister) TypeTag() string                       { return "cutting_garden-test-v1" }
func (typesOnlyLister) ValidateSource(*url.URL, string) error { return nil }
func (typesOnlyLister) CaptureRoot(CaptureRootRequest) CaptureRootResult {
	return CaptureRootResult{}
}

func (typesOnlyLister) Types() []NodeType {
	return []NodeType{
		{Tag: "test-folder-v1", Container: true},
		{Tag: "test-ics-v1", Container: false, MimeType: "text/calendar"},
		{Tag: "test-blob-v1", Container: false},
	}
}

func (typesOnlyLister) ListRoots(context.Context, *url.URL) ([]Node, error) {
	return nil, nil
}

func TestBodyMimeType_LeafDefaultsAndContainerPassthrough(t *testing.T) {
	cases := []struct {
		name string
		nt   NodeType
		want string
	}{
		{
			"declared leaf keeps its mimetype",
			NodeType{Tag: "x", MimeType: "text/calendar"},
			"text/calendar",
		},
		{
			"undeclared leaf defaults to octet-stream",
			NodeType{Tag: "x"},
			MimeTypeDefault,
		},
		{
			"zero NodeType (unknown tag) defaults like a leaf",
			NodeType{},
			MimeTypeDefault,
		},
		{
			"container without mimetype stays empty (no body)",
			NodeType{Tag: "x", Container: true},
			"",
		},
	}
	for _, tc := range cases {
		if got := tc.nt.BodyMimeType(); got != tc.want {
			t.Errorf("%s: BodyMimeType() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestNodeTypeFor_ResolvesDeclaredAndRejectsUnknown(t *testing.T) {
	l := typesOnlyLister{}

	nt, ok := NodeTypeFor(l, "test-ics-v1")
	if !ok || nt.Container || nt.MimeType != "text/calendar" {
		t.Errorf("declared leaf = %+v (ok=%v), want non-container text/calendar", nt, ok)
	}

	nt, ok = NodeTypeFor(l, "test-folder-v1")
	if !ok || !nt.Container {
		t.Errorf("declared container = %+v (ok=%v), want container", nt, ok)
	}

	nt, ok = NodeTypeFor(l, "no-such-tag-v1")
	if ok {
		t.Errorf("unknown tag resolved: %+v", nt)
	}
	if nt.Container {
		t.Error("unknown tag's zero NodeType reports container; want leaf")
	}
	if got := nt.BodyMimeType(); got != MimeTypeDefault {
		t.Errorf("unknown tag body mimetype = %q, want %q", got, MimeTypeDefault)
	}
}
