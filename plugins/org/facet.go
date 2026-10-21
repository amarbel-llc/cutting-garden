package org

import (
	"context"
	"net/url"
	"slices"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// Field and facet keys (FDR 0026 D4–D6).
const (
	fieldTitle         = "title"
	fieldState         = "state"
	fieldPriority      = "priority"
	fieldTags          = "tags"
	fieldOwnTags       = "own_tags"
	fieldScheduled     = "scheduled"
	fieldDeadline      = "deadline"
	fieldClosed        = "closed"
	fieldScheduledBand = "scheduled_band"
	fieldDeadlineBand  = "deadline_band"
	fieldFile          = "file"
	fieldLevel         = "level"
	fieldIDSynthetic   = "id_synthetic"
	fieldID            = "id"
	fieldModified      = "modified"

	// valueNone is the known-empty value for a headline without a keyword,
	// priority, or date (RFC 0012 §3).
	valueNone = "none"
)

// Date bands, as caldav's due_band (overdue / today / this-week / later)
// plus "none" for an absent date (FDR 0026 D6).
const (
	bandOverdue  = "overdue"
	bandToday    = "today"
	bandThisWeek = "this-week"
	bandLater    = "later"
)

var bandValues = []string{bandOverdue, bandToday, bandThisWeek, bandLater, valueNone}

// bandRevalidate re-buckets the volatile date bands every 15 minutes, as
// caldav's due_band does.
const bandRevalidate = 15 * time.Minute

// dateBand buckets an ISO day against today in the host's zone. A done
// headline is "none": its date no longer schedules anything.
func dateBand(day string, done bool, now time.Time) string {
	if day == "" || done {
		return valueNone
	}
	// Compare calendar dates in UTC so a DST change between today and the
	// date cannot shift the day count.
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		return valueNone
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	switch days := int(d.Sub(today).Hours()) / 24; {
	case days < 0:
		return bandOverdue
	case days == 0:
		return bandToday
	case days <= 6:
		return bandThisWeek
	default:
		return bandLater
	}
}

func orNone(s string) string {
	if s == "" {
		return valueNone
	}
	return s
}

// headlineNode projects one headline into a Node with its listing fields
// and facet membership.
func headlineNode(pf *parsedFile, n *hnode, now time.Time) cutting_garden_plugins.Node {
	h := n.h
	done := pf.file.Keywords.IsDone(h.Keyword)
	fields := map[string]any{
		fieldTitle:    h.Title,
		fieldState:    orNone(h.Keyword),
		fieldPriority: orNone(h.Priority),
		// Copies: the headline lives in the shared parse cache.
		fieldTags:    nonNil(slices.Clone(h.Tags)),
		fieldOwnTags: nonNil(slices.Clone(h.OwnTags)),
		fieldFile:    pf.rel,
		fieldLevel:   h.Level,
	}
	// The id is reported only when it addresses this headline: a copy-pasted
	// duplicate's shadowed :ID: would point at the original.
	id := n.uri.Query().Get("id")
	fields[fieldIDSynthetic] = id == ""
	if id != "" {
		// After a quote, an agent reads this to learn the durable ?id= URI.
		fields[fieldID] = id
	}
	facets := map[string][]cutting_garden_plugins.FacetValue{
		fieldState:         {{Key: orNone(h.Keyword)}},
		fieldPriority:      {{Key: orNone(h.Priority)}},
		fieldScheduledBand: {{Key: dateBand(h.Planning.Scheduled, done, now)}},
		fieldDeadlineBand:  {{Key: dateBand(h.Planning.Deadline, done, now)}},
		fieldFile:          {{Key: pf.rel}},
	}
	for _, tag := range h.Tags {
		facets[fieldTags] = append(facets[fieldTags], cutting_garden_plugins.FacetValue{Key: tag})
	}
	for key, day := range map[string]string{
		fieldScheduled: h.Planning.Scheduled,
		fieldDeadline:  h.Planning.Deadline,
		fieldClosed:    h.Planning.Closed,
	} {
		if day != "" {
			fields[key] = day
			facets[key] = []cutting_garden_plugins.FacetValue{{Key: day}}
		}
	}
	return cutting_garden_plugins.Node{
		URI:    n.uri,
		Name:   h.Title,
		Type:   typeHeadline,
		Facets: facets,
		Fields: fields,
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// FacetCounts summarizes every headline beneath node — the whole folder
// tree, a file, or a headline's descendants — narrowed by filter, with a
// per-immediate-child breakdown (RFC 0012 §13).
func (Plugin) FacetCounts(
	ctx context.Context, node *url.URL, filter cutting_garden_plugins.FacetFilter,
) (cutting_garden_plugins.FacetResult, bool, error) {
	t, err := resolve(node)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, err
	}
	now := time.Now()
	acc := newFacetAccumulator(filter)

	switch t.ref.kind {
	case kindFolder:
		entries, err := t.listFolder(ctx)
		if err != nil {
			return cutting_garden_plugins.FacetResult{}, false, err
		}
		for _, e := range entries {
			before := acc.matched
			if err := t.countEntry(ctx, e, acc, now); err != nil {
				return cutting_garden_plugins.FacetResult{}, false, err
			}
			acc.attribute(entryNode(t.acct.Name, e), acc.matched-before)
		}
	case kindFile:
		pf, err := t.file(ctx)
		if err != nil {
			return cutting_garden_plugins.FacetResult{}, false, err
		}
		acc.countTop(pf, pf.top, now)
	default:
		pf, n, err := t.headline(ctx)
		if err != nil {
			return cutting_garden_plugins.FacetResult{}, false, err
		}
		acc.countTop(pf, n.children, now)
	}
	return acc.result(), true, nil
}

// countEntry folds one folder entry: a file's headlines, or a subfolder's
// whole tree.
func (t target) countEntry(ctx context.Context, e davEntry, acc *facetAccumulator, now time.Time) error {
	if !e.dir {
		pf, err := loadFile(ctx, t.client, t.acct, e.rel, e.etag)
		if err != nil {
			return acc.tolerate(ctx, err)
		}
		eachHeadline(pf.top, func(n *hnode) { acc.add(headlineNode(pf, n, now)) })
		return nil
	}
	sub := t
	sub.ref = nodeRef{account: t.ref.account, kind: kindFolder, path: e.rel, nth: 1}
	entries, err := sub.listFolder(ctx)
	if err != nil {
		return acc.tolerate(ctx, err)
	}
	for _, child := range entries {
		if err := sub.countEntry(ctx, child, acc, now); err != nil {
			return err
		}
	}
	return nil
}

type facetAccumulator struct {
	filter      cutting_garden_plugins.FacetFilter
	summary     cutting_garden_plugins.FacetSummary
	matched     int64
	sawAny      bool
	complete    bool
	byContainer []cutting_garden_plugins.FacetContainerBreakdown
}

func newFacetAccumulator(filter cutting_garden_plugins.FacetFilter) *facetAccumulator {
	return &facetAccumulator{filter: filter, summary: cutting_garden_plugins.FacetSummary{}, complete: true}
}

// tolerate implements fault isolation for a folder-wide count: one
// unreadable file or subfolder marks the summary partial instead of
// failing it. Cancellation and credential failures (401/403) still fail,
// so wrong credentials never pass as an empty, incomplete summary.
func (a *facetAccumulator) tolerate(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if isAuthFailure(err) {
		return err
	}
	a.complete = false
	return nil
}

func (a *facetAccumulator) add(n cutting_garden_plugins.Node) {
	a.sawAny = true
	if !a.filter.Matches(n.Facets) {
		return
	}
	a.matched++
	for dim, values := range n.Facets {
		hist := a.summary[dim]
		if hist == nil {
			hist = cutting_garden_plugins.FacetHistogram{}
			a.summary[dim] = hist
		}
		for _, v := range values {
			hist[v.Key]++
		}
	}
}

// countTop folds ns and all their descendants, attributing each top-level
// subtree to its head.
func (a *facetAccumulator) countTop(pf *parsedFile, ns []*hnode, now time.Time) {
	for _, n := range ns {
		before := a.matched
		head := headlineNode(pf, n, now)
		a.add(head)
		eachHeadline(n.children, func(d *hnode) { a.add(headlineNode(pf, d, now)) })
		a.attribute(head, a.matched-before)
	}
}

func (a *facetAccumulator) attribute(child cutting_garden_plugins.Node, count int64) {
	if count > 0 {
		a.byContainer = append(a.byContainer, cutting_garden_plugins.FacetContainerBreakdown{
			URI: child.URIString(), Name: child.Name, Count: count,
		})
	}
}

// result emits the summary. The volatile band dimensions carry their
// closed domain with informative zeros whenever any headline was seen
// (RFC 0012 §11.3).
func (a *facetAccumulator) result() cutting_garden_plugins.FacetResult {
	if a.sawAny {
		for _, dim := range []string{fieldScheduledBand, fieldDeadlineBand} {
			hist := a.summary[dim]
			if hist == nil {
				hist = cutting_garden_plugins.FacetHistogram{}
				a.summary[dim] = hist
			}
			for _, v := range bandValues {
				hist[v] += 0
			}
		}
	}
	limited, truncated := cutting_garden_plugins.SortAndLimitContainerBreakdown(a.byContainer)
	return cutting_garden_plugins.FacetResult{
		Summary:              a.summary,
		Complete:             a.complete,
		ByContainer:          limited,
		ByContainerTruncated: truncated,
	}
}

// FacetVersion is the file's ETag for a file or headline. A folder has
// no cheap aggregate token, so it declines.
//
// Blind spots (RFC 0012 §11.1): none known for a file or headline. Every org
// facet (state, tags including filetags and ancestors' tags, priority,
// scheduled, deadline, closed, file) derives from the content of the headline's
// own file, which the ETag covers whole; the `*_band` dimensions also depend on
// the clock, which §11.3's revalidation window handles, not this token. A
// headline moved to another file changes both files' ETags. Like any ETag
// token it trusts the server: one that did not
// change the ETag when the content changed would serve stale summaries until
// the framework TTL, and the plugin cannot detect that.
func (Plugin) FacetVersion(ctx context.Context, node *url.URL) (string, bool, error) {
	t, err := resolve(node)
	if err != nil {
		return "", false, err
	}
	if t.ref.kind == kindFolder {
		return "", false, nil
	}
	entries, err := t.client.propfind(ctx, t.ref.path, "0")
	if err != nil || len(entries) == 0 || entries[0].etag == "" {
		return "", false, err
	}
	return "etag:" + entries[0].etag, true, nil
}
