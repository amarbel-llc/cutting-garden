package fastmail

import (
	"context"
	"net/url"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

const (
	// typeMailbox is a Fastmail mailbox (a tag in labels mode) — a
	// container whose children are its child mailboxes and the threads
	// tagged at this exact level.
	typeMailbox = "cutting_garden-fastmail-mailbox-v1"
	// typeThread is a mail thread — the primary organizing unit
	// (FDR 0024). A container whose children are its member email nodes;
	// its facet values are derived union/any-of across those members.
	typeThread = "cutting_garden-fastmail-thread-v1"
	// typeEmail is one message — a container whose OWN body is the
	// structured JMAP Email JSON (via ReadLeaf) and whose child is the raw
	// message leaf.
	typeEmail = "cutting_garden-fastmail-email-v1"
	// typeEmailRaw is the verbatim RFC 5322 message — a leaf stamped
	// message/rfc822.
	typeEmailRaw = "cutting_garden-fastmail-email-raw-v1"
)

// mimeRFC822 is the IANA content type of a raw message body — every raw
// leaf's LeafContent.RawMimeType.
const mimeRFC822 = "message/rfc822"

// Types declares the node types the fastmail tree is built from. Tags are
// hyphenated and horizontally versioned (FDR 0018) so a future shape change
// adds a -v2 tag beside the -v1 rather than breaking it.
func (Plugin) Types() []cutting_garden_plugins.NodeType {
	return []cutting_garden_plugins.NodeType{
		{Tag: typeMailbox, Container: true},
		{Tag: typeThread, Container: true},
		// The email is a container (its child is the raw leaf) whose own
		// body is the structured JMAP Email JSON via ReadLeaf.
		{Tag: typeEmail, Container: true},
		{Tag: typeEmailRaw, Container: false, MimeType: mimeRFC822},
	}
}

// ListRoots returns the immediate children of node:
//
//   - account root → the account's top-level in-scope mailboxes.
//   - mailbox → its in-scope child mailboxes + the threads tagged at this
//     exact level (Email/query collapsed, newest-first).
//   - thread → its member email nodes.
//   - email → the raw-message leaf (attachment leaves are deferred).
//   - raw → none (a leaf).
func (Plugin) ListRoots(
	ctx context.Context,
	node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	if node == nil {
		return nil, errors.ErrorWithStackf(
			"fastmail plugin: ListRoots requires a node URI",
		)
	}
	ref, err := classifyURI(node)
	if err != nil {
		return nil, err
	}
	c, err := resolveClient(ref)
	if err != nil {
		return nil, err
	}

	switch ref.kind {
	case kindAccountRoot:
		return c.accountRootNodes(ctx, ref.account)
	case kindMailbox:
		return c.mailboxChildNodes(ctx, ref)
	case kindThread:
		return c.threadChildNodes(ctx, ref)
	case kindEmail:
		return []cutting_garden_plugins.Node{rawNode(ref)}, nil
	case kindRaw:
		return nil, nil
	default:
		return nil, nil
	}
}

// accountRootNodes lists the account's top-level in-scope mailboxes.
func (c *client) accountRootNodes(
	ctx context.Context, account string,
) ([]cutting_garden_plugins.Node, error) {
	mailboxes, err := c.mailboxGetAll(ctx)
	if err != nil {
		return nil, err
	}
	tree := newMailboxTree(mailboxes)
	return mailboxNodes(account, tree, tree.topLevelInScope()), nil
}

// mailboxChildNodes lists a mailbox's in-scope child mailboxes plus its
// threads (cheap representatives only — facets/fields are the enriched
// path). It resolves the mailbox name-path to its JMAP id first.
func (c *client) mailboxChildNodes(
	ctx context.Context, ref nodeRef,
) ([]cutting_garden_plugins.Node, error) {
	mailboxes, err := c.mailboxGetAll(ctx)
	if err != nil {
		return nil, err
	}
	tree := newMailboxTree(mailboxes)
	mbox, ok := tree.resolvePath(ref.mailboxPath)
	if !ok {
		return nil, errors.BadRequestf(
			"fastmail plugin: no mailbox at path %q in account %q",
			pathString(ref.mailboxPath), ref.account,
		)
	}

	nodes := mailboxNodes(ref.account, tree, tree.childrenInScope(mbox.ID))

	reps, _, err := c.mailboxThreadReps(ctx, mbox.ID)
	if err != nil {
		return nil, err
	}
	for _, rep := range reps {
		nodes = append(nodes, cutting_garden_plugins.Node{
			URI:  threadURI(ref.account, ref.mailboxPath, rep.ThreadID),
			Name: threadName(rep, rep.ThreadID),
			Type: typeThread,
		})
	}
	return nodes, nil
}

// threadChildNodes lists a thread's member email nodes, each carrying its
// cheap listing fields (subject / from / date).
func (c *client) threadChildNodes(
	ctx context.Context, ref nodeRef,
) ([]cutting_garden_plugins.Node, error) {
	threads, err := c.threadGet(ctx, []string{ref.threadID})
	if err != nil {
		return nil, err
	}
	if len(threads) == 0 {
		return nil, nil
	}
	members, err := c.emailGet(ctx, threads[0].EmailIDs, emailFacetProps)
	if err != nil {
		return nil, err
	}
	ordered := orderByIDs(members, threads[0].EmailIDs)
	nodes := make([]cutting_garden_plugins.Node, 0, len(ordered))
	for _, m := range ordered {
		nodes = append(nodes, cutting_garden_plugins.Node{
			URI:    emailURI(ref.account, ref.mailboxPath, ref.threadID, m.ID),
			Name:   threadName(m, m.ID),
			Type:   typeEmail,
			Fields: emailFields(m),
		})
	}
	return nodes, nil
}

// mailboxNodes maps in-scope mailboxes to container Nodes addressed by
// their full name-path.
func mailboxNodes(
	account string, tree *mailboxTree, mailboxes []Mailbox,
) []cutting_garden_plugins.Node {
	nodes := make([]cutting_garden_plugins.Node, 0, len(mailboxes))
	for _, m := range mailboxes {
		nodes = append(nodes, cutting_garden_plugins.Node{
			URI:    mailboxURI(account, tree.path(m.ID)),
			Name:   m.Name,
			Type:   typeMailbox,
			Fields: mailboxFields(m),
		})
	}
	return nodes
}

// rawNode is the raw-message leaf child of an email node.
func rawNode(ref nodeRef) cutting_garden_plugins.Node {
	return cutting_garden_plugins.Node{
		URI:  rawURI(ref.account, ref.mailboxPath, ref.threadID, ref.emailID),
		Name: "raw",
		Type: typeEmailRaw,
	}
}

// pathString renders a mailbox name-path for diagnostics.
func pathString(segs []string) string {
	return "/" + strings.Join(segs, "/")
}
