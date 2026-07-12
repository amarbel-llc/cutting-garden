package caldav

import (
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// TestProtocolRegistration guards the registry wiring the unit tests
// (which call RestoreProtocol/DiffProtocol directly) bypass: the binary
// dispatches a protocol receipt by KIND through the protocol registries,
// so the plugin's init() MUST register itself there. A missing
// MustRegisterProtocolRestore/Diff manifests only at the command layer
// ("unknown protocol restore kind \"caldav\"") — caught by the bats lane,
// but this pins it at Go-test speed.
func TestProtocolRegistration(t *testing.T) {
	if _, err := cutting_garden_plugins.ResolveProtocolRestore(captureKind); err != nil {
		t.Errorf("ResolveProtocolRestore(%q) = %v, want the caldav plugin registered", captureKind, err)
	}
	if _, err := cutting_garden_plugins.ResolveProtocolDiff(captureKind); err != nil {
		t.Errorf("ResolveProtocolDiff(%q) = %v, want the caldav plugin registered", captureKind, err)
	}
}
