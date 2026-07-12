package cutting_garden_plugin_file

import (
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

func init() {
	p := Plugin{}
	cutting_garden_plugins.MustRegisterCapture(p)
	cutting_garden_plugins.MustRegisterRestore(p)
	cutting_garden_plugins.MustRegisterDiff(p)
}
