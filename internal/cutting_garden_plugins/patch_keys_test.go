package cutting_garden_plugins

import (
	"context"
	"net/url"
	"reflect"
	"testing"
)

// keysPlugin is a lister declaring two types; only /item/ URIs resolve
// through PatchTarget, and "templated" carries a URITemplate.
type keysPlugin struct {
	types    []NodeType
	declared []NodeTypePatchKeys
}

func (keysPlugin) Schemes() []string                     { return []string{"x"} }
func (keysPlugin) TypeTag() string                       { return "keys-v1" }
func (keysPlugin) ValidateSource(*url.URL, string) error { return nil }
func (keysPlugin) CaptureRoot(CaptureRootRequest) CaptureRootResult {
	return CaptureRootResult{}
}
func (p keysPlugin) Types() []NodeType { return p.types }
func (keysPlugin) ListRoots(context.Context, *url.URL) ([]Node, error) {
	return nil, nil
}
func (p keysPlugin) DescribePatchKeys() []NodeTypePatchKeys { return p.declared }
func (keysPlugin) PatchTarget(uri *url.URL) (string, bool) {
	if len(uri.Path) > 6 && uri.Path[:6] == "/item/" {
		return "item-v1", true
	}
	return "", false
}

// plainLister is a lister that does not declare patch keys.
type plainLister struct{}

func (plainLister) Schemes() []string                     { return []string{"x"} }
func (plainLister) TypeTag() string                       { return "plain-v1" }
func (plainLister) ValidateSource(*url.URL, string) error { return nil }
func (plainLister) CaptureRoot(CaptureRootRequest) CaptureRootResult {
	return CaptureRootResult{}
}
func (plainLister) Types() []NodeType { return []NodeType{{Tag: "item-v1"}} }
func (plainLister) ListRoots(context.Context, *url.URL) ([]Node, error) {
	return nil, nil
}

func parse(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestResolvePatchKeys_ViaPatchTarget(t *testing.T) {
	p := keysPlugin{
		types:    []NodeType{{Tag: "item-v1"}},
		declared: []NodeTypePatchKeys{{Tag: "item-v1", Keys: []string{"a", "b"}}},
	}
	got, ok := ResolvePatchKeys(p, parse(t, "x://h/item/1"))
	if !ok || got.Tag != "item-v1" || !reflect.DeepEqual(got.Keys, []string{"a", "b"}) {
		t.Errorf("got %+v %v", got, ok)
	}
}

// A declared URITemplate wins over PatchTarget (RFC 0018 §4 first).
func TestResolvePatchKeys_URITemplateFirst(t *testing.T) {
	p := keysPlugin{
		types: []NodeType{{Tag: "templated-v1", URITemplate: "x://{h}/item/{id}"}},
		declared: []NodeTypePatchKeys{
			{Tag: "templated-v1", Keys: []string{"t"}},
			{Tag: "item-v1", Keys: []string{"i"}},
		},
	}
	got, ok := ResolvePatchKeys(p, parse(t, "x://h/item/1"))
	if !ok || got.Tag != "templated-v1" || !reflect.DeepEqual(got.Keys, []string{"t"}) {
		t.Errorf("got %+v %v, want the template's type", got, ok)
	}
}

func TestResolvePatchKeys_NotOkWhenUnknownOrUndeclared(t *testing.T) {
	p := keysPlugin{
		types:    []NodeType{{Tag: "item-v1"}},
		declared: []NodeTypePatchKeys{{Tag: "other-v1", Keys: []string{"a"}}},
	}
	// Type resolves, but declares no keys.
	if _, ok := ResolvePatchKeys(p, parse(t, "x://h/item/1")); ok {
		t.Error("ok for a type with no declared keys")
	}
	// Type cannot be told.
	if _, ok := ResolvePatchKeys(p, parse(t, "x://h/elsewhere")); ok {
		t.Error("ok for a URI no type resolves")
	}
	// Not a describer at all.
	if _, ok := ResolvePatchKeys(plainLister{}, parse(t, "x://h/item/1")); ok {
		t.Error("ok for a lister without PatchKeysDescriber")
	}
}

func TestResolvePatchKeys_ReturnsACopy(t *testing.T) {
	p := keysPlugin{
		types:    []NodeType{{Tag: "item-v1"}},
		declared: []NodeTypePatchKeys{{Tag: "item-v1", Keys: []string{"a"}}},
	}
	got, _ := ResolvePatchKeys(p, parse(t, "x://h/item/1"))
	got.Keys[0] = "mutated"
	if p.declared[0].Keys[0] != "a" {
		t.Error("the declaration was mutated through the returned slice")
	}
}
