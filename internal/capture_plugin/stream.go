package capture_plugin

import (
	"bufio"
	"bytes"
	"io"
	"strings"

	"code.linenisgreat.com/hyphence/go/hyphence"
	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
	"code.linenisgreat.com/piggy/go/pkgs/markl"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// ParseNodeHeader parses a node's framing (its type-string and ordered
// references) from r and returns the parsed header alongside a reader
// positioned at the first byte of the node body. Unlike ParseNode it does
// not buffer the body, so a caller materializing a large payload can
// io.Copy it straight to its destination.
//
// The body reader yields exactly the bytes ParseNode would have returned
// as Node.Body (verified by TestParseNodeHeader_BodyMatchesParseNode); it
// is empty when the node has no body, and is only valid until r is
// exhausted or closed. The returned Node's Body field is always nil — the
// body lives in the reader.
func ParseNodeHeader(r io.Reader) (Node, io.Reader, error) {
	br := bufio.NewReader(r)

	open, err := readFramedLine(br)
	if err != nil {
		return Node{}, nil, errors.Wrapf(err, "capture_plugin: read node opening boundary")
	}
	if open != hyphence.Boundary {
		return Node{}, nil, errors.ErrorWithStackf(
			"capture_plugin: node does not open with %q boundary", hyphence.Boundary,
		)
	}

	var node Node
	closed := false
	for {
		line, lineErr := readFramedLine(br)
		if lineErr == io.EOF {
			break
		}
		if lineErr != nil {
			return Node{}, nil, errors.Wrap(lineErr)
		}
		if line == hyphence.Boundary {
			closed = true
			break
		}
		switch {
		case strings.HasPrefix(line, "! "):
			node.Type = strings.TrimPrefix(line, "! ")
		case strings.HasPrefix(line, "- "):
			ref, ok := parseRefLine(line)
			if !ok {
				return Node{}, nil, errors.ErrorWithStackf(
					"capture_plugin: malformed reference line %q", line,
				)
			}
			node.Refs = append(node.Refs, ref)
		}
	}

	if !closed {
		return Node{}, nil, errors.ErrorWithStackf(
			"capture_plugin: node missing closing boundary",
		)
	}
	if node.Type == "" {
		return Node{}, nil, errors.ErrorWithStackf(
			"capture_plugin: node missing `! type` line",
		)
	}

	// A body is present iff a blank line follows the closing boundary
	// (matching ParseNode); the bytes after that blank line are the body
	// verbatim. EOF or any non-blank content right after the boundary
	// means no body, exactly as ParseNode's `after[0] == ""` guard.
	blank, blankErr := readFramedLine(br)
	if blankErr == io.EOF || blank != "" {
		return node, bytes.NewReader(nil), nil
	}
	if blankErr != nil {
		return Node{}, nil, errors.Wrap(blankErr)
	}
	return node, br, nil
}

// readFramedLine reads one '\n'-terminated line from br with the trailing
// newline stripped. A final line with no trailing newline is returned with
// a nil error; io.EOF is returned only when no bytes remain.
func readFramedLine(br *bufio.Reader) (string, error) {
	s, err := br.ReadString('\n')
	switch {
	case err == io.EOF && s != "":
		return s, nil
	case err != nil:
		return "", err
	default:
		return strings.TrimSuffix(s, "\n"), nil
	}
}

// OpenNodeBody opens the blob identified by digest and parses its node
// header, returning the header and a ReadCloser that streams the node
// body. The caller MUST Close the returned reader — it owns the underlying
// blob reader. Use this instead of ReadNode when the body may be large
// (e.g. restoring a captured payload) to avoid buffering it in memory.
func OpenNodeBody(
	store blob_stores.BlobStoreInitialized,
	digest string,
) (Node, io.ReadCloser, error) {
	var id markl.Id
	if err := id.Set(digest); err != nil {
		return Node{}, nil, errors.Wrapf(err, "capture_plugin: parse node id %q", digest)
	}

	reader, err := store.MakeBlobReader(&id)
	if err != nil {
		return Node{}, nil, errors.Wrapf(err, "capture_plugin: open node %s", digest)
	}

	node, body, err := ParseNodeHeader(reader)
	if err != nil {
		_ = reader.Close()
		return Node{}, nil, errors.Wrapf(err, "capture_plugin: parse node %s", digest)
	}
	return node, nodeBodyReadCloser{Reader: body, Closer: reader}, nil
}

// nodeBodyReadCloser couples the body stream returned by ParseNodeHeader
// with the underlying blob reader's Close, so the caller closes one thing.
type nodeBodyReadCloser struct {
	io.Reader
	io.Closer
}
