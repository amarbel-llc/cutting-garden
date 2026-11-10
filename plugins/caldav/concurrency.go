package caldav

import "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"

var _ cutting_garden_plugins.ConcurrentReader = Plugin{}

// MaxConcurrentReads lets a consumer overlap up to 4 of this plugin's reads
// (cutting-garden#313). The read paths are safe under concurrent callers: each
// read builds its own client, the discovery and root-label caches are locked,
// and concurrent listings of one calendar-home share a single discovery
// (discoverCalendarsThisCall's flight), so overlapping reads cost one
// discovery PROPFIND per home per call, as a sequential walk does. The bound
// is small because the source is one CalDAV server, typically a hosted one.
func (Plugin) MaxConcurrentReads() int { return 4 }
