package cutting_garden_plugin_file

import (
	"context"
	"io/fs"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Facet dimension keys declared for the file leaf (RFC 0012). All four draw
// from os.FileInfo — the SAME stat data ListRoots already fetches per entry
// (via fs.DirEntry.Info()) to classify container vs leaf — so populating
// Node.Facets there costs nothing beyond what traversal already pays (§1's
// "same enumeration" rule).
const (
	facetExtension = "extension" // lowercased, no leading dot; absent for none
	facetSizeBand  = "size_band" // closed domain: tiny/small/large/huge
	facetMonth     = "month"     // the YYYY-MM bucket of ModTime, pure
	facetAgeBand   = "age_band"  // VOLATILE: ModTime vs today (§11.3)
)

// The size_band closed domain, in ascending order (small to large — no
// urgency semantics, unlike caldav's due_band).
const (
	sizeBandTiny  = "tiny"  // < 4 KiB
	sizeBandSmall = "small" // < 1 MiB
	sizeBandLarge = "large" // < 100 MiB
	sizeBandHuge  = "huge"  // >= 100 MiB
)

const (
	kib = 1024
	mib = 1024 * kib
)

// sizeBandOrder renders smallest-first (ascending), the inverse convention
// from caldav's urgency-first due_band — size has no urgency, just
// magnitude.
var sizeBandOrder = map[string]int64{
	sizeBandTiny:  1,
	sizeBandSmall: 2,
	sizeBandLarge: 3,
	sizeBandHuge:  4,
}

// The age_band closed domain: a total partition of time relative to the
// current host-local day (files carry no per-object zone the way a caldav
// TZID does, so host-local is the only anchor — RFC 0012 §11.3 "Time
// anchoring" fallback). Order renders recency-first, mirroring due_band's
// urgency-first convention.
const (
	ageBandToday     = "today"      // modified today (or a future mtime, clamped)
	ageBandThisWeek  = "this-week"  // within the past 6 days
	ageBandThisMonth = "this-month" // within the past 29 days
	ageBandOlder     = "older"      // beyond
)

var ageBandOrder = map[string]int64{
	ageBandToday:     4,
	ageBandThisWeek:  3,
	ageBandThisMonth: 2,
	ageBandOlder:     1,
}

// ageBandRevalidateAfter is the volatile window (RFC 0012 §11.3): a file
// crosses age_band buckets at most this + the refresher interval late.
const ageBandRevalidateAfter = 15 * time.Minute

// ageBandNow is the evaluation clock, injectable for tests. Bucketing
// quantizes it to the host-local day start (§11.3 evaluation-instant
// quantization), so summaries memoized at different instants within one day
// agree exactly.
var ageBandNow = time.Now

// facetWalkCap bounds FacetCounts's one-shot subtree walk (RFC 0012 §8's
// REQUIRED fold bound): a generous number for an ordinary directory tree,
// small enough that a pathological huge tree returns promptly with
// Complete == false rather than blocking indefinitely. Counts every
// filepath.WalkDir visit (directories and files alike), not just the
// leaves that contribute facets.
const facetWalkCap = 50_000

var (
	_ cutting_garden_plugins.FacetDescriber = (*Plugin)(nil)
	_ cutting_garden_plugins.FacetCounter   = (*Plugin)(nil)
)

// DescribeFacets declares the facet dimensions of a file leaf — the
// self-describing schema the mcp `describe_node_types` tool surfaces.
// extension and month are OPEN (their value sets are discovered, not fixed
// up front); size_band and age_band are CLOSED (RFC 0012 §2), enabling
// informative zeros.
func (Plugin) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	return []cutting_garden_plugins.NodeTypeFacets{
		{
			Tag: typeFile,
			Dimensions: []cutting_garden_plugins.FacetDimension{
				{
					Key:   facetExtension,
					Label: "Extension",
					Kind:  cutting_garden_plugins.FacetCategorical,
				},
				{
					Key:   facetSizeBand,
					Label: "Size",
					Kind:  cutting_garden_plugins.FacetNumericBucket,
					Values: []cutting_garden_plugins.FacetValue{
						{Key: sizeBandTiny, Order: sizeBandOrder[sizeBandTiny]},
						{Key: sizeBandSmall, Order: sizeBandOrder[sizeBandSmall]},
						{Key: sizeBandLarge, Order: sizeBandOrder[sizeBandLarge]},
						{Key: sizeBandHuge, Order: sizeBandOrder[sizeBandHuge]},
					},
				},
				{
					Key:   facetMonth,
					Label: "Modified month",
					Kind:  cutting_garden_plugins.FacetNumericBucket,
				},
				{
					// VOLATILE (RFC 0012 §11.3): bucketing is a function of
					// (mtime, today). Host-local anchored — files carry no
					// per-object zone to anchor in, unlike a caldav TZID.
					Key:   facetAgeBand,
					Label: "Age",
					Kind:  cutting_garden_plugins.FacetNumericBucket,
					Values: []cutting_garden_plugins.FacetValue{
						{Key: ageBandToday, Order: ageBandOrder[ageBandToday]},
						{Key: ageBandThisWeek, Order: ageBandOrder[ageBandThisWeek]},
						{Key: ageBandThisMonth, Order: ageBandOrder[ageBandThisMonth]},
						{Key: ageBandOlder, Order: ageBandOrder[ageBandOlder]},
					},
					RevalidateAfter: ageBandRevalidateAfter,
				},
			},
		},
	}
}

// fileFacets projects one regular file's already-fetched os.FileInfo into
// RFC 0012 FacetValues. Returns nil (contributes nothing) only in the
// degenerate case where every dimension is empty (never happens today,
// since size_band and age_band are total partitions — kept for symmetry
// with caldav/jira's objectFacets/issueFacets shape).
func fileFacets(info os.FileInfo) map[string][]cutting_garden_plugins.FacetValue {
	facets := map[string][]cutting_garden_plugins.FacetValue{}

	if ext, ok := extensionOf(info.Name()); ok {
		facets[facetExtension] = []cutting_garden_plugins.FacetValue{{Key: ext}}
	}

	if key, order := sizeBandOf(info.Size()); key != "" {
		facets[facetSizeBand] = []cutting_garden_plugins.FacetValue{
			{Key: key, Order: order},
		}
	}

	if key, order := monthOf(info.ModTime()); key != "" {
		facets[facetMonth] = []cutting_garden_plugins.FacetValue{
			{Key: key, Order: order},
		}
	}

	if key, order := ageBandOf(info.ModTime(), ageBandNow()); key != "" {
		facets[facetAgeBand] = []cutting_garden_plugins.FacetValue{
			{Key: key, Order: order},
		}
	}

	if len(facets) == 0 {
		return nil
	}
	return facets
}

// extensionOf lowercases and strips the leading dot from name's extension.
// A leading dot with no further dot (a dotfile like ".bashrc") is NOT an
// extension marker — trimmed before delegating to filepath.Ext so
// ".bashrc" reports no extension while ".tar.gz" reports "gz" (the last
// segment only, matching filepath.Ext's single-suffix semantics — this
// does not attempt compound-extension detection). Returns ok=false when
// there is no extension.
func extensionOf(name string) (ext string, ok bool) {
	trimmed := strings.TrimPrefix(name, ".")
	e := filepath.Ext(trimmed)
	if e == "" || e == "." {
		// e == "." is a bare trailing dot with nothing after it
		// ("noext.") — not a meaningful extension.
		return "", false
	}
	return strings.ToLower(strings.TrimPrefix(e, ".")), true
}

// sizeBandOf buckets a byte count into the closed size_band domain. Total
// partition of [0, +inf) — always returns a non-empty key.
func sizeBandOf(size int64) (key string, order int64) {
	switch {
	case size < 4*kib:
		key = sizeBandTiny
	case size < 1*mib:
		key = sizeBandSmall
	case size < 100*mib:
		key = sizeBandLarge
	default:
		key = sizeBandHuge
	}
	return key, sizeBandOrder[key]
}

// monthOf extracts the YYYY-MM bucket of t (e.g. 2026-07-18 → key "2026-07",
// order 202607). Empty key for the zero time (an unset ModTime — some
// virtual/synthetic filesystems report one).
func monthOf(t time.Time) (key string, order int64) {
	if t.IsZero() {
		return "", 0
	}
	key = t.Format("2006-01")
	order, _ = strconv.ParseInt(t.Format("200601"), 10, 64)
	return key, order
}

// ageBandOf buckets modTime against the current day in HOST-LOCAL time
// (now.Location() — the only anchor available; a file carries no per-object
// zone the way a caldav TZID does), quantized to day start (RFC 0012 §11.3),
// so summaries computed at different times within one day agree exactly.
// Day-start subtraction crosses DST boundaries as 23/25h days; rounding the
// hour count recovers the calendar-day distance (mirrors caldav's
// dueBandOf). A future-dated mtime (clock skew, a restored file) clamps to
// "today" rather than producing a band outside the closed domain. Empty key
// for the zero time.
func ageBandOf(modTime, now time.Time) (key string, order int64) {
	if modTime.IsZero() {
		return "", 0
	}
	loc := now.Location()
	local := now.In(loc)
	today := time.Date(
		local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc,
	)
	mod := modTime.In(loc)
	modDay := time.Date(
		mod.Year(), mod.Month(), mod.Day(), 0, 0, 0, 0, loc,
	)

	days := int(math.Round(today.Sub(modDay).Hours() / 24))
	switch {
	case days <= 0:
		key = ageBandToday
	case days <= 6:
		key = ageBandThisWeek
	case days <= 29:
		key = ageBandThisMonth
	default:
		key = ageBandOlder
	}
	return key, ageBandOrder[key]
}

// ensureAgeBandPresence implements the RFC 0012 §11.3 emission rule:
// whenever the summarized set contains any regular file, the volatile
// age_band dimension is present with informative zeros — the memoization
// layer's expiry trigger stays correct even when a bucket is currently
// empty (an empty bucket can fill purely by time passing; a file-free
// subtree can only gain a file via a data change a change token would
// catch).
func ensureAgeBandPresence(summary cutting_garden_plugins.FacetSummary, sawFile bool) {
	if !sawFile {
		return
	}
	hist := summary[facetAgeBand]
	if hist == nil {
		hist = cutting_garden_plugins.FacetHistogram{}
		summary[facetAgeBand] = hist
	}
	for key := range ageBandOrder {
		if _, ok := hist[key]; !ok {
			hist[key] = 0
		}
	}
}

// liftFacets folds one file's facet values into summary: +1 per
// (dimension, value key). The per-node "lift" of RFC 0012 §3. A
// package-local copy, like caldav's and jira's — the three plugins share
// no common dependency to hang one copy off (jira's facet.go comment).
func liftFacets(
	summary cutting_garden_plugins.FacetSummary,
	facets map[string][]cutting_garden_plugins.FacetValue,
) {
	for dim, values := range facets {
		hist := summary[dim]
		if hist == nil {
			hist = cutting_garden_plugins.FacetHistogram{}
			summary[dim] = hist
		}
		for _, v := range values {
			hist[v.Key]++
		}
	}
}

// FacetCounts summarizes a directory's (or a single file's) facets in one
// shot via filepath.WalkDir, bounded by facetWalkCap (RFC 0012 §8) — the
// framework-fold fallback (RFC 0012 §4.2) would otherwise need to descend
// this same tree itself via repeated ListRoots calls, which is strictly
// more work for a plugin that can walk it directly in one pass. node MUST
// be non-nil.
//
// node is Lstat'd (never Stat'd): filepath.WalkDir follows a symlink ROOT
// even though it never follows symlinks found WITHIN the walk, which would
// silently break the "never follow a symlink to a directory" posture
// ListRoots documents. A symlink node (to a file or a directory) is
// therefore handled directly, without ever calling WalkDir, exactly
// mirroring ListRoots's leaf treatment of the same case.
//
// Per-entry stat failures (a TOCTOU race, a permission edge case) are
// skipped rather than aborting the whole summary — the same "log nothing,
// just omit" posture ListRoots takes (RFC 0014's read-only, best-effort
// enumeration contract); only a failure to stat the ROOT node itself is
// fatal, since that indicates the node argument itself is invalid rather
// than a transient race deeper in the tree.
func (Plugin) FacetCounts(
	ctx context.Context,
	node *url.URL,
	filter cutting_garden_plugins.FacetFilter,
) (cutting_garden_plugins.FacetResult, bool, error) {
	if node == nil {
		return cutting_garden_plugins.FacetResult{}, false, errors.ErrorWithStackf(
			"file plugin: FacetCounts requires a node URI",
		)
	}
	path, err := pathFromURL(node)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, errors.Wrapf(
			err, "file plugin: resolve %q", path,
		)
	}
	rootInfo, err := os.Lstat(abs)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, errors.Wrapf(
			err, "file plugin: stat %q", abs,
		)
	}

	summary := cutting_garden_plugins.FacetSummary{}
	var sawFile bool

	if !rootInfo.IsDir() {
		// A leaf node (a regular file, or a symlink of either kind — never
		// descended, matching ListRoots): its own lift is the whole
		// summary, no walk.
		if rootInfo.Mode().IsRegular() {
			if facets := fileFacets(rootInfo); facets != nil {
				sawFile = true
				if filter.Matches(facets) {
					liftFacets(summary, facets)
				}
			}
		}
		ensureAgeBandPresence(summary, sawFile)
		return cutting_garden_plugins.FacetResult{
			Summary: summary, Complete: true,
		}, true, nil
	}

	var (
		visited  int
		complete = true
	)

	walkErr := filepath.WalkDir(abs, func(p string, d fs.DirEntry, walkErr error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if walkErr != nil {
			// Unreadable entry: omit and continue, mirroring ListRoots's
			// "skip entries that can't be stat'd" posture.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		visited++
		if visited > facetWalkCap {
			complete = false
			return filepath.SkipAll
		}

		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil // vanished between walk and stat; omit
		}
		if !info.Mode().IsRegular() {
			return nil // symlinks and other special files contribute nothing
		}

		facets := fileFacets(info)
		if facets == nil {
			return nil
		}
		sawFile = true
		if !filter.Matches(facets) {
			return nil
		}
		liftFacets(summary, facets)
		return nil
	})
	if walkErr != nil {
		return cutting_garden_plugins.FacetResult{}, false, errors.Wrapf(
			walkErr, "file plugin: facet walk %q", abs,
		)
	}

	ensureAgeBandPresence(summary, sawFile)

	return cutting_garden_plugins.FacetResult{
		Summary:  summary,
		Complete: complete,
	}, true, nil
}
