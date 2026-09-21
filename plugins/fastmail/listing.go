package fastmail

import (
	"context"
	"net/url"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Listing-field keys for the thread and email nodes — the human-readable
// "what is this" projection (cutting-garden#160), distinct from the bucketed
// facet membership. They are the stored keys the unified codecs (unified.go)
// read; the derived facet keys alias them.
const (
	listingFieldSubject = "subject"
	listingFieldFrom    = "from"
	listingFieldDate    = "date"
	// listingFieldTags is the thread's designated tag set (D6): label tags ∪
	// state tags, a []string in Node.Fields — the stored field tagsCodec
	// presents and, once the write side lands, writes back as a full set.
	listingFieldTags = "tags"
	// Mailbox direct-membership counts (progressive disclosure: each label
	// container advertises how much it holds). Direct membership only, not a
	// recursive subtree rollup.
	listingFieldThreads = "threads"
	listingFieldEmails  = "emails"
)

// DescribeListingFields declares each type's display fields — the
// discoverability half of enrichment, symmetric with DescribeFacets: a
// consumer learns via describe_node_types which Node.Fields keys a node may
// carry. Hand-written (the SDK has no listing-field derivation helper yet)
// but a strict projection of the unified declaration —
// TestDescribeListingFields_ConsistentWithUnified pins key/label/trailer
// agreement. Nothing is Writable: the plugin implements no
// FieldWriteApplier, and the tag set writes through the facet membership
// path (a full-set replacement), not an inline field edit — exactly caldav's
// categories rule. subject is the box trailer (D6). has_attachment is a
// grouping dimension with no stored counterpart, so it is not listed.
func (Plugin) DescribeListingFields() []cutting_garden_plugins.NodeTypeListingFields {
	subject := cutting_garden_plugins.ListingField{Key: listingFieldSubject, Label: "Subject", Trailer: true}
	from := cutting_garden_plugins.ListingField{Key: listingFieldFrom, Label: "From"}
	date := cutting_garden_plugins.ListingField{Key: listingFieldDate, Label: "Date"}
	return []cutting_garden_plugins.NodeTypeListingFields{
		{
			Tag: typeMailbox,
			Fields: []cutting_garden_plugins.ListingField{
				{Key: listingFieldThreads, Label: "Threads"},
				{Key: listingFieldEmails, Label: "Emails"},
			},
		},
		{
			Tag: typeThread,
			Fields: []cutting_garden_plugins.ListingField{
				{Key: listingFieldTags, Label: "Tags"},
				from, date, subject,
			},
		},
		{Tag: typeEmail, Fields: []cutting_garden_plugins.ListingField{from, date, subject}},
	}
}

// mailboxFields projects a mailbox's direct-membership counts onto its Node
// listing fields — the count-per-tag progressive-disclosure signal. Both
// counts are always present (a zero count is meaningful: an empty label).
func mailboxFields(m Mailbox) map[string]any {
	return map[string]any{
		listingFieldThreads: m.TotalThreads,
		listingFieldEmails:  m.TotalEmails,
	}
}

// ListEnriched serves a mailbox's children enriched — child mailboxes
// (structural navigation, unenriched) plus its threads with Facets and
// Fields populated and narrowed by filter — in the SAME set ListRoots
// returns (RFC 0012 §12.2 level-scoping). It applies to mailbox nodes only;
// every other level (account root, thread, email, raw) reports ok=false so
// the framework falls back to ListRoots plus host-side handling.
//
// The filter narrows the THREAD nodes (a from=/date= drill); child mailbox
// nodes are always retained as navigation, since the filter dimensions are
// thread facets a mailbox container does not itself carry.
func (Plugin) ListEnriched(
	ctx context.Context,
	node *url.URL,
	filter cutting_garden_plugins.FacetFilter,
) ([]cutting_garden_plugins.Node, bool, error) {
	if node == nil {
		return nil, false, errors.ErrorWithStackf(
			"fastmail plugin: ListEnriched requires a node URI",
		)
	}
	ref, err := classifyURI(node)
	if err != nil {
		return nil, false, err
	}
	if ref.kind != kindMailbox {
		return nil, false, nil
	}
	c, err := resolveClient(ref)
	if err != nil {
		return nil, false, err
	}

	tree, mbox, err := c.resolveMailbox(ctx, ref)
	if err != nil {
		return nil, false, err
	}

	nodes := mailboxNodes(ref.account, tree, tree.childrenInScope(mbox.ID))

	views, _, err := c.mailboxThreadViews(ctx, mbox.ID)
	if err != nil {
		return nil, false, err
	}
	for _, v := range views {
		facets := threadFacets(v, tree)
		if !filter.Matches(facets) {
			continue
		}
		nodes = append(nodes, cutting_garden_plugins.Node{
			URI:    threadURI(ref.account, ref.mailboxPath, v.threadID),
			Name:   v.name,
			Type:   typeThread,
			Facets: facets,
			Fields: threadFields(v, tree),
		})
	}
	return nodes, true, nil
}

// emailFields projects one message onto its listing fields.
func emailFields(e Email) map[string]any {
	fields := map[string]any{}
	if e.Subject != "" {
		fields[listingFieldSubject] = e.Subject
	}
	if f := firstFrom(e); f != "" {
		fields[listingFieldFrom] = f
	}
	if e.ReceivedAt != "" {
		fields[listingFieldDate] = e.ReceivedAt
	}
	return fields
}

// threadFields projects one thread onto its listing fields: the display
// name (subject), the representative sender, the newest date, and the tag
// set (threadTags — the same set threadFacets counts under `tags`). An
// empty tag set omits the key, matching the present-but-empty-omitted
// convention of the other fields.
func threadFields(v threadView, tree *mailboxTree) map[string]any {
	fields := map[string]any{}
	if v.name != "" {
		fields[listingFieldSubject] = v.name
	}
	if len(v.members) > 0 {
		if f := firstFrom(v.members[0]); f != "" {
			fields[listingFieldFrom] = f
		}
	}
	if v.receivedAt != "" {
		fields[listingFieldDate] = v.receivedAt
	}
	if tags := threadTags(v.members, tree); len(tags) > 0 {
		fields[listingFieldTags] = tags
	}
	return fields
}
