package fastmail

import (
	"net/url"
	"slices"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

var _ cutting_garden_plugins.NodeIDer = (*Plugin)(nil)

// RelativeNodeID gives a THREAD its organize box id (fastmail tags slice 1,
// Task 5b; plan D6 "object ids are thread ids"). A thread's identity rides in
// the URI query (`fastmail://acct/Inbox/?thread=T1`), which the framework's
// host+path default ignores — every thread in a mailbox would collapse to one
// id. So, for a thread in the anchor's account whose mailbox path starts with
// the anchor mailbox's path (segment-wise):
//
//   - the same mailbox → the bare thread id (`T1`);
//   - a descendant mailbox → the remaining mailbox segments, then the thread
//     id, `/`-joined (anchor `fastmail://acct/` → `Inbox/T1`).
//
// A `/` or `%` inside one mailbox NAME is percent-escaped so segment
// boundaries stay unambiguous (the id is never reversed, but it must stay
// injective); other runes, spaces included, stay readable — the box writer
// quotes the id as a trellis String when it is not a bare identifier. A JMAP
// thread id is URL-safe (RFC 8620 §1.2), so it never contains `/` and a bare
// `T1` can never equal a nested `sub/T1`. Every other node — a mailbox,
// email, or raw leaf; another account; a mailbox outside the anchor; an
// anchor that is itself a thread — returns ok=false and takes the default.
// Pure in (nodeURI, anchor), as the capability requires.
func (Plugin) RelativeNodeID(nodeURI, anchor string) (string, bool) {
	node, ok := classifyString(nodeURI)
	if !ok || node.kind != kindThread {
		return "", false
	}
	base, ok := classifyString(anchor)
	if !ok || (base.kind != kindMailbox && base.kind != kindAccountRoot) {
		return "", false
	}
	depth := len(base.mailboxPath)
	if node.account != base.account || len(node.mailboxPath) < depth ||
		!slices.Equal(node.mailboxPath[:depth], base.mailboxPath) {
		return "", false
	}
	parts := make([]string, 0, len(node.mailboxPath)-depth+1)
	for _, seg := range node.mailboxPath[depth:] {
		parts = append(parts, escapeIDSegment.Replace(seg))
	}
	return strings.Join(append(parts, node.threadID), "/"), true
}

// escapeIDSegment keeps one mailbox name a single id segment.
var escapeIDSegment = strings.NewReplacer("%", "%25", "/", "%2F")

// classifyString parses and classifies a URI string, reporting ok=false for
// anything unparseable or not a well-formed `fastmail://` URI.
func classifyString(s string) (nodeRef, bool) {
	u, err := url.Parse(s)
	if err != nil {
		return nodeRef{}, false
	}
	ref, err := classifyURI(u)
	return ref, err == nil
}
