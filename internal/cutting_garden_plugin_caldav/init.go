package cutting_garden_plugin_caldav

import (
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
)

func init() {
	p := Plugin{}
	cutting_garden_plugins.MustRegisterCapture(p)
	cutting_garden_plugins.MustRegisterRestore(p)
	cutting_garden_plugins.MustRegisterDiff(p)
}
