package cutting_garden_plugin_googlephotos

import (
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
)

func init() {
	p := Plugin{}
	cutting_garden_plugins.MustRegisterCapture(p)
	cutting_garden_plugins.MustRegisterDiff(p)
	// Restore intentionally not registered. See FDR 0017 §Restore
	// Deferral.
}
