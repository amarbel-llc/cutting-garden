package caldav

import (
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// The caldav plugin opts into concurrent reads at 4 (cutting-garden#313), once
// concurrent listings of one home share a discovery
// (TestDiscovery_ConcurrentListingsShareOne).
func TestConcurrentReader_OptedInAtFour(t *testing.T) {
	cr, ok := any(Plugin{}).(cutting_garden_plugins.ConcurrentReader)
	if !ok {
		t.Fatal("the caldav plugin does not implement ConcurrentReader")
	}
	if got := cr.MaxConcurrentReads(); got != 4 {
		t.Errorf("MaxConcurrentReads() = %d, want 4", got)
	}
}
