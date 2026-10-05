package command_components

import (
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/traversal_serve"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// TestRegisterStanza_OncePerDefinition pins cutting-garden#262: a wire-plugin
// stanza registers once per process and is remembered by its definition, so
// loading the same config again is a no-op, while the same name under a
// different definition is refused rather than silently ignored.
func TestRegisterStanza_OncePerDefinition(t *testing.T) {
	stanza := traversal_serve.PluginStanza{
		Name:    "cc262",
		Command: []string{"/nonexistent/cc262-peer"},
		Schemes: []string{"cc262"},
	}

	if err := registerStanza(stanza, nil, true); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	if err := registerStanza(stanza, nil, true); err != nil {
		t.Fatalf("same definition again must be a no-op, got: %v", err)
	}

	changed := stanza
	changed.Command = []string{"/nonexistent/other-peer"}
	err := registerStanza(changed, nil, true)
	if err == nil {
		t.Fatal("a changed definition under the same name must be refused")
	}
	if !errors.Is400BadRequest(err) {
		t.Errorf("changed definition: want a bad request, got: %v", err)
	}
}
