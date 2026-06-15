package cutting_garden_plugin_git

import (
	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
)

func init() {
	p := Plugin{}
	cutting_garden_plugins.MustRegisterCapture(p)
	cutting_garden_plugins.MustRegisterDiff(p)
	// RFC 0002 protocol restore/diff, keyed by the "git" receipt kind.
	// The EntryV1 RestorePlugin is still not registered (the file plugin
	// materializes EntryV1 git captures); these handle the protocol
	// receipts that `capture git:…` now writes.
	cutting_garden_plugins.MustRegisterProtocolRestore(p)
	cutting_garden_plugins.MustRegisterProtocolDiff(p)
}
