package cutting_garden_plugins

// Prefix-granularity support for FacetDate dimensions (cutting-garden#230).
// The framework owns ONLY string prefix truncation over ISO-day bucket keys
// ("2026" ⊂ "2026-08" ⊂ "2026-08-15"); the plugin owns the values and the
// writes. Any plugin declaring a date-kind dimension gets granularity
// grouping and prefix filtering from these helpers.

// DateGranularity is a bucket coarseness for a FacetDate dimension: the
// --group-by suffix spelling ("date_due:month") and the config default.
type DateGranularity string

const (
	GranularityYear  DateGranularity = "year"  // "2026"
	GranularityMonth DateGranularity = "month" // "2026-08"
	GranularityDay   DateGranularity = "day"   // "2026-08-15" (the identity)
)

// granularityLen is each granularity's key length — the whole hierarchy.
var granularityLen = map[DateGranularity]int{
	GranularityYear:  len("2026"),
	GranularityMonth: len("2026-08"),
	GranularityDay:   len("2026-08-15"),
}

// ParseDateGranularity resolves a granularity spelling. Strict lowercase —
// a typo rejects loudly at the CLI/config layer.
func ParseDateGranularity(s string) (DateGranularity, bool) {
	g := DateGranularity(s)
	_, ok := granularityLen[g]
	return g, ok
}

// TruncateDateKey coarsens a date bucket key to the granularity by prefix
// truncation. A key already at or coarser than the granularity is unchanged.
func TruncateDateKey(key string, g DateGranularity) string {
	n := granularityLen[g]
	if n == 0 || len(key) <= n {
		return key
	}
	return key[:n]
}

// DateBucketMatches reports whether a bucket key falls inside a (coarser or
// equal) date bucket: key equals the bucket, or extends it at a `-` boundary —
// "2026-08" contains "2026-08-15" but never a hypothetical "2026-081". This is
// the single hierarchy-containment definition shared by FacetPredicate.matches
// and the trellis evaluator's date-kind `=` predicate (cutting-garden#230), so
// the two filter surfaces cannot drift. Callers gate on ParseDateBucket first:
// a non-shape bucket value means exact semantics, not containment.
func DateBucketMatches(key, bucket string) bool {
	if key == bucket {
		return true
	}
	return len(key) > len(bucket) &&
		key[:len(bucket)] == bucket && key[len(bucket)] == '-'
}

// ParseDateBucket classifies a value by its date-bucket shape — YYYY,
// YYYY-MM, or YYYY-MM-DD with in-range month/day — reporting the granularity
// it addresses. ok == false for any other shape: the loud-rejection gate for
// prefix filters and coarse bucket moves.
func ParseDateBucket(v string) (DateGranularity, bool) {
	switch len(v) {
	case granularityLen[GranularityYear]:
		if allDateDigits(v) {
			return GranularityYear, true
		}
	case granularityLen[GranularityMonth]:
		if allDateDigits(v[:4]) && v[4] == '-' && inRange(v[5:7], "01", "12") {
			return GranularityMonth, true
		}
	case granularityLen[GranularityDay]:
		if allDateDigits(v[:4]) && v[4] == '-' &&
			inRange(v[5:7], "01", "12") && v[7] == '-' && inRange(v[8:10], "01", "31") {
			return GranularityDay, true
		}
	}
	return "", false
}

func allDateDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// inRange reports lo <= s <= hi for equal-length digit strings.
func inRange(s, lo, hi string) bool {
	return allDateDigits(s) && s >= lo && s <= hi
}
