package cutting_garden_plugin_ytdlp

import (
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

func init() {
	p := Plugin{}
	cutting_garden_plugins.MustRegisterCapture(p)
	cutting_garden_plugins.MustRegisterDiff(p)
	// Restore intentionally not registered. See FDR 0003 §Restore
	// Deferral.
}
