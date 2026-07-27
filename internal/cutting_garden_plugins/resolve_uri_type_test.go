package cutting_garden_plugins

import (
	"context"
	"net/url"
	"testing"
)

// templateLister is a RootLister whose declared types (with their URI
// templates) are configurable per test.
type templateLister struct{ types []NodeType }

func (templateLister) Schemes() []string                     { return []string{"tmpltest"} }
func (templateLister) TypeTag() string                       { return "cutting_garden-tmpl-v1" }
func (templateLister) ValidateSource(*url.URL, string) error { return nil }
func (templateLister) CaptureRoot(CaptureRootRequest) CaptureRootResult {
	return CaptureRootResult{}
}
func (l templateLister) Types() []NodeType { return l.types }
func (templateLister) ListRoots(context.Context, *url.URL) ([]Node, error) {
	return nil, nil
}

// TestResolveNodeTypeByURI_Match: a URI matching exactly one declared
// template resolves to that type with its captured bindings.
func TestResolveNodeTypeByURI_Match(t *testing.T) {
	lister := templateLister{types: []NodeType{
		{
			Tag: "fj-repo-v1", Container: true,
			URITemplate: "fj://{host}/{owner}/{repo}",
		},
		{
			Tag: "fj-issue-v1", Container: true,
			URITemplate: "fj://{host}/{owner}/{repo}/issues/{number}",
		},
	}}

	got, ok := ResolveNodeTypeByURI(
		lister, "fj://forge.example/acme/web/issues/42",
	)
	if !ok {
		t.Fatal("resolve = not ok; want fj-issue-v1")
	}
	if got.Type.Tag != "fj-issue-v1" {
		t.Errorf("Type.Tag = %q, want fj-issue-v1", got.Type.Tag)
	}
	if got.Bindings["number"] != "42" || got.Bindings["repo"] != "web" {
		t.Errorf("bindings = %v", got.Bindings)
	}
}

// TestResolveNodeTypeByURI_DistinctBySegmentCount: the benign overlap —
// repo vs issue differ by segment count, so a repo URI resolves to the
// repo type, not the issue type.
func TestResolveNodeTypeByURI_DistinctBySegmentCount(t *testing.T) {
	lister := templateLister{types: []NodeType{
		{
			Tag: "fj-repo-v1", Container: true,
			URITemplate: "fj://{host}/{owner}/{repo}",
		},
		{
			Tag: "fj-issue-v1", Container: true,
			URITemplate: "fj://{host}/{owner}/{repo}/issues/{number}",
		},
	}}

	got, ok := ResolveNodeTypeByURI(lister, "fj://forge.example/acme/web")
	if !ok || got.Type.Tag != "fj-repo-v1" {
		t.Fatalf("resolve = (%q, %v), want fj-repo-v1", got.Type.Tag, ok)
	}
}

// TestResolveNodeTypeByURI_MostSpecificWins: two templates match, but one
// has strictly more literal characters (an extra literal segment), so it
// wins without a tie.
func TestResolveNodeTypeByURI_MostSpecificWins(t *testing.T) {
	lister := templateLister{types: []NodeType{
		{Tag: "generic-v1", URITemplate: "x://{a}/{b}"},
		{Tag: "special-v1", URITemplate: "x://{a}/special"},
	}}

	got, ok := ResolveNodeTypeByURI(lister, "x://foo/special")
	if !ok || got.Type.Tag != "special-v1" {
		t.Fatalf("resolve = (%q, %v), want special-v1 (more literal)",
			got.Type.Tag, ok)
	}
}

// TestResolveNodeTypeByURI_TieToNil: two templates that a URI matches with
// no specificity winner resolve to ⊥ (ok == false) — the host MUST fall
// back, never guess (RFC 0018 §4).
func TestResolveNodeTypeByURI_TieToNil(t *testing.T) {
	lister := templateLister{types: []NodeType{
		{Tag: "fj-repo-v1", URITemplate: "fj://{host}/{owner}/{repo}"},
		{Tag: "fj-user-v1", URITemplate: "fj://{host}/{owner}/{name}"},
	}}

	if _, ok := ResolveNodeTypeByURI(
		lister, "fj://forge.example/acme/web",
	); ok {
		t.Fatal("resolve = ok on a true tie; want ⊥ (fall back to probe)")
	}
}

// TestResolveNodeTypeByURI_NoTemplateOrNoMatch: a type with no template is
// skipped, and a URI no template matches resolves to ⊥.
func TestResolveNodeTypeByURI_NoTemplateOrNoMatch(t *testing.T) {
	lister := templateLister{types: []NodeType{
		{Tag: "no-template-v1", Container: true},
		{Tag: "fj-repo-v1", URITemplate: "fj://{host}/{owner}/{repo}"},
	}}

	if _, ok := ResolveNodeTypeByURI(lister, "fj://only/two"); ok {
		t.Error("resolve = ok on a non-matching URI; want ⊥")
	}
	if _, ok := ResolveNodeTypeByURI(
		lister, "other://a/b/c",
	); ok {
		t.Error("resolve = ok on a foreign scheme; want ⊥")
	}
}

// TestResolveNodeTypeByURI_UnparseableTemplateSkipped: a malformed
// template resolves nothing rather than failing the read (RFC 0018 §4
// leaves rejection to an OPTIONAL init check).
func TestResolveNodeTypeByURI_UnparseableTemplateSkipped(t *testing.T) {
	lister := templateLister{types: []NodeType{
		{Tag: "bad-v1", URITemplate: "x://{a}{b}"}, // adjacent vars: invalid
	}}

	if _, ok := ResolveNodeTypeByURI(lister, "x://foo"); ok {
		t.Error("resolve = ok against an unparseable template; want ⊥")
	}
}
