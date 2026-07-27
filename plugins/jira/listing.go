package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

var _ cutting_garden_plugins.EnrichedLister = (*Plugin)(nil)

// enrichedFields is the field selector ListEnriched issues: the facet fields
// (status/issuetype/priority/updated/created) plus summary, so an enriched
// node carries both its Facets and its one listing Field in a single search.
var enrichedFields = []string{
	"status", "issuetype", "priority", "updated", "created", "summary",
}

// ListEnriched serves a PROJECT's issues with Facets and Fields populated and
// the RFC 0012 §6 filter pushed down into JQL (cutting-garden#160/#193) — the
// native-filtering path a sweep relies on to resolve "every Open issue in
// PROJ" in one search. The JQL is an OPTIMIZATION: every returned issue is
// re-checked host-side with filter.Matches over its parsed facets, so the set
// is exactly the filter's regardless of any JQL-translation subtlety (the
// month dimension's updated-or-created semantics most of all).
//
// At the HOST root (children are project CONTAINERS, not the issue objects a
// facet filter selects) and at an ISSUE leaf (no children), it DECLINES
// (ok=false) — mirroring caldav's calendar-home decline, so a sweep at those
// levels REFUSES rather than widening scope, and a plain listing falls back
// to ListRoots.
func (Plugin) ListEnriched(
	ctx context.Context,
	node *url.URL,
	filter cutting_garden_plugins.FacetFilter,
) ([]cutting_garden_plugins.Node, bool, error) {
	if node == nil {
		return nil, false, errors.ErrorWithStackf(
			"jira plugin: ListEnriched requires a node URI",
		)
	}

	base, username, token, err := connectionFromArg(node)
	if err != nil {
		return nil, false, err
	}
	origin, projectKey, issueKey, err := nodeFromBase(base)
	if err != nil {
		return nil, false, err
	}

	switch {
	case issueKey != "":
		// A single issue is a leaf — no children to enrich.
		return nil, false, nil
	case projectKey != "":
		jql, err := jqlForProjectFiltered(projectKey, filter)
		if err != nil {
			return nil, false, err
		}
		nodes, err := newClient(origin, username, token).
			enrichedIssueNodes(ctx, origin, projectKey, jql, filter)
		if err != nil {
			return nil, false, err
		}
		return nodes, true, nil
	default:
		// Host root: children are project containers, not filterable objects.
		return nil, false, nil
	}
}

// enrichedIssueNodes searches jql with the enriched selector and builds one
// enriched Node per issue that STILL matches filter host-side — the soundness
// guard over the JQL pushdown. Each node carries its parsed facets and its
// summary field.
func (c *client) enrichedIssueNodes(
	ctx context.Context,
	origin, projectKey, jql string,
	filter cutting_garden_plugins.FacetFilter,
) ([]cutting_garden_plugins.Node, error) {
	raws, err := c.searchRaw(ctx, jql, enrichedFields)
	if err != nil {
		return nil, err
	}

	nodes := make([]cutting_garden_plugins.Node, 0, len(raws))
	for _, raw := range raws {
		facets := issueFacets(raw)
		if !filter.Matches(facets) {
			// The JQL narrows the fetch; filter.Matches is authoritative, so
			// a node the JQL over-returned (or the translation approximated)
			// is dropped here — the returned set is exactly the filter's.
			continue
		}
		key, summary := keyAndSummary(raw)
		nodes = append(nodes, cutting_garden_plugins.Node{
			URI:    jiraURIForNode(origin, projectKey, key),
			Name:   key,
			Type:   typeIssue,
			Facets: facets,
			Fields: map[string]any{"summary": summary},
		})
	}
	return nodes, nil
}

// keyAndSummary parses an issue's key and summary from a search result.
func keyAndSummary(raw json.RawMessage) (key, summary string) {
	var v struct {
		Key    string `json:"key"`
		Fields struct {
			Summary string `json:"summary"`
		} `json:"fields"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.Key, v.Fields.Summary
}

// jqlForProjectFiltered builds the JQL selecting a project's issues narrowed
// by the RFC 0012 §6 filter: `project = "KEY"` AND one clause per predicate
// (in the filter's given order — AND is commutative, so the query is
// deterministic regardless), with the RESULT SET ordered by issue key
// (ORDER BY key ASC) for a deterministic listing. An unknown dimension is a
// caller-fault bad request (the same contract facets.counts enforces).
func jqlForProjectFiltered(
	projectKey string, filter cutting_garden_plugins.FacetFilter,
) (string, error) {
	clauses := []string{"project = " + jqlQuote(projectKey)}
	for _, pred := range filter {
		clause, err := jqlClauseFor(pred)
		if err != nil {
			return "", err
		}
		clauses = append(clauses, clause)
	}
	return strings.Join(clauses, " AND ") + " ORDER BY key ASC", nil
}

// jqlClauseFor maps one facet predicate to a JQL clause. The three
// categorical dimensions map 1:1 to their JQL fields; month maps to a
// date-range clause (jqlMonthClause).
func jqlClauseFor(
	pred cutting_garden_plugins.FacetPredicate,
) (string, error) {
	switch pred.Dimension {
	case facetStatus:
		return "status = " + jqlQuote(pred.Value), nil
	case facetIssueType:
		return "issuetype = " + jqlQuote(pred.Value), nil
	case facetPriority:
		return "priority = " + jqlQuote(pred.Value), nil
	case facetMonth:
		return jqlMonthClause(pred.Value)
	default:
		return "", errors.BadRequestf(
			"jira plugin: cannot filter on unknown dimension %q"+
				" (want status, issue_type, priority, or month)",
			pred.Dimension,
		)
	}
}

// jqlMonthClause translates a YYYY-MM month bucket to a JQL date-range clause
// matching the SAME updated-or-created semantics issueFacets buckets on: an
// issue is in month M if its updated is in M, or (never updated) its created
// is in M. Expressed soundly so the pushdown does not silently drop a
// never-updated issue that filter.Matches would keep.
func jqlMonthClause(month string) (string, error) {
	start, next, err := monthBounds(month)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"((updated >= %q AND updated < %q)"+
			" OR (updated is EMPTY AND created >= %q AND created < %q))",
		start, next, start, next,
	), nil
}

// monthBounds returns the first day of month and of the following month for a
// YYYY-MM bucket (e.g. "2026-07" -> "2026-07-01", "2026-08-01"), rolling the
// year over at December. A malformed bucket is a caller-fault bad request.
func monthBounds(month string) (start, next string, err error) {
	if len(month) != 7 || month[4] != '-' {
		return "", "", errors.BadRequestf(
			"jira plugin: month filter %q must be YYYY-MM", month,
		)
	}
	year, errY := strconv.Atoi(month[:4])
	mon, errM := strconv.Atoi(month[5:7])
	if errY != nil || errM != nil || mon < 1 || mon > 12 {
		return "", "", errors.BadRequestf(
			"jira plugin: month filter %q must be YYYY-MM", month,
		)
	}
	nextYear, nextMon := year, mon+1
	if nextMon > 12 {
		nextYear, nextMon = year+1, 1
	}
	return fmt.Sprintf("%04d-%02d-01", year, mon),
		fmt.Sprintf("%04d-%02d-01", nextYear, nextMon), nil
}

// jqlQuote wraps a value in JQL double quotes, escaping embedded quotes and
// backslashes — a facet value like a status name "In Progress" is a caller
// input, never trusted into the query unescaped.
func jqlQuote(v string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range v {
		if r == '"' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}
