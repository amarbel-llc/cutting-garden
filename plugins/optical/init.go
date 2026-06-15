package cutting_garden_plugin_optical

import (
	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
)

func init() {
	p := Plugin{}
	cutting_garden_plugins.MustRegisterCapture(p)
	// Restore and diff intentionally not registered: optical artifacts
	// are regular files the filesystem plugin materializes, and a disc
	// re-rip is far too expensive to back a diff probe. See
	// docs/features/0016-optical-plugin.md.
}
