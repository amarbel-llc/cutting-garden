package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// issueLister models the cutting-garden#168 case: a container
// ("test-issue-v1") that has children (comments) AND its own body (a
// title/state). withTemplate toggles whether the issue type declares a URI
// template (so URI→type resolution can gate the body read locally, or —
// when absent — the host must probe). declareBody toggles the BodyDescriber
// entry, so a test can prove the declaration gate SKIPS the body read even
// though ReadLeaf would return one.
type issueLister struct {
	withTemplate bool
	declareBody  bool
}

func (issueLister) Schemes() []string                     { return []string{"fj2"} }
func (issueLister) TypeTag() string                       { return "cutting_garden-fj2-v1" }
func (issueLister) ValidateSource(*url.URL, string) error { return nil }
func (issueLister) CaptureRoot(
	cutting_garden_plugins.CaptureRootRequest,
) cutting_garden_plugins.CaptureRootResult {
	return cutting_garden_plugins.CaptureRootResult{}
}

const issueURI = "fj2://h/issues/1"

func (l issueLister) Types() []cutting_garden_plugins.NodeType {
	issue := cutting_garden_plugins.NodeType{Tag: "test-issue-v1", Container: true}
	if l.withTemplate {
		issue.URITemplate = "fj2://{host}/issues/{n}"
	}
	return []cutting_garden_plugins.NodeType{
		issue,
		{Tag: "test-comment-v1", Container: false},
	}
}

func (issueLister) ListRoots(
	_ context.Context, node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	if node == nil {
		return nil, errors.ErrorWithStackf("fj2: nil node")
	}
	mk := func(path, name, typ string) cutting_garden_plugins.Node {
		return cutting_garden_plugins.Node{
			URI:  &url.URL{Scheme: "fj2", Host: node.Host, Path: path},
			Name: name,
			Type: typ,
		}
	}
	switch node.Path {
	case "/", "":
		return []cutting_garden_plugins.Node{
			mk("/issues/1", "issue 1", "test-issue-v1"),
		}, nil
	case "/issues/1": // the container has one child comment
		return []cutting_garden_plugins.Node{
			mk("/issues/1/comments/1", "comment 1", "test-comment-v1"),
		}, nil
	default:
		return nil, nil
	}
}

// ReadLeaf returns the issue's OWN body for the issue URI (a container with
// children), and nothing for the endpoint. This is exactly the node the
// pre-RFC-0018 read path could never expose.
func (issueLister) ReadLeaf(
	_ context.Context, node *url.URL,
) (cutting_garden_plugins.LeafContent, bool, error) {
	if node.Path == "/issues/1" {
		return cutting_garden_plugins.LeafContent{
			Structured: map[string]any{"title": "Fix it", "state": "open"},
		}, true, nil
	}
	return cutting_garden_plugins.LeafContent{}, false, nil
}

func (l issueLister) DescribeBodies() []cutting_garden_plugins.NodeTypeBody {
	if !l.declareBody {
		return nil
	}
	return []cutting_garden_plugins.NodeTypeBody{
		{Tag: "test-issue-v1", Accepts: []string{"application/json"}},
	}
}

func newIssueResources(withTemplate, declareBody bool) *Resources {
	lister := issueLister{withTemplate: withTemplate, declareBody: declareBody}
	resolve := func(uriStr string) (
		*url.URL, cutting_garden_plugins.RootLister, error,
	) {
		u, err := url.Parse(uriStr)
		if err != nil {
			return nil, nil, errors.Wrap(err)
		}
		if u.Scheme != "fj2" {
			return nil, nil, errors.ErrorWithStackf("unknown scheme %q", u.Scheme)
		}
		return u, lister, nil
	}
	return &Resources{
		resolve:  resolve,
		facets:   newFacetCache(),
		listings: newListingCache(),
	}
}

// readNodeText renders a ReadNode result the way the read_node tool does, so
// the mode assertions read the caller-visible shape.
func readNodeText(t *testing.T, r *Resources, uri, content string) string {
	t.Helper()
	res, err := r.ReadNode(context.Background(), uri, content)
	if err != nil {
		t.Fatalf("ReadNode(%q, %q): %v", uri, content, err)
	}
	return renderReadNode(res.Contents)
}

// TestReadNode_BothReturnsBodyAndChildren_DeclarationGated: a container that
// declares a template AND a body returns, in both mode, a wrapper carrying
// its own body beside its child listing (RFC 0018 §7).
func TestReadNode_BothReturnsBodyAndChildren_DeclarationGated(t *testing.T) {
	r := newIssueResources(true, true)

	text := readNodeText(t, r, issueURI, contentBoth)

	var wrapper struct {
		Body     map[string]any `json:"body"`
		Children listingView    `json:"children"`
	}
	if err := json.Unmarshal([]byte(text), &wrapper); err != nil {
		t.Fatalf("both-mode output is not the {body,children} wrapper: %v\n%s",
			err, text)
	}
	if wrapper.Body["title"] != "Fix it" || wrapper.Body["state"] != "open" {
		t.Errorf("body = %v, want the issue's title/state", wrapper.Body)
	}
	// children is the #203 listing wrapper ({nodes:[...]}), not a bare array.
	if len(wrapper.Children.Nodes) != 1 {
		t.Errorf("children = %v, want the one comment", wrapper.Children)
	}
}

// TestReadNode_ChildrenOmitsBody: children mode returns only the listing
// array — never the container's own body (RFC 0018 §7.3), byte-for-byte the
// pre-RFC-0018 shape.
func TestReadNode_ChildrenOmitsBody(t *testing.T) {
	r := newIssueResources(true, true)

	text := readNodeText(t, r, issueURI, contentChildren)

	if !strings.HasPrefix(strings.TrimSpace(text), "{") ||
		!strings.Contains(text, `"nodes"`) {
		t.Fatalf("children mode is not the #203 listing wrapper:\n%s", text)
	}
	if strings.Contains(text, "Fix it") {
		t.Errorf("children mode leaked the container body:\n%s", text)
	}
}

// TestReadNode_BodyOmitsChildren: body mode returns only the node's own
// body object, never the listing (RFC 0018 §7.3).
func TestReadNode_BodyOmitsChildren(t *testing.T) {
	r := newIssueResources(true, true)

	text := readNodeText(t, r, issueURI, contentBody)

	if !strings.HasPrefix(strings.TrimSpace(text), "{") {
		t.Fatalf("body mode is not a bare object:\n%s", text)
	}
	if !strings.Contains(text, "Fix it") {
		t.Errorf("body mode missing the container body:\n%s", text)
	}
	if strings.Contains(text, "comment 1") {
		t.Errorf("body mode leaked the child listing:\n%s", text)
	}
}

// TestReadNode_TemplatelessBothProbes: with no template the URI resolves to
// ⊥, so both mode probes leaf.read and still surfaces the container body
// (RFC 0018 §6 fallback).
func TestReadNode_TemplatelessBothProbes(t *testing.T) {
	r := newIssueResources(false, true)

	text := readNodeText(t, r, issueURI, contentBoth)

	if !strings.Contains(text, "Fix it") {
		t.Errorf("template-less both mode did not probe the body:\n%s", text)
	}
}

// TestReadNode_DeclarationGateSkipsUndeclaredBody: a container that declares
// a template but NOT a body has its body read SKIPPED in both mode — even
// though ReadLeaf would return one — proving the gate saves the round trip
// (RFC 0018 §7.2). The result is the plain listing, no body.
func TestReadNode_DeclarationGateSkipsUndeclaredBody(t *testing.T) {
	r := newIssueResources(true, false)

	text := readNodeText(t, r, issueURI, contentBoth)

	if strings.Contains(text, "Fix it") {
		t.Errorf("gate did not skip the undeclared body:\n%s", text)
	}
	if !strings.HasPrefix(strings.TrimSpace(text), "{") ||
		!strings.Contains(text, `"nodes"`) {
		t.Errorf("expected the #203 listing wrapper when the body is skipped:\n%s",
			text)
	}
}
