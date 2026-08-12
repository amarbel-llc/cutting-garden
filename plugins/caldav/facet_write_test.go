package caldav

import (
	"testing"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// TestDescribeFacetWrites_ConsistentAndDeclared pins that caldav's write
// mappings validate against its OWN read-side facet schema (every mapped key is
// a declared dimension) and declare the expected writability: the
// reschedule-by-move date buckets, status, and priority are write:one through a
// field, and the derived / identity dimensions are explicitly read-only.
func TestDescribeFacetWrites_ConsistentAndDeclared(t *testing.T) {
	p := Plugin{}
	reads := p.DescribeFacets()
	writes := p.DescribeFacetWrites()

	if err := cutting_garden_plugins.ValidateFacetWrites(reads, writes); err != nil {
		t.Fatalf("caldav write mappings inconsistent with read facets: %v", err)
	}

	mode := map[string]cutting_garden_plugins.FacetWriteMode{}
	field := map[string]string{}
	// Merge across the three per-component write entries; the shared
	// dimensions (year/month/status write:one, component/due_band/timezone
	// read-only) declare consistent modes wherever they appear.
	for _, nt := range writes {
		for _, w := range nt.Writes {
			mode[w.DimensionKey] = w.Mode
			field[w.DimensionKey] = w.Field
		}
	}

	for _, k := range []string{facetYear, facetMonth, facetStatus, facetPriority} {
		if mode[k] != cutting_garden_plugins.FacetWriteOne {
			t.Errorf("dimension %q: mode = %q, want one", k, mode[k])
		}
		if field[k] == "" {
			t.Errorf("dimension %q: writable but declares no field", k)
		}
	}
	for _, k := range []string{facetComponent, facetDueBand, facetTimezone} {
		if mode[k] != cutting_garden_plugins.FacetWriteNone {
			t.Errorf("dimension %q: mode = %q, want none", k, mode[k])
		}
	}
}
