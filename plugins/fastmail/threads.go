package fastmail

import (
	"context"
	"strings"
)

// threadQueryLimit bounds one mailbox's thread listing (newest-first). A
// large tag can hold thousands of threads; this caps a single page. When
// the mailbox's thread total exceeds it, FacetCounts marks the summary
// partial (Complete=false) — Slice 1 does not page deeper (FDR 0024 tuning
// lever "page size").
const threadQueryLimit = 200

// threadView is one thread as the listing/facet layer sees it: its id, a
// display name (the representative message's subject), the newest
// receivedAt (the thread's date), and every member email (for union/any-of
// facet derivation).
type threadView struct {
	threadID   string
	name       string
	receivedAt string
	members    []Email
}

// mailboxThreadReps lists a mailbox's threads cheaply for ListRoots: one
// Email/query (collapsed, newest-first) plus one Email/get of the
// representative messages — no per-thread member fetch. It returns the
// representatives in query (newest-first) order.
func (c *client) mailboxThreadReps(
	ctx context.Context, mailboxID string,
) (reps []Email, total int64, err error) {
	repIDs, total, err := c.emailQuery(ctx, mailboxID, 0, threadQueryLimit)
	if err != nil {
		return nil, 0, err
	}
	if len(repIDs) == 0 {
		return nil, total, nil
	}
	emails, err := c.emailGet(ctx, repIDs, emailListProps)
	if err != nil {
		return nil, 0, err
	}
	return orderByIDs(emails, repIDs), total, nil
}

// mailboxThreadViews builds the full per-thread member view for facets and
// enriched listing: Email/query (collapsed reps) → Thread/get (member ids
// per thread) → one Email/get of every member with the facet property set,
// grouped back per thread in query (newest-first) order. total is the
// mailbox's thread count; a total above threadQueryLimit means this page is
// partial.
func (c *client) mailboxThreadViews(
	ctx context.Context, mailboxID string,
) (views []threadView, total int64, err error) {
	repIDs, total, err := c.emailQuery(ctx, mailboxID, 0, threadQueryLimit)
	if err != nil {
		return nil, 0, err
	}
	if len(repIDs) == 0 {
		return nil, total, nil
	}

	reps, err := c.emailGet(ctx, repIDs, emailFacetProps)
	if err != nil {
		return nil, 0, err
	}
	repByID := indexByID(reps)

	// Thread ids in query order, deduped.
	var threadIDs []string
	seenThread := map[string]bool{}
	for _, id := range repIDs {
		rep, ok := repByID[id]
		if !ok || seenThread[rep.ThreadID] {
			continue
		}
		seenThread[rep.ThreadID] = true
		threadIDs = append(threadIDs, rep.ThreadID)
	}

	threads, err := c.threadGet(ctx, threadIDs)
	if err != nil {
		return nil, 0, err
	}
	threadByID := make(map[string]Thread, len(threads))
	var allMemberIDs []string
	seenMember := map[string]bool{}
	for _, th := range threads {
		threadByID[th.ID] = th
		for _, mid := range th.EmailIDs {
			if seenMember[mid] {
				continue
			}
			seenMember[mid] = true
			allMemberIDs = append(allMemberIDs, mid)
		}
	}

	members, err := c.emailGet(ctx, allMemberIDs, emailFacetProps)
	if err != nil {
		return nil, 0, err
	}
	memberByID := indexByID(members)

	for _, tid := range threadIDs {
		th := threadByID[tid]
		rep := repByID[repIDForThread(repIDs, repByID, tid)]
		var msgs []Email
		for _, mid := range th.EmailIDs {
			if m, ok := memberByID[mid]; ok {
				msgs = append(msgs, m)
			}
		}
		views = append(views, threadView{
			threadID:   tid,
			name:       threadName(rep, tid),
			receivedAt: rep.ReceivedAt,
			members:    msgs,
		})
	}
	return views, total, nil
}

// repIDForThread finds the representative email id whose thread is tid.
func repIDForThread(repIDs []string, repByID map[string]Email, tid string) string {
	for _, id := range repIDs {
		if rep, ok := repByID[id]; ok && rep.ThreadID == tid {
			return id
		}
	}
	return ""
}

// threadName is the thread's display name: the representative message's
// subject, or the thread id when it has none.
func threadName(rep Email, tid string) string {
	if s := strings.TrimSpace(rep.Subject); s != "" {
		return s
	}
	return tid
}

// indexByID maps emails by id.
func indexByID(emails []Email) map[string]Email {
	m := make(map[string]Email, len(emails))
	for _, e := range emails {
		m[e.ID] = e
	}
	return m
}

// orderByIDs returns emails in the order their ids appear in ids (dropping
// any the server did not return).
func orderByIDs(emails []Email, ids []string) []Email {
	byID := indexByID(emails)
	out := make([]Email, 0, len(ids))
	for _, id := range ids {
		if e, ok := byID[id]; ok {
			out = append(out, e)
		}
	}
	return out
}

// yearOf extracts the four-digit year prefixing an ISO-8601 receivedAt
// (e.g. "2026-07-14T09:12:03Z" → "2026"). Empty when there is no leading
// year.
func yearOf(receivedAt string) string {
	var year strings.Builder
	for _, r := range receivedAt {
		switch {
		case r >= '0' && r <= '9':
			year.WriteRune(r)
			if year.Len() == 4 {
				return year.String()
			}
		default:
			return ""
		}
	}
	return ""
}

// firstFrom returns the first sender's email address (lowercased), or "".
func firstFrom(e Email) string {
	for _, a := range e.From {
		if a.Email != "" {
			return strings.ToLower(a.Email)
		}
	}
	return ""
}
