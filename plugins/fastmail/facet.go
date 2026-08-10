package fastmail

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Facet dimension keys declared on thread-v1. Every value is derived
// union/any-of across a thread's member messages, from fields already in
// hand after one Email/get (RFC 0012's cheapness rule) — no per-node fetch.
// All are READ-ONLY in Slice 1 (no FacetWrite*; writes are Slice 2).
const (
	facetTag           = "tag"            // user-mailbox membership (role == null), open
	facetRead          = "read"           // any member unseen ⇒ unread
	facetFlagged       = "flagged"        // any member $flagged
	facetFolder        = "folder"         // role-mailbox union (closed)
	facetYear          = "year"           // year of the thread's newest receivedAt
	facetFrom          = "from"           // union of member senders, top-N capped
	facetHasAttachment = "has_attachment" // any member has an attachment
)

// Closed-domain value keys for the boolean/categorical dimensions.
const (
	readValueRead   = "read"
	readValueUnread = "unread"
	flaggedValueYes = "flagged"
	flaggedValueNo  = "unflagged"
	attachmentYes   = "yes"
	attachmentNo    = "no"
)

// fromTopN caps the open, high-cardinality `from` dimension so it does not
// dwarf a summary (FDR 0024 tuning lever). When capping drops senders, the
// result is marked partial (Complete=false).
const fromTopN = 20

// folderValues is the closed `folder` domain: the role mailboxes a thread
// may belong to (FDR 0024). archive is the silent default a thread carries
// when nothing else applies, so it is listed but never special-cased here.
var folderValues = []cutting_garden_plugins.FacetValue{
	{Key: "inbox"},
	{Key: "archive"},
	{Key: "sent"},
	{Key: "junk"},
	{Key: "trash"},
}

// DescribeFacets declares thread-v1's facet schema — the self-describing
// surface `describe_node_types` reports. Only thread-v1 carries facets:
// mailboxes are navigation and emails are drilled-to leaves.
func (Plugin) DescribeFacets() []cutting_garden_plugins.NodeTypeFacets {
	return []cutting_garden_plugins.NodeTypeFacets{
		{
			Tag: typeThread,
			Dimensions: []cutting_garden_plugins.FacetDimension{
				{
					Key:   facetTag,
					Label: "Tag",
					Kind:  cutting_garden_plugins.FacetLabelled,
					Multi: true,
				},
				{
					Key:   facetRead,
					Label: "Read",
					Kind:  cutting_garden_plugins.FacetCategorical,
					Values: []cutting_garden_plugins.FacetValue{
						{Key: readValueUnread},
						{Key: readValueRead},
					},
				},
				{
					Key:   facetFlagged,
					Label: "Flagged",
					Kind:  cutting_garden_plugins.FacetCategorical,
					Values: []cutting_garden_plugins.FacetValue{
						{Key: flaggedValueYes},
						{Key: flaggedValueNo},
					},
				},
				{
					Key:    facetFolder,
					Label:  "Folder",
					Kind:   cutting_garden_plugins.FacetCategorical,
					Multi:  true,
					Values: folderValues,
				},
				{
					Key:   facetYear,
					Label: "Year",
					Kind:  cutting_garden_plugins.FacetNumericBucket,
				},
				{
					Key:   facetFrom,
					Label: "From",
					Kind:  cutting_garden_plugins.FacetLabelled,
					Multi: true,
				},
				{
					Key:   facetHasAttachment,
					Label: "Has attachment",
					Kind:  cutting_garden_plugins.FacetCategorical,
					Values: []cutting_garden_plugins.FacetValue{
						{Key: attachmentYes},
						{Key: attachmentNo},
					},
				},
			},
		},
	}
}

// threadFacets derives one thread's facet values union/any-of across its
// member messages. tree maps a member's mailbox ids to a user-tag path
// (role == null) or a role folder.
func threadFacets(
	v threadView, tree *mailboxTree,
) map[string][]cutting_garden_plugins.FacetValue {
	var (
		unread  bool
		flagged bool
		hasAtt  bool
	)
	tags := map[string]bool{}
	folders := map[string]bool{}
	froms := map[string]bool{}

	for _, m := range v.members {
		if !m.Keywords["$seen"] {
			unread = true
		}
		if m.Keywords["$flagged"] {
			flagged = true
		}
		if m.HasAttachment {
			hasAtt = true
		}
		for mid := range m.MailboxIDs {
			mbox, ok := tree.byID[mid]
			if !ok {
				continue
			}
			switch {
			case mbox.Role == "":
				tags[strings.Join(tree.path(mid), "/")] = true
			case allowedRoles[mbox.Role]:
				folders[mbox.Role] = true
			}
		}
		if f := firstFrom(m); f != "" {
			froms[f] = true
		}
	}

	facets := map[string][]cutting_garden_plugins.FacetValue{}

	readKey := readValueRead
	if unread {
		readKey = readValueUnread
	}
	facets[facetRead] = []cutting_garden_plugins.FacetValue{{Key: readKey}}

	flagKey := flaggedValueNo
	if flagged {
		flagKey = flaggedValueYes
	}
	facets[facetFlagged] = []cutting_garden_plugins.FacetValue{{Key: flagKey}}

	attKey := attachmentNo
	if hasAtt {
		attKey = attachmentYes
	}
	facets[facetHasAttachment] = []cutting_garden_plugins.FacetValue{{Key: attKey}}

	if multi := sortedKeys(tags); len(multi) > 0 {
		facets[facetTag] = keysToValues(multi)
	}
	if multi := sortedKeys(folders); len(multi) > 0 {
		facets[facetFolder] = keysToValues(multi)
	}
	if multi := sortedKeys(froms); len(multi) > 0 {
		facets[facetFrom] = keysToValues(multi)
	}
	if year := yearOf(v.receivedAt); year != "" {
		order, _ := strconv.ParseInt(year, 10, 64)
		facets[facetYear] = []cutting_garden_plugins.FacetValue{{Key: year, Order: order}}
	}
	return facets
}

// FacetCounts summarizes a mailbox's threads in one shot: it fetches the
// mailbox's collapsed threads with their members and folds each thread's
// union/any-of facet values into one summary (RFC 0012 §4.1). It applies to
// mailbox nodes only (a thread's facets live on the mailbox that lists it);
// account-root / thread / email / raw nodes report ok=false so the
// framework falls back. Complete is false when the thread page hit
// threadQueryLimit or the open `from` dimension was capped at fromTopN.
func (Plugin) FacetCounts(
	ctx context.Context,
	node *url.URL,
	filter cutting_garden_plugins.FacetFilter,
) (cutting_garden_plugins.FacetResult, bool, error) {
	if node == nil {
		return cutting_garden_plugins.FacetResult{}, false, errors.ErrorWithStackf(
			"fastmail plugin: FacetCounts requires a node URI",
		)
	}
	ref, err := classifyURI(node)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, err
	}
	if ref.kind != kindMailbox {
		return cutting_garden_plugins.FacetResult{}, false, nil
	}
	c, err := resolveClient(ref)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, err
	}

	tree, mbox, err := c.resolveMailbox(ctx, ref)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, err
	}

	views, total, err := c.mailboxThreadViews(ctx, mbox.ID)
	if err != nil {
		return cutting_garden_plugins.FacetResult{}, false, err
	}

	summary := cutting_garden_plugins.FacetSummary{}
	for _, v := range views {
		facets := threadFacets(v, tree)
		if !filter.Matches(facets) {
			continue
		}
		liftFacets(summary, facets)
	}

	complete := total <= int64(threadQueryLimit)
	if capHistogram(summary[facetFrom], fromTopN) {
		complete = false
	}

	return cutting_garden_plugins.FacetResult{Summary: summary, Complete: complete}, true, nil
}

// FacetVersion is the fastmail change token (RFC 0012 §11): the
// account-global Email type state (RFC 8620 §1.5), which moves on ANY email
// change — new, deleted, or a keyword/mailbox mutation — so it never misses
// a facet-relevant change. One Email/get with no ids, strictly cheaper than
// FacetCounts. It applies to mailbox nodes, mirroring FacetCounts; other
// kinds report ok=false so the framework falls back to a TTL.
func (Plugin) FacetVersion(
	ctx context.Context, node *url.URL,
) (string, bool, error) {
	if node == nil {
		return "", false, errors.ErrorWithStackf(
			"fastmail plugin: FacetVersion requires a node URI",
		)
	}
	ref, err := classifyURI(node)
	if err != nil {
		return "", false, err
	}
	if ref.kind != kindMailbox {
		return "", false, nil
	}
	c, err := resolveClient(ref)
	if err != nil {
		return "", false, err
	}
	state, err := c.emailState(ctx)
	if err != nil {
		return "", false, err
	}
	if state == "" {
		return "", false, nil
	}
	return state, true, nil
}

// resolveMailbox fetches the account's mailbox tree and resolves ref's
// name-path to its mailbox. A path that names no mailbox is a bad request.
func (c *client) resolveMailbox(
	ctx context.Context, ref nodeRef,
) (*mailboxTree, Mailbox, error) {
	mailboxes, err := c.mailboxGetAll(ctx)
	if err != nil {
		return nil, Mailbox{}, err
	}
	tree := newMailboxTree(mailboxes)
	mbox, ok := tree.resolvePath(ref.mailboxPath)
	if !ok {
		return nil, Mailbox{}, errors.BadRequestf(
			"fastmail plugin: no mailbox at path %q in account %q",
			pathString(ref.mailboxPath), ref.account,
		)
	}
	return tree, mbox, nil
}

// liftFacets folds one thread's facet values into summary: +1 per
// (dimension, value key). The per-node "lift" of RFC 0012 §3. Because a
// thread can carry several tags/folders/senders, these are membership
// counts — a thread under two tags counts under each.
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

// capHistogram keeps only the top-n buckets of hist by descending count
// (ties broken by ascending key for determinism), deleting the rest in
// place. It reports whether any bucket was dropped — the caller marks the
// result partial. A nil or already-small histogram is left untouched.
func capHistogram(hist cutting_garden_plugins.FacetHistogram, n int) bool {
	if len(hist) <= n {
		return false
	}
	type kv struct {
		key   string
		count int64
	}
	entries := make([]kv, 0, len(hist))
	for k, v := range hist {
		entries = append(entries, kv{k, v})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].key < entries[j].key
	})
	for _, e := range entries[n:] {
		delete(hist, e.key)
	}
	return true
}

// sortedKeys returns a set's keys in sorted order (stable multi-value
// output).
func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// keysToValues wraps facet-value keys as FacetValues.
func keysToValues(keys []string) []cutting_garden_plugins.FacetValue {
	values := make([]cutting_garden_plugins.FacetValue, len(keys))
	for i, k := range keys {
		values[i] = cutting_garden_plugins.FacetValue{Key: k}
	}
	return values
}
