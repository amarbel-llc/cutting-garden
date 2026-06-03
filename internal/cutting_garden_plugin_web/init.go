package cutting_garden_plugin_web

import (
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
)

func init() {
	p := Plugin{}
	// Scheme registration keeps `web:` resolvable for capture/diff arg
	// classification; the protocol registrations (keyed by the "web"
	// receipt kind) handle the RFC 0002 receipts capture writes.
	cutting_garden_plugins.MustRegisterCapture(p)
	cutting_garden_plugins.MustRegisterDiff(p)
	cutting_garden_plugins.MustRegisterProtocolRestore(p)
	cutting_garden_plugins.MustRegisterProtocolDiff(p)
}
