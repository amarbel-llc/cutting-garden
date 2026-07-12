package caldav

import (
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

func init() {
	p := Plugin{}
	cutting_garden_plugins.MustRegisterCapture(p)
	cutting_garden_plugins.MustRegisterRestore(p)
	cutting_garden_plugins.MustRegisterDiff(p)
	// Protocol restore/diff route by receipt KIND (ProtocolKind), via a
	// separate registry from the scheme-keyed flat capabilities above.
	// Without these, a caldav-v1 protocol receipt fails to dispatch
	// ("unknown protocol restore kind \"caldav\""). Protocol CAPTURE needs
	// no registration: the orchestrator resolves the scheme's CapturePlugin
	// (above) and type-asserts ProtocolCapturePlugin (git's pattern).
	cutting_garden_plugins.MustRegisterProtocolRestore(p)
	cutting_garden_plugins.MustRegisterProtocolDiff(p)
}
