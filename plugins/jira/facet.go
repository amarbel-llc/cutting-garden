package jira

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// Facet dimension keys declared for the jira issue leaf (RFC 0012). Unlike
// caldav's REPORT-with-data (whose facet fields ride the same fetch that
// discovers members), jira's traversal listing (ListRoots) requests only
// `summary` — deliberately light for lazy browsing (traversal.go) — so
// none of these fields are "already in hand" from that call. FacetCounts
// below is the one place that fetches them, via ITS OWN one-shot search
// per project (never per issue), exactly mirroring how caldav's
// foldCalendarFacets fetches full calendar-data separately from the
// etag-only ListRoots (RFC 0012 §1's "never a per-node re-fetch" bars an
// N+1 per-issue GET, not a single widened search call).
const (
	facetStatus    = "status"     // fields.status.name
	facetIssueType = "issue_type" // fields.issuetype.name
	facetPriority  = "priority"   // fields.priority.name (nullable in Jira)
	facetMonth     = "month"      // YYYY-MM bucket of updated (fallback created)
)

// facetFields is the field selector FacetCounts issues against the search
// endpoint: cheap scalar fields only — no ADF bodies, comments, or
// attachments, which aggregation never needs. Distinct from listFields
// (traversal.go, summary-only) and allFields (capture.go, `*all`).
var facetFields = []string{"status", "issuetype", "priority", "updated", "created"}

var (
	_ cutting_garden_plugins.FacetDescriber = (*Plugin)(nil)
	_ cutting_garden_plugins.FacetCounter   = (*Plugin)(nil)
)

// DescribeFacets declares the facet dimensions of a jira issue leaf — the
// self-describing schema the mcp `describe_node_types` tool surfaces. All
// four dimensions are open (no closed Values domain): Jira's status/
// issuetype/priority schemes are per-project/per-instance configuration,
// not a fixed enum this plugin can enumerate up front, and month is
// naturally open-ended.
func (Plugin) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	return []cutting_garden_plugins.NodeTypeFacets{
		{
			Tag: typeIssue,
			Dimensions: []cutting_garden_plugins.FacetDimension{
				{
					Key:   facetStatus,
					Label: "Status",
					Kind:  cutting_garden_plugins.FacetCategorical,
				},
				{
					Key:   facetIssueType,
					Label: "Issue type",
					Kind:  cutting_garden_plugins.FacetCategorical,
				},
				{
					Key:   facetPriority,
					Label: "Priority",
					Kind:  cutting_garden_plugins.FacetCategorical,
				},
				{
					Key:   facetMonth,
					Label: "Month",
					Kind:  cutting_garden_plugins.FacetNumericBucket,
				},
			},
		},
	}
}

// FacetCounts summarizes a project's (or a site's) issues in one shot: it
// searches every matching issue with the light facetFields selector and
// folds the per-issue facet values into one summary — the preferred
// size-agnostic path (RFC 0012 §4.1). node addresses the same site/
// project/issue scopes as ListRoots and CaptureRoot (nodeFromBase), so
// discovery, capture, and facets cannot disagree about what a node covers.
//
// The result is always Complete: searchRaw paginates every page via
// nextPageToken with no source-imposed cap, so a facet count never comes
// back partial (RFC 0012 §5).
func (Plugin) FacetCounts(
	ctx context.Context,
	node *url.URL,
	filter cutting_garden_plugins.FacetFilter,
) (cutting_garden_plugins.FacetResult, bool, error) {
	if node == nil {
		return cutting_garden_plugins.FacetResult{}, false, errors.ErrorWithStackf(
			"jira plugin: FacetCounts requires a node URI",
		)
	}

	base, username, token, err := connectionFromArg(node)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, err
	}
	origin, projectKey, issueKey, err := nodeFromBase(base)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, err
	}
	c := newClient(origin, username, token)

	summary := cutting_garden_plugins.FacetSummary{}

	if issueKey != "" {
		// A single-issue node has no descendants; its hoisted summary is
		// its own lift. Mirrors CaptureRoot's single-issue special case
		// (one GET, no project search).
		iss, err := c.getIssue(ctx, issueKey, facetFields)
		if err != nil {
			return cutting_garden_plugins.FacetResult{}, false, err
		}
		if facets := issueFacets(iss.data); facets != nil && filter.Matches(facets) {
			liftFacets(summary, facets)
		}
		return cutting_garden_plugins.FacetResult{Summary: summary, Complete: true}, true, nil
	}

	// A named project, or (with none) every browsable project — the same
	// scoping resolveProjects already gives CaptureProtocol.
	projects, err := resolveProjects(
		ctx, c, projectKey, "", cutting_garden_plugins.ReporterOrNop(nil),
	)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, err
	}
	for _, pk := range projects {
		if err := c.foldIssueFacets(ctx, jqlForProject(pk), filter, summary); err != nil {
			return cutting_garden_plugins.FacetResult{}, false, err
		}
	}

	return cutting_garden_plugins.FacetResult{Summary: summary, Complete: true}, true, nil
}

// foldIssueFacets searches jql with facetFields (no bodies) and folds every
// matching issue's facet values into summary. Mirrors caldav's
// foldCalendarFacets: one dedicated fetch per scope, never per node.
func (c *client) foldIssueFacets(
	ctx context.Context,
	jql string,
	filter cutting_garden_plugins.FacetFilter,
	summary cutting_garden_plugins.FacetSummary,
) error {
	raws, err := c.searchRaw(ctx, jql, facetFields)
	if err != nil {
		return err
	}
	for _, raw := range raws {
		facets := issueFacets(raw)
		if facets == nil || !filter.Matches(facets) {
			continue
		}
		liftFacets(summary, facets)
	}
	return nil
}

// issueFacetFields is the subset of an issue resource's fields read for
// aggregation, shaped to match the facetFields selector.
type issueFacetFields struct {
	Fields struct {
		Status struct {
			Name string `json:"name"`
		} `json:"status"`
		IssueType struct {
			Name string `json:"name"`
		} `json:"issuetype"`
		Priority struct {
			Name string `json:"name"`
		} `json:"priority"`
		Updated string `json:"updated"`
		Created string `json:"created"`
	} `json:"fields"`
}

// issueFacets parses one issue's facet-relevant fields (fetched via
// facetFields) and projects them into RFC 0012 FacetValues. A field with
// no value (Jira permits an unset priority on some schemes; a malformed
// record) contributes nothing to its dimension rather than a synthetic
// zero-value bucket. Returns nil (contributes nothing) when raw does not
// parse or carries no facet-relevant field at all.
func issueFacets(raw json.RawMessage) map[string][]cutting_garden_plugins.FacetValue {
	var view issueFacetFields
	if err := json.Unmarshal(raw, &view); err != nil {
		return nil
	}

	facets := map[string][]cutting_garden_plugins.FacetValue{}
	if view.Fields.Status.Name != "" {
		facets[facetStatus] = []cutting_garden_plugins.FacetValue{
			{Key: view.Fields.Status.Name},
		}
	}
	if view.Fields.IssueType.Name != "" {
		facets[facetIssueType] = []cutting_garden_plugins.FacetValue{
			{Key: view.Fields.IssueType.Name},
		}
	}
	if view.Fields.Priority.Name != "" {
		facets[facetPriority] = []cutting_garden_plugins.FacetValue{
			{Key: view.Fields.Priority.Name},
		}
	}
	// updated-or-created: the freshness timestamp when present, falling
	// back to creation date so a never-updated issue still contributes to
	// the month dimension.
	date := view.Fields.Updated
	if date == "" {
		date = view.Fields.Created
	}
	if key, order := monthOf(date); key != "" {
		facets[facetMonth] = []cutting_garden_plugins.FacetValue{
			{Key: key, Order: order},
		}
	}

	if len(facets) == 0 {
		return nil
	}
	return facets
}

// liftFacets folds one issue's facet values into summary: +1 per
// (dimension, value key). The per-node "lift" of RFC 0012 §3.
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

// monthOf extracts the year-month bucket prefixing a Jira timestamp (e.g.
// "2026-07-18T09:15:00.000-0700" → key "2026-07", order 202607). Empty key
// when the value has no leading YYYYMM or the month digits are out of
// range. Copied from plugins/caldav's monthOf (RFC 0012's reference
// implementation): both plugins face the same "YYYYMM-prefixed timestamp,
// possibly hyphenated" shape, and the two packages share no common
// dependency to hang one copy off.
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
			// stop at the first non-date rune (T, Z, …)
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
