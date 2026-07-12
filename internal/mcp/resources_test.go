package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
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
		{Tag: "test-object-v1", Container: false, MimeType: "text/calendar"},
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
	if leaf.MimeType != "text/calendar" {
		t.Errorf("child mimetype = %q, want the declared text/calendar", leaf.MimeType)
	}
}

// TestReadResource_LeafYieldsEmptyListing covers a plugin WITHOUT the
// LeafReader capability (fakeLister): a childless node still reads as an
// empty array, the pre-#85 behavior.
func TestReadResource_LeafYieldsEmptyListing(t *testing.T) {
	r := newFakeResources(t, "faketest://h/")

	got, err := r.ReadResource(
		context.Background(), "faketest://h/work/task1.ics",
	)
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

// fakeLeafLister is a fakeLister that also implements LeafReader: it treats
// any ".ics" leaf as a fetchable object with a structured body and declines
// anything else, so both the leaf-body and the fall-back-to-empty paths are
// exercised.
type fakeLeafLister struct{ fakeLister }

func (fakeLeafLister) ReadLeaf(
	_ context.Context, node *url.URL,
) (cutting_garden_plugins.LeafContent, bool, error) {
	if !strings.HasSuffix(node.Path, ".ics") {
		return cutting_garden_plugins.LeafContent{}, false, nil
	}
	return cutting_garden_plugins.LeafContent{
		Structured:  map[string]any{"component": "VTODO", "summary": "Task One"},
		Raw:         []byte("BEGIN:VCALENDAR\nBEGIN:VTODO\nEND:VTODO\nEND:VCALENDAR\n"),
		RawMimeType: "text/calendar",
	}, true, nil
}

func newFakeLeafResources(t *testing.T, rootStrs ...string) *Resources {
	t.Helper()
	r := newFakeResources(t, rootStrs...)
	r.resolve = func(uriStr string) (*url.URL, cutting_garden_plugins.RootLister, error) {
		u, _, err := fakeResolve(uriStr)
		if err != nil {
			return nil, nil, err
		}
		return u, fakeLeafLister{}, nil
	}
	return r
}

// TestReadResource_LeafYieldsStructuredBody is the #85 path: a childless
// node whose plugin can fetch it reads as the object's structured JSON
// fields (a JSON object), not the empty array a listing yields.
func TestReadResource_LeafYieldsStructuredBody(t *testing.T) {
	r := newFakeLeafResources(t, "faketest://h/")

	got, err := r.ReadResource(context.Background(), "faketest://h/work/task1.ics")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(got.Contents) != 1 {
		t.Fatalf("got %d content blocks, want 1", len(got.Contents))
	}
	c := got.Contents[0]
	if c.MimeType != mimeObject {
		t.Errorf("content mimetype = %q, want %q", c.MimeType, mimeObject)
	}
	// A JSON object (the parsed fields), not the array a listing would emit:
	// decoding into a map fails on an array, so this also pins object-ness.
	var obj map[string]any
	if err := json.Unmarshal([]byte(c.Text), &obj); err != nil {
		t.Fatalf("decode object %q: %v", c.Text, err)
	}
	if obj["summary"] != "Task One" {
		t.Errorf("summary = %v, want Task One; body=%s", obj["summary"], c.Text)
	}
}

// TestReadResource_LeafReaderDeclineFallsBackToEmpty pins that a LeafReader
// reporting ok=false (not a leaf) yields the empty listing, not an error.
func TestReadResource_LeafReaderDeclineFallsBackToEmpty(t *testing.T) {
	r := newFakeLeafResources(t, "faketest://h/")

	got, err := r.ReadResource(context.Background(), "faketest://h/empty")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if got.Contents[0].MimeType != mimeListing {
		t.Errorf("declined-leaf mimetype = %q, want listing %q",
			got.Contents[0].MimeType, mimeListing)
	}
	var views []nodeView
	if err := json.Unmarshal([]byte(got.Contents[0].Text), &views); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	if len(views) != 0 {
		t.Errorf("declined leaf yielded %d children, want 0: %+v", len(views), views)
	}
}

func TestReadResource_ResolveErrorSurfaces(t *testing.T) {
	r := newFakeResources(t, "faketest://h/")
	if _, err := r.ReadResource(context.Background(), "bogus://x"); err == nil {
		t.Fatal("ReadResource on unresolvable uri: want error, got nil")
	}
}

// fakeWriter is a capture_plugin.Writer that records the bytes it is given
// and returns a fixed digest, so a leaf read's raw-bytes write is testable
// without a real blob store.
type fakeWriter struct {
	digest  string
	written []byte
}

func (w *fakeWriter) WriteBlob(_ context.Context, r io.Reader) (string, int64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", 0, err
	}
	w.written = b
	return w.digest, int64(len(b)), nil
}

// TestReadResource_LeafAppendsRawBlobLink is the Phase B path (#85): with a
// store configured, a leaf read writes the verbatim bytes and appends a
// link-only second content entry addressing them by digest — no inlined
// bytes.
func TestReadResource_LeafAppendsRawBlobLink(t *testing.T) {
	r := newFakeLeafResources(t, "faketest://h/")
	w := &fakeWriter{digest: "blake2b256-deadbeef"}
	r.writer = w

	got, err := r.ReadResource(context.Background(), "faketest://h/work/task1.ics")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(got.Contents) != 2 {
		t.Fatalf("got %d contents, want 2 (structured + blob link)", len(got.Contents))
	}

	// [0] is the structured JSON object (unchanged from the no-store path).
	if got.Contents[0].MimeType != mimeObject {
		t.Errorf("content[0] mimetype = %q, want %q", got.Contents[0].MimeType, mimeObject)
	}

	// [1] is the link-only madder blob reference: a URI + mimetype, no bytes.
	link := got.Contents[1]
	if link.URI != "madder://blobs/blake2b256-deadbeef" {
		t.Errorf("link URI = %q, want madder://blobs/blake2b256-deadbeef", link.URI)
	}
	if link.MimeType != "text/calendar" {
		t.Errorf("link mimetype = %q, want text/calendar", link.MimeType)
	}
	if link.Text != "" || link.Blob != "" {
		t.Errorf("link must be link-only: text=%q blob=%q", link.Text, link.Blob)
	}

	// The verbatim source bytes are what was written to the store.
	if !strings.Contains(string(w.written), "BEGIN:VTODO") {
		t.Errorf("writer received %q, want the verbatim .ics body", w.written)
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

func TestNodeToResource_LeafAdvertisesBodyMimeType(t *testing.T) {
	mk := func(typ string) cutting_garden_plugins.Node {
		return cutting_garden_plugins.Node{
			URI:  &url.URL{Scheme: "faketest", Host: "h", Path: "/x"},
			Name: "x",
			Type: typ,
		}
	}

	// A declared leaf carries its declared mimetype.
	if got := nodeToResource(fakeLister{}, mk("test-object-v1")).MimeType; got != "text/calendar" {
		t.Errorf("declared leaf mimetype = %q, want text/calendar", got)
	}
	// An unknown tag is a leaf of unspecified mimetype → the contract
	// default, never an invented container.
	if got := nodeToResource(fakeLister{}, mk("no-such-tag-v1")).MimeType; got != cutting_garden_plugins.MimeTypeDefault {
		t.Errorf("unknown-tag leaf mimetype = %q, want %q",
			got, cutting_garden_plugins.MimeTypeDefault)
	}
	// A container keeps the listing mimetype, not a body type.
	if got := nodeToResource(fakeLister{}, mk("test-calendar-v1")).MimeType; got != mimeListing {
		t.Errorf("container mimetype = %q, want %q", got, mimeListing)
	}
}
