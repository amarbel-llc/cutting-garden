package cutting_garden_plugins

import (
	"context"
	"errors"
	"net/url"
	"testing"
)

// rootProviderOnly is a plugin that implements neither capture, restore,
// nor diff — only the base Plugin plus RootLister/RootProvider. Its sole
// registration path is MustRegisterScheme; it models an out-of-tree
// traversal plugin such as a nix_store cache (RFC 0009).
type rootProviderOnly struct{}

func (rootProviderOnly) Schemes() []string { return []string{"schemetest"} }
func (rootProviderOnly) TypeTag() string   { return "cutting_garden-schemetest-v1" }
func (rootProviderOnly) Types() []NodeType {
	return []NodeType{{Tag: "cutting_garden-schemetest-v1"}}
}

func (rootProviderOnly) ListRoots(context.Context, *url.URL) ([]Node, error) {
	return nil, nil
}
func (rootProviderOnly) Roots(context.Context) ([]*url.URL, error) { return nil, nil }

func TestMustRegisterScheme_ResolvesAndEnumerates(t *testing.T) {
	MustRegisterScheme(rootProviderOnly{})

	// Resolvable by scheme.
	got, err := ResolveScheme("schemetest")
	if err != nil {
		t.Fatalf("ResolveScheme(schemetest) error: %v", err)
	}
	if _, ok := got.(rootProviderOnly); !ok {
		t.Fatalf("ResolveScheme(schemetest) = %T, want rootProviderOnly", got)
	}

	// A scheme-only plugin (no capture/restore/diff registration) MUST
	// still be enumerated by RegisteredPlugins — the property list/mcp
	// depend on for RootProvider discovery (RFC 0009 §3).
	var found bool
	for _, p := range RegisteredPlugins() {
		if _, ok := p.(rootProviderOnly); ok {
			found = true
			break
		}
	}
	if !found {
		t.Error("RegisteredPlugins() omitted a scheme-only plugin; RootProvider discovery would miss it")
	}
}

func TestResolveScheme_UnknownWrapsSentinel(t *testing.T) {
	_, err := ResolveScheme("no-such-scheme")
	if !errors.Is(err, ErrUnknownScheme) {
		t.Errorf("ResolveScheme(unknown) error = %v, want wrap of ErrUnknownScheme", err)
	}
}

func TestMustRegisterScheme_DuplicatePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("duplicate MustRegisterScheme did not panic")
		}
	}()
	MustRegisterScheme(dupSchemePlugin{})
	MustRegisterScheme(dupSchemePlugin{}) // second registration must panic
}

type dupSchemePlugin struct{}

func (dupSchemePlugin) Schemes() []string { return []string{"duptest"} }
func (dupSchemePlugin) TypeTag() string   { return "cutting_garden-duptest-v1" }
