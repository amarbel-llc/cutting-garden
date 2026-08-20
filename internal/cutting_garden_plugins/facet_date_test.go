package cutting_garden_plugins

import "testing"

// TruncateDateKey coarsens an ISO-day bucket key by pure prefix truncation —
// the framework's whole share of the date hierarchy (no calendar knowledge).
func TestTruncateDateKey(t *testing.T) {
	cases := []struct {
		key  string
		g    DateGranularity
		want string
	}{
		{"2026-08-15", GranularityDay, "2026-08-15"},
		{"2026-08-15", GranularityMonth, "2026-08"},
		{"2026-08-15", GranularityYear, "2026"},
		{"2026-08", GranularityMonth, "2026-08"}, // already coarse: unchanged
		{"2026-08", GranularityYear, "2026"},
		{"2026", GranularityMonth, "2026"}, // coarser than asked: unchanged
	}
	for _, c := range cases {
		if got := TruncateDateKey(c.key, c.g); got != c.want {
			t.Errorf("TruncateDateKey(%q, %q) = %q, want %q", c.key, c.g, got, c.want)
		}
	}
}

// ParseDateBucket classifies a filter/bucket value by its validated shape —
// the granularity a prefix filter or coarse bucket move addresses.
func TestParseDateBucket(t *testing.T) {
	cases := []struct {
		in string
		g  DateGranularity
		ok bool
	}{
		{"2026", GranularityYear, true},
		{"2026-08", GranularityMonth, true},
		{"2026-08-15", GranularityDay, true},
		{"202608", "", false},
		{"2026-13", "", false},    // month out of range
		{"2026-08-32", "", false}, // day out of range
		{"2026-8", "", false},
		{"garbage", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		g, ok := ParseDateBucket(c.in)
		if g != c.g || ok != c.ok {
			t.Errorf("ParseDateBucket(%q) = (%q, %t), want (%q, %t)", c.in, g, ok, c.g, c.ok)
		}
	}
}

// ParseDateGranularity resolves the --group-by suffix / config spelling.
func TestParseDateGranularity(t *testing.T) {
	for _, s := range []string{"year", "month", "day"} {
		if _, ok := ParseDateGranularity(s); !ok {
			t.Errorf("ParseDateGranularity(%q): want ok", s)
		}
	}
	for _, s := range []string{"week", "", "MONTH", "days"} {
		if _, ok := ParseDateGranularity(s); ok {
			t.Errorf("ParseDateGranularity(%q): want !ok", s)
		}
	}
}
