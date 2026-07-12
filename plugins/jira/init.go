package jira

import (
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

func init() {
	p := Plugin{}
	cutting_garden_plugins.MustRegisterCapture(p)
	cutting_garden_plugins.MustRegisterDiff(p)
	// Protocol diff routes by receipt KIND (ProtocolKind), via a separate
	// registry from the scheme-keyed flat capabilities above. Without it, a
	// jira-v1 protocol receipt fails to dispatch ("unknown protocol diff
	// kind \"jira\""). Protocol CAPTURE needs no registration: the
	// orchestrator resolves the scheme's CapturePlugin (above) and
	// type-asserts ProtocolCapturePlugin (git/caldav's pattern).
	cutting_garden_plugins.MustRegisterProtocolDiff(p)
	// Restore (flat and protocol) intentionally not registered: snapshotting
	// issue state is read-only, and writing issues back to a live tracker is
	// a lossy, destructive mutation. See docs/features/0019-jira-plugin.md
	// §Restore Deferral.
}
