package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// fakeLister is a registry-free RootLister for the provider tests. It
// models a two-level tree: an endpoint's children are containers
// ("calendars"); a container's children are leaves ("objects"); a leaf
// has no children.
type fakeLister struct{}

func (fakeLister) Schemes() []string                     { return []string{"faketest"} }
func (fakeLister) TypeTag() string                       { return "cutting_garden-test-v1" }
func (fakeLister) ValidateSource(*url.URL, string) error { return nil }
func (fakeLister) CaptureRoot(
	cutting_garden_plugins.CaptureRootRequest,
) cutting_garden_plugins.CaptureRootResult {
	return cutting_garden_plugins.CaptureRootResult{}
}

func (fakeLister) Types() []cutting_garden_plugins.NodeType {
	return []cutting_garden_plugins.NodeType{
		{Tag: "test-calendar-v1", Container: true},
		{Tag: "test-object-v1", Container: false},
	}
}

func (fakeLister) ListRoots(
	_ context.Context,
	node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	if node == nil {
		return nil, errors.ErrorWithStackf("faketest: nil node")
	}
	mk := func(path, name, typ string) cutting_garden_plugins.Node {
		return cutting_garden_plugins.Node{
			URI:  &url.URL{Scheme: "faketest", Host: node.Host, Path: path},
			Name: name,
			Type: typ,
		}
	}
	switch node.Path {
	case "/", "": // endpoint → two calendar containers
		return []cutting_garden_plugins.Node{
			mk("/work", "Work", "test-calendar-v1"),
			mk("/personal", "Personal", "test-calendar-v1"),
		}, nil
	case "/work": // a container → one leaf object
		return []cutting_garden_plugins.Node{
			mk("/work/task1.ics", "task1.ics", "test-object-v1"),
		}, nil
	default: // a leaf → no children
		return nil, nil
	}
}

// fakeResolve is the injected resolver: every faketest URI resolves to
// fakeLister, anything else is unknown.
func fakeResolve(
	uriStr string,
) (*url.URL, cutting_garden_plugins.RootLister, error) {
	u, err := url.Parse(uriStr)
	if err != nil {
		return nil, nil, errors.Wrap(err)
	}
	if u.Scheme != "faketest" {
		return nil, nil, errors.ErrorWithStackf("unknown scheme %q", u.Scheme)
	}
	return u, fakeLister{}, nil
}

func newFakeResources(t *testing.T, rootStrs ...string) *Resources {
	t.Helper()
	roots := make([]*url.URL, 0, len(rootStrs))
	for _, s := range rootStrs {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatalf("parse root %q: %v", s, err)
		}
		roots = append(roots, u)
	}
	return &Resources{roots: roots, resolve: fakeResolve}
}

func TestListResources_ChildrenOfRoots(t *testing.T) {
	r := newFakeResources(t, "faketest://h/")

	res, err := r.ListResources(context.Background())
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("got %d resources, want 2: %+v", len(res), res)
	}

	byURI := map[string]string{} // uri -> mimetype
	for _, x := range res {
		byURI[x.URI] = x.MimeType
	}
	for _, uri := range []string{"faketest://h/work", "faketest://h/personal"} {
		mime, ok := byURI[uri]
		if !ok {
			t.Errorf("missing resource %q in %+v", uri, res)
			continue
		}
		// Both children are containers → advertise the listing mimetype.
		if mime != mimeListing {
			t.Errorf("resource %q mimetype = %q, want %q", uri, mime, mimeListing)
		}
	}
}

func TestListResources_MultipleRoots(t *testing.T) {
	// Two endpoints, two children each → four resources.
	r := newFakeResources(t, "faketest://a/", "faketest://b/")
	res, err := r.ListResources(context.Background())
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(res) != 4 {
		t.Fatalf("got %d resources, want 4: %+v", len(res), res)
	}
}

func TestListResources_ResolveErrorSurfaces(t *testing.T) {
	r := newFakeResources(t, "bogus://h/")
	if _, err := r.ListResources(context.Background()); err == nil {
		t.Fatal("ListResources on unresolvable root: want error, got nil")
	}
}

func TestReadResource_ContainerYieldsChildren(t *testing.T) {
	r := newFakeResources(t, "faketest://h/")

	got, err := r.ReadResource(context.Background(), "faketest://h/work")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(got.Contents) != 1 {
		t.Fatalf("got %d content blocks, want 1", len(got.Contents))
	}
	c := got.Contents[0]
	if c.MimeType != mimeListing {
		t.Errorf("content mimetype = %q, want %q", c.MimeType, mimeListing)
	}

	var views []nodeView
	if err := json.Unmarshal([]byte(c.Text), &views); err != nil {
		t.Fatalf("decode listing %q: %v", c.Text, err)
	}
	if len(views) != 1 {
		t.Fatalf("got %d child views, want 1: %+v", len(views), views)
	}
	leaf := views[0]
	if leaf.URI != "faketest://h/work/task1.ics" {
		t.Errorf("child uri = %q", leaf.URI)
	}
	if leaf.Container {
		t.Errorf("object child reported as container: %+v", leaf)
	}
	if leaf.Type != "test-object-v1" {
		t.Errorf("child type = %q, want test-object-v1", leaf.Type)
	}
}

func TestReadResource_LeafYieldsEmptyListing(t *testing.T) {
	r := newFakeResources(t, "faketest://h/")

	got, err := r.ReadResource(
		context.Background(), "faketest://h/work/task1.ics")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	var views []nodeView
	if err := json.Unmarshal([]byte(got.Contents[0].Text), &views); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	if len(views) != 0 {
		t.Errorf("leaf read yielded %d children, want 0: %+v", len(views), views)
	}
}

func TestReadResource_ResolveErrorSurfaces(t *testing.T) {
	r := newFakeResources(t, "faketest://h/")
	if _, err := r.ReadResource(context.Background(), "bogus://x"); err == nil {
		t.Fatal("ReadResource on unresolvable uri: want error, got nil")
	}
}

func TestListResourceTemplates_Empty(t *testing.T) {
	r := newFakeResources(t, "faketest://h/")
	tmpls, err := r.ListResourceTemplates(context.Background())
	if err != nil {
		t.Fatalf("ListResourceTemplates: %v", err)
	}
	if len(tmpls) != 0 {
		t.Errorf("got %d templates, want 0", len(tmpls))
	}
}

func TestIsContainer_UnknownTagIsLeaf(t *testing.T) {
	if isContainer(fakeLister{}, "no-such-tag-v1") {
		t.Error("unknown tag treated as container; want leaf")
	}
	if !isContainer(fakeLister{}, "test-calendar-v1") {
		t.Error("declared container tag treated as leaf")
	}
}
