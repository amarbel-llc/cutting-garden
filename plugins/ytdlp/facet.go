package cutting_garden_plugin_ytdlp

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Facet dimension keys declared for the ytdlp video leaf. All four are
// drawn from fields the shared flat-playlist enumeration primitive
// already carries (flatplaylist.go) — no per-video fetch (RFC 0012 §1).
const (
	facetUploader     = "uploader"      // the channel/uploader name
	facetYear         = "year"          // the year bucket of upload_date, when present
	facetMonth        = "month"         // the YYYY-MM bucket of upload_date, when present
	facetDurationBand = "duration_band" // closed domain: short/medium/long
)

// The duration_band closed domain. Bucket edges are seconds; Order
// renders shortest-first.
const (
	durationBandShort  = "short"  // < 4 minutes
	durationBandMedium = "medium" // 4-20 minutes
	durationBandLong   = "long"   // >= 20 minutes
)

const (
	durationShortMaxSeconds  = 4 * 60
	durationMediumMaxSeconds = 20 * 60
)

var (
	_ cutting_garden_plugins.FacetDescriber = (*Plugin)(nil)
	_ cutting_garden_plugins.FacetCounter   = (*Plugin)(nil)
)

// DescribeFacets declares the ytdlp video leaf's facet dimensions — the
// self-describing schema the mcp `describe_node_types` tool surfaces.
func (Plugin) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	return []cutting_garden_plugins.NodeTypeFacets{
		{
			Tag: typeVideo,
			Dimensions: []cutting_garden_plugins.FacetDimension{
				{
					Key:   facetUploader,
					Label: "Uploader",
					Kind:  cutting_garden_plugins.FacetCategorical,
				},
				{
					// OPEN domain: upload_date is only approximated when
					// present at all (see flatPlaylistEntry's doc comment)
					// so a closed Values set would be misleading.
					Key:   facetYear,
					Label: "Year",
					Kind:  cutting_garden_plugins.FacetNumericBucket,
				},
				{
					Key:   facetMonth,
					Label: "Month",
					Kind:  cutting_garden_plugins.FacetNumericBucket,
				},
				{
					// CLOSED domain: the band set is fixed and pure (not a
					// function of "now" like caldav's due_band), so
					// RevalidateAfter stays zero.
					Key:   facetDurationBand,
					Label: "Duration",
					Kind:  cutting_garden_plugins.FacetNumericBucket,
					Values: []cutting_garden_plugins.FacetValue{
						{Key: durationBandShort, Order: 1},
						{Key: durationBandMedium, Order: 2},
						{Key: durationBandLong, Order: 3},
					},
				},
			},
		},
	}
}

// FacetCounts summarizes node's subtree in one shot via the SAME
// flat-playlist probe ListRoots uses, folding every entry's lift
// (RFC 0012 §3) into one summary. This is the honest fallback FDR 0014's
// RootLister-fold consumer would otherwise need: as of this writing there
// is no framework-fold implementation yet (`list --facets` requires a
// FacetCounter directly — see internal/list/list.go's runFacets), so
// without this method ytdlp's facets would be declared but never
// servable, exactly the trap caldav's and jira's FacetCounter avoid.
//
// Unlike CaptureRoot, FacetCounts does NOT apply the FDR 0004
// channelLimitParam/defaultChannelCaptureThreshold guardrail: summarizing
// is cheap (one probe already has every entry), and it is precisely the
// progressive-disclosure signal (FDR 0021) a user needs BEFORE deciding
// whether to raise the capture limit — capping it here would hide the
// very information the guardrail is supposed to inform.
//
// The result is always Complete: one flat-playlist probe returns every
// entry yt-dlp itself reports, with no framework-imposed cap.
func (Plugin) FacetCounts(
	ctx context.Context,
	node *url.URL,
	filter cutting_garden_plugins.FacetFilter,
) (cutting_garden_plugins.FacetResult, bool, error) {
	if node == nil {
		return cutting_garden_plugins.FacetResult{}, false, errors.ErrorWithStackf(
			"ytdlp plugin: FacetCounts requires a node URI",
		)
	}

	source, err := sourceURLFromArg(node)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, err
	}

	entries, err := probeFlatPlaylist(ctx, source)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, err
	}

	summary := cutting_garden_plugins.FacetSummary{}
	for _, e := range entries {
		facets := entryFacets(e)
		if !filter.Matches(facets) {
			continue
		}
		liftFacets(summary, facets)
	}

	return cutting_garden_plugins.FacetResult{Summary: summary, Complete: true}, true, nil
}

// entryFacets projects one flat-playlist entry's facet values — the
// values Node.Facets carries during ListRoots (the SAME enumeration,
// RFC 0012 §1) and what FacetCounts lifts. A field absent from the
// entry (upload_date/duration, see flatPlaylistEntry's doc comment)
// simply contributes nothing to its dimension rather than a synthetic
// zero value.
func entryFacets(e flatPlaylistEntry) map[string][]cutting_garden_plugins.FacetValue {
	facets := map[string][]cutting_garden_plugins.FacetValue{}

	if e.Uploader != "" {
		facets[facetUploader] = []cutting_garden_plugins.FacetValue{{Key: e.Uploader}}
	}
	if year := yearOf(e.UploadDate); year != "" {
		order, _ := strconv.ParseInt(year, 10, 64)
		facets[facetYear] = []cutting_garden_plugins.FacetValue{{Key: year, Order: order}}
	}
	if key, order := monthOf(e.UploadDate); key != "" {
		facets[facetMonth] = []cutting_garden_plugins.FacetValue{{Key: key, Order: order}}
	}
	if e.Duration != nil {
		if key, order := durationBandOf(*e.Duration); key != "" {
			facets[facetDurationBand] = []cutting_garden_plugins.FacetValue{{Key: key, Order: order}}
		}
	}

	if len(facets) == 0 {
		return nil
	}
	return facets
}

// durationBandOf buckets a duration in seconds into the closed
// short/medium/long domain. Empty key for a negative (malformed) value.
func durationBandOf(seconds float64) (key string, order int64) {
	switch {
	case seconds < 0:
		return "", 0
	case seconds < durationShortMaxSeconds:
		return durationBandShort, 1
	case seconds < durationMediumMaxSeconds:
		return durationBandMedium, 2
	default:
		return durationBandLong, 3
	}
}

// liftFacets folds one entry's facet values into summary: +1 per
// (dimension, value key) — the per-node "lift" of RFC 0012 §3.
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

// monthOf extracts the year-month bucket prefixing a yt-dlp
// upload_date (YYYYMMDD, e.g. "20260718" -> key "2026-07", order
// 202607). Copied from caldav's monthOf (plugins/caldav/facet.go) per
// FDR 0004's "copy caldav's yearOf/monthOf" guidance — duplicated
// rather than cross-imported since a plugin never depends on a sibling
// plugin. Empty key when date has fewer than 6 leading digits or an
// out-of-range month.
func monthOf(date string) (key string, order int64) {
	var digits strings.Builder
scan:
	for _, r := range date {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
			if digits.Len() == 6 {
				break scan
			}
		case r == '-':
			// tolerate a hyphenated date prefix
		default:
			break scan
		}
	}
	if digits.Len() < 6 {
		return "", 0
	}
	s := digits.String()
	month := s[4:6]
	if month < "01" || month > "12" {
		return "", 0
	}
	order, _ = strconv.ParseInt(s, 10, 64)
	return s[:4] + "-" + month, order
}

// yearOf extracts the four-digit year prefixing a yt-dlp upload_date
// (YYYYMMDD, e.g. "20260718" -> "2026"). Copied from caldav's yearOf
// (see monthOf's doc comment). Empty when date has no leading year.
func yearOf(date string) string {
	var year strings.Builder
	for _, r := range date {
		switch {
		case r >= '0' && r <= '9':
			year.WriteRune(r)
			if year.Len() == 4 {
				return year.String()
			}
		case r == '-':
			// tolerate a hyphenated date prefix
		default:
			return ""
		}
	}
	return ""
}
