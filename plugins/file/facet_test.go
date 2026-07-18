package cutting_garden_plugin_file

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// --- Declaration pins -------------------------------------------------

// TestDescribeFacets_DeclaresFileDimensions pins the four declared
// dimensions and their kinds against the file leaf type.
func TestDescribeFacets_DeclaresFileDimensions(t *testing.T) {
	var dims map[string]cutting_garden_plugins.FacetKind
	for _, ntf := range (Plugin{}).DescribeFacets() {
		if ntf.Tag != typeFile {
			continue
		}
		dims = map[string]cutting_garden_plugins.FacetKind{}
		for _, d := range ntf.Dimensions {
			dims[d.Key] = d.Kind
		}
	}
	if dims == nil {
		t.Fatalf("no facet dimensions declared for %q", typeFile)
	}
	if dims[facetExtension] != cutting_garden_plugins.FacetCategorical {
		t.Errorf("extension kind = %q, want categorical", dims[facetExtension])
	}
	if dims[facetSizeBand] != cutting_garden_plugins.FacetNumericBucket {
		t.Errorf("size_band kind = %q, want numeric-bucket", dims[facetSizeBand])
	}
	if dims[facetMonth] != cutting_garden_plugins.FacetNumericBucket {
		t.Errorf("month kind = %q, want numeric-bucket", dims[facetMonth])
	}
	if dims[facetAgeBand] != cutting_garden_plugins.FacetNumericBucket {
		t.Errorf("age_band kind = %q, want numeric-bucket", dims[facetAgeBand])
	}
}

// TestDescribeFacets_ClosedDomains pins which dimensions declare a CLOSED
// domain (RFC 0012 §2): size_band and age_band are closed (bounded band
// sets known up front); extension and month are open (discovered).
func TestDescribeFacets_ClosedDomains(t *testing.T) {
	var dims map[string]cutting_garden_plugins.FacetDimension
	for _, ntf := range (Plugin{}).DescribeFacets() {
		if ntf.Tag != typeFile {
			continue
		}
		dims = map[string]cutting_garden_plugins.FacetDimension{}
		for _, d := range ntf.Dimensions {
			dims[d.Key] = d
		}
	}
	if dims == nil {
		t.Fatal("no facet dimensions declared")
	}
	if dims[facetExtension].Values != nil {
		t.Errorf("extension declares a closed domain, want open (nil Values)")
	}
	if dims[facetMonth].Values != nil {
		t.Errorf("month declares a closed domain, want open (nil Values)")
	}
	if dims[facetSizeBand].Values == nil {
		t.Errorf("size_band declares an open domain, want closed")
	}
	if dims[facetAgeBand].Values == nil {
		t.Errorf("age_band declares an open domain, want closed")
	}
}

// TestAgeBandDeclaration pins the RFC 0012 §11.3 volatile-dimension
// obligations: nonzero RevalidateAfter, a closed domain covering every
// bucket, and declaration Orders consistent with the bucketing map.
func TestAgeBandDeclaration(t *testing.T) {
	var dim *cutting_garden_plugins.FacetDimension
	for _, ntf := range (Plugin{}).DescribeFacets() {
		for i := range ntf.Dimensions {
			if ntf.Dimensions[i].Key == facetAgeBand {
				dim = &ntf.Dimensions[i]
			}
		}
	}
	if dim == nil {
		t.Fatal("age_band not declared")
	}
	if dim.RevalidateAfter != ageBandRevalidateAfter {
		t.Errorf("RevalidateAfter = %v, want %v", dim.RevalidateAfter, ageBandRevalidateAfter)
	}
	if len(dim.Values) != len(ageBandOrder) {
		t.Fatalf("closed domain has %d values, want %d", len(dim.Values), len(ageBandOrder))
	}
	for _, v := range dim.Values {
		if want, ok := ageBandOrder[v.Key]; !ok || v.Order != want {
			t.Errorf("declared %q order %d inconsistent with ageBandOrder (%d, declared=%t)",
				v.Key, v.Order, want, ok)
		}
	}
}

// TestSizeBandDeclaration mirrors TestAgeBandDeclaration for the pure
// (non-volatile) closed dimension: RevalidateAfter stays zero and the
// declared Values match sizeBandOrder exactly.
func TestSizeBandDeclaration(t *testing.T) {
	var dim *cutting_garden_plugins.FacetDimension
	for _, ntf := range (Plugin{}).DescribeFacets() {
		for i := range ntf.Dimensions {
			if ntf.Dimensions[i].Key == facetSizeBand {
				dim = &ntf.Dimensions[i]
			}
		}
	}
	if dim == nil {
		t.Fatal("size_band not declared")
	}
	if dim.RevalidateAfter != 0 {
		t.Errorf("RevalidateAfter = %v, want 0 (size_band is pure)", dim.RevalidateAfter)
	}
	if len(dim.Values) != len(sizeBandOrder) {
		t.Fatalf("closed domain has %d values, want %d", len(dim.Values), len(sizeBandOrder))
	}
	for _, v := range dim.Values {
		if want, ok := sizeBandOrder[v.Key]; !ok || v.Order != want {
			t.Errorf("declared %q order %d inconsistent with sizeBandOrder (%d, declared=%t)",
				v.Key, v.Order, want, ok)
		}
	}
}

// --- Bucketing table tests ---------------------------------------------

func TestExtensionOf(t *testing.T) {
	cases := []struct {
		name   string
		want   string
		wantOK bool
	}{
		{"report.TXT", "txt", true},
		{"archive.tar.gz", "gz", true},
		{"README", "", false},
		{".bashrc", "", false},
		{"noext.", "", false},
		{"a.B", "b", true},
	}
	for _, c := range cases {
		got, ok := extensionOf(c.name)
		if got != c.want || ok != c.wantOK {
			t.Errorf("extensionOf(%q) = (%q, %t), want (%q, %t)",
				c.name, got, ok, c.want, c.wantOK)
		}
	}
}

func TestSizeBandOf(t *testing.T) {
	cases := []struct {
		size int64
		want string
	}{
		{0, sizeBandTiny},
		{4*kib - 1, sizeBandTiny},
		{4 * kib, sizeBandSmall},
		{1*mib - 1, sizeBandSmall},
		{1 * mib, sizeBandLarge},
		{100*mib - 1, sizeBandLarge},
		{100 * mib, sizeBandHuge},
		{10 * 1024 * mib, sizeBandHuge},
	}
	for _, c := range cases {
		key, order := sizeBandOf(c.size)
		if key != c.want {
			t.Errorf("sizeBandOf(%d) = %q, want %q", c.size, key, c.want)
			continue
		}
		if order != sizeBandOrder[key] {
			t.Errorf("sizeBandOf(%d) order = %d, want %d", c.size, order, sizeBandOrder[key])
		}
	}
}

func TestMonthOf(t *testing.T) {
	cases := []struct {
		in    time.Time
		key   string
		order int64
	}{
		{time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC), "2026-07", 202607},
		{time.Date(2025, 12, 31, 23, 59, 0, 0, time.UTC), "2025-12", 202512},
		{time.Time{}, "", 0},
	}
	for _, c := range cases {
		key, order := monthOf(c.in)
		if key != c.key || order != c.order {
			t.Errorf("monthOf(%v) = (%q, %d), want (%q, %d)", c.in, key, order, c.key, c.order)
		}
	}
}

// TestAgeBandOf pins the quantized, host-local-anchored bucketing: days
// are computed by day-start subtraction (crossing DST as 23/25h days,
// rounded back to whole days), and the domain totally partitions time
// (a future mtime clamps to "today" rather than falling outside the
// closed domain).
func TestAgeBandOf(t *testing.T) {
	now := time.Date(2026, 7, 18, 15, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		mod  time.Time
		want string
	}{
		{"same day", time.Date(2026, 7, 18, 3, 0, 0, 0, time.UTC), ageBandToday},
		{"today late", time.Date(2026, 7, 18, 23, 59, 0, 0, time.UTC), ageBandToday},
		{"future clamps to today", time.Date(2026, 7, 19, 1, 0, 0, 0, time.UTC), ageBandToday},
		{"yesterday", time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC), ageBandThisWeek},
		{"6 days ago: week edge", time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC), ageBandThisWeek},
		{"7 days ago: month begins", time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC), ageBandThisMonth},
		{"29 days ago: month edge", time.Date(2026, 6, 19, 0, 0, 0, 0, time.UTC), ageBandThisMonth},
		{"30 days ago: older", time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC), ageBandOlder},
		{"long ago", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), ageBandOlder},
		{"zero time", time.Time{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			key, order := ageBandOf(c.mod, now)
			if key != c.want {
				t.Errorf("ageBandOf(%v, %v) = %q, want %q", c.mod, now, key, c.want)
				return
			}
			if key != "" && order != ageBandOrder[key] {
				t.Errorf("ageBandOf order = %d, want %d", order, ageBandOrder[key])
			}
		})
	}
}

// --- Node.Facets / injected-clock end to end ----------------------------

// TestFileFacets_InjectedClock drives fileFacets through the package-level
// ageBandNow hook, pinning the age_band value against a controlled "now"
// without depending on wall-clock time (mirrors caldav's dueBandNow
// pattern).
func TestFileFacets_InjectedClock(t *testing.T) {
	prev := ageBandNow
	ageBandNow = func() time.Time { return time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { ageBandNow = prev })

	dir := t.TempDir()
	p := filepath.Join(dir, "old.log")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(p, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	facets := fileFacets(info)
	if len(facets[facetAgeBand]) != 1 || facets[facetAgeBand][0].Key != ageBandOlder {
		t.Errorf("age_band facet = %+v, want [older]", facets[facetAgeBand])
	}
	if len(facets[facetExtension]) != 1 || facets[facetExtension][0].Key != "log" {
		t.Errorf("extension facet = %+v, want [log]", facets[facetExtension])
	}
}

// --- FacetCounts: aggregation, zeros presence, cap/Complete -------------

func TestFacetCounts_NilNodeErrors(t *testing.T) {
	if _, _, err := (Plugin{}).FacetCounts(context.Background(), nil, nil); err == nil {
		t.Fatal("FacetCounts(nil) must error")
	}
}

// TestFacetCounts_AggregatesAcrossFiles pins the one-shot walk end to end:
// counts fold across every regular file in the subtree, directories
// contribute nothing, and the result is Complete under the cap.
func TestFacetCounts_AggregatesAcrossFiles(t *testing.T) {
	prev := ageBandNow
	ageBandNow = func() time.Time { return time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { ageBandNow = prev })

	dir := t.TempDir()
	mustWriteSized(t, filepath.Join(dir, "a.txt"), 10, time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC))
	mustWriteSized(t, filepath.Join(dir, "b.txt"), 2*mib, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteSized(t, filepath.Join(dir, "sub", "c.bin"), 0, time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC))

	result, ok, err := (Plugin{}).FacetCounts(
		context.Background(), &url.URL{Scheme: "file", Path: dir}, nil,
	)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}
	if !result.Complete {
		t.Error("Complete = false, want true (well under facetWalkCap)")
	}

	assertFacetCount(t, result.Summary, facetExtension, "txt", 2)
	assertFacetCount(t, result.Summary, facetExtension, "bin", 1)
	assertFacetCount(t, result.Summary, facetSizeBand, sizeBandTiny, 2)
	assertFacetCount(t, result.Summary, facetSizeBand, sizeBandLarge, 1)
}

// TestFacetCounts_FilterNarrowsSummary pins the conjunctive filter path
// (RFC 0012 §6): only files matching every predicate are lifted.
func TestFacetCounts_FilterNarrowsSummary(t *testing.T) {
	dir := t.TempDir()
	mustWriteSized(t, filepath.Join(dir, "a.txt"), 10, time.Now())
	mustWriteSized(t, filepath.Join(dir, "b.log"), 10, time.Now())

	filter := cutting_garden_plugins.FacetFilter{
		{Dimension: facetExtension, Value: "txt"},
	}
	result, ok, err := (Plugin{}).FacetCounts(
		context.Background(), &url.URL{Scheme: "file", Path: dir}, filter,
	)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}
	assertFacetCount(t, result.Summary, facetExtension, "txt", 1)
	if _, present := result.Summary[facetExtension]["log"]; present {
		t.Error("log should be excluded under the extension=txt filter")
	}
}

// TestFacetCounts_AgeBandZerosPresence drives the RFC 0012 §11.3 emission
// rule end to end: every declared age_band bucket is present (informative
// zeros) whenever the subtree contains files, even when no file currently
// occupies a given bucket.
func TestFacetCounts_AgeBandZerosPresence(t *testing.T) {
	prev := ageBandNow
	ageBandNow = func() time.Time { return time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { ageBandNow = prev })

	dir := t.TempDir()
	// Every file lands in "today"; this-week/this-month/older stay empty.
	mustWriteSized(t, filepath.Join(dir, "a.txt"), 1, time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC))

	result, ok, err := (Plugin{}).FacetCounts(
		context.Background(), &url.URL{Scheme: "file", Path: dir}, nil,
	)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}

	assertFacetCount(t, result.Summary, facetAgeBand, ageBandToday, 1)
	// Informative zeros: present, empty.
	assertFacetCount(t, result.Summary, facetAgeBand, ageBandThisWeek, 0)
	assertFacetCount(t, result.Summary, facetAgeBand, ageBandThisMonth, 0)
	assertFacetCount(t, result.Summary, facetAgeBand, ageBandOlder, 0)
}

// TestFacetCounts_EmptyDirNoAgeBand pins the other half of the emission
// rule: a file-free subtree omits the volatile dimension entirely.
func TestFacetCounts_EmptyDirNoAgeBand(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "empty-sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, ok, err := (Plugin{}).FacetCounts(
		context.Background(), &url.URL{Scheme: "file", Path: dir}, nil,
	)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}
	if _, present := result.Summary[facetAgeBand]; present {
		t.Errorf("file-free summary carries age_band: %+v", result.Summary[facetAgeBand])
	}
}

// TestFacetCounts_LeafNode pins the single-file (leaf) node case: no walk
// is performed, the file's own lift is the whole summary, and Complete is
// always true (a single node can never hit the cap).
func TestFacetCounts_LeafNode(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "solo.txt")
	if err := os.WriteFile(f, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, ok, err := (Plugin{}).FacetCounts(
		context.Background(), &url.URL{Scheme: "file", Path: f}, nil,
	)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}
	if !result.Complete {
		t.Error("Complete = false for a single leaf node, want true")
	}
	assertFacetCount(t, result.Summary, facetExtension, "txt", 1)
}

// TestFacetCounts_SymlinkNodeNeverDescends pins the symlink-consistency
// fix: a symlinked directory passed as node must not be walked (mirrors
// ListRoots's leaf treatment), even though filepath.WalkDir would normally
// follow a symlink root.
func TestFacetCounts_SymlinkNodeNeverDescends(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "realdir")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "inside.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linkdir")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	result, ok, err := (Plugin{}).FacetCounts(
		context.Background(), &url.URL{Scheme: "file", Path: link}, nil,
	)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}
	if len(result.Summary) != 0 {
		t.Errorf("symlink node summary = %+v, want empty (not descended)", result.Summary)
	}
}

// TestFacetCounts_WalkCapMarksPartial pins the RFC 0012 §8 fold bound: a
// tree deeper than facetWalkCap returns Complete == false rather than
// blocking to walk it exhaustively or silently under-reporting as
// complete.
func TestFacetCounts_WalkCapMarksPartial(t *testing.T) {
	dir := t.TempDir()
	// facetWalkCap+a few files, cheap to create (empty files).
	n := facetWalkCap + 50
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, "f"+itoa(i)+".txt")
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatalf("write fixture %d: %v", i, err)
		}
	}

	result, ok, err := (Plugin{}).FacetCounts(
		context.Background(), &url.URL{Scheme: "file", Path: dir}, nil,
	)
	if err != nil || !ok {
		t.Fatalf("FacetCounts: ok=%v err=%v", ok, err)
	}
	if result.Complete {
		t.Error("Complete = true over a tree larger than facetWalkCap, want false")
	}
}

// itoa avoids importing strconv into the test file twice for one call site.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

func mustWriteSized(t *testing.T, path string, size int, modTime time.Time) {
	t.Helper()
	data := make([]byte, size)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
}

func assertFacetCount(
	t *testing.T,
	summary cutting_garden_plugins.FacetSummary,
	dimension, key string,
	want int64,
) {
	t.Helper()
	hist, ok := summary[dimension]
	if !ok {
		t.Errorf("dimension %q absent from summary", dimension)
		return
	}
	if got := hist[key]; got != want {
		t.Errorf("summary[%s][%s] = %d, want %d", dimension, key, got, want)
	}
}
