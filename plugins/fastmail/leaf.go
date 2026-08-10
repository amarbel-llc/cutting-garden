package fastmail

import (
	"context"
	"net/url"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// ReadLeaf fetches a leaf node's content:
//
//   - an email node → the structured JMAP Email JSON as Structured PLUS the
//     verbatim RFC 5322 bytes as Raw (message/rfc822). An email is a
//     container whose OWN body is this structured view (FDR 0024), so
//     ReadLeaf serves it even though the node also has a raw child.
//   - a raw node → the verbatim bytes only (Structured nil).
//
// Any other node kind (account root, mailbox, thread) is not a readable
// leaf: ok is false so the consumer falls back to the child listing. node
// MUST be non-nil.
func (Plugin) ReadLeaf(
	ctx context.Context,
	node *url.URL,
) (cutting_garden_plugins.LeafContent, bool, error) {
	if node == nil {
		return cutting_garden_plugins.LeafContent{}, false, errors.ErrorWithStackf(
			"fastmail plugin: ReadLeaf requires a node URI",
		)
	}
	ref, err := classifyURI(node)
	if err != nil {
		return cutting_garden_plugins.LeafContent{}, false, err
	}
	if ref.kind != kindEmail && ref.kind != kindRaw {
		return cutting_garden_plugins.LeafContent{}, false, nil
	}

	c, err := resolveClient(ref)
	if err != nil {
		return cutting_garden_plugins.LeafContent{}, false, err
	}

	emails, err := c.emailGet(ctx, []string{ref.emailID}, emailFacetProps)
	if err != nil {
		return cutting_garden_plugins.LeafContent{}, false, err
	}
	if len(emails) == 0 {
		// The email id does not resolve (deleted, or a stale URI): not a
		// readable leaf, not an error to surface.
		return cutting_garden_plugins.LeafContent{}, false, nil
	}
	email := emails[0]

	var raw []byte
	if email.BlobID != "" {
		raw, err = c.download(ctx, email.BlobID, rawFilename(ref.emailID), mimeRFC822)
		if err != nil {
			return cutting_garden_plugins.LeafContent{}, false, err
		}
	}

	content := cutting_garden_plugins.LeafContent{
		Raw:         raw,
		RawMimeType: mimeRFC822,
	}
	if ref.kind == kindEmail {
		// The email container's own body is the structured JMAP Email; the
		// raw node carries only the bytes.
		content.Structured = email
	}
	if raw == nil {
		content.RawMimeType = ""
	}
	return content, true, nil
}

// rawFilename is the {name} slot for the download URL — a stable, harmless
// filename hint derived from the message id.
func rawFilename(emailID string) string { return emailID + ".eml" }
