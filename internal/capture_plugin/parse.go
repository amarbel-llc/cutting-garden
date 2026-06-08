package capture_plugin

import (
	"io"
	"strings"

	"github.com/amarbel-llc/madder/go/pkgs/hyphence"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// Node is a parsed protocol node: its type-string, its ordered typed
// blob references, and its body (nil for metadata-only nodes; the raw
// bytes after the blank line otherwise, JCS callers TrimSpace before
// unmarshaling).
type Node struct {
	Type string
	Refs []Ref
	Body []byte
}

// RefByAlias returns the first reference whose alias matches, and
// whether one was found.
func (n Node) RefByAlias(alias string) (Ref, bool) {
	for _, r := range n.Refs {
		if r.Alias == alias {
			return r, true
		}
	}
	return Ref{}, false
}

// ParseNode parses one hyphence node written by encodeNode/BuildNode. It
// is the inverse of that framing: an opening boundary, `- <alias> <
// @<digest> !<type>` reference lines, the `! type` line, the closing
// boundary, and an optional blank-line-separated body.
func ParseNode(r io.Reader) (Node, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return Node{}, errors.Wrap(err)
	}

	lines := strings.Split(string(raw), "\n")
	if len(lines) == 0 || lines[0] != hyphence.Boundary {
		return Node{}, errors.ErrorWithStackf(
			"capture_plugin: node does not open with %q boundary", hyphence.Boundary,
		)
	}

	var node Node
	i := 1
	for ; i < len(lines); i++ {
		line := lines[i]
		if line == hyphence.Boundary {
			break
		}
		switch {
		case strings.HasPrefix(line, "! "):
			node.Type = strings.TrimPrefix(line, "! ")
		case strings.HasPrefix(line, "- "):
			ref, ok := parseRefLine(line)
			if !ok {
				return Node{}, errors.ErrorWithStackf(
					"capture_plugin: malformed reference line %q", line,
				)
			}
			node.Refs = append(node.Refs, ref)
		}
	}

	if i >= len(lines) {
		return Node{}, errors.ErrorWithStackf(
			"capture_plugin: node missing closing boundary",
		)
	}
	if node.Type == "" {
		return Node{}, errors.ErrorWithStackf(
			"capture_plugin: node missing `! type` line",
		)
	}

	// Body: present iff a blank line follows the closing boundary.
	after := lines[i+1:]
	if len(after) >= 2 && after[0] == "" {
		node.Body = []byte(strings.Join(after[1:], "\n"))
	}

	return node, nil
}

// parseRefLine parses `- <alias> < @<digest> !<type-string>`.
func parseRefLine(line string) (Ref, bool) {
	rest := strings.TrimPrefix(line, "- ")
	alias, after, ok := strings.Cut(rest, " < @")
	if !ok {
		return Ref{}, false
	}
	digest, typ, ok := strings.Cut(after, " !")
	if !ok {
		return Ref{}, false
	}
	// Split an optional `@<sig>` type lock off the type-string.
	var sig string
	if typeString, lock, hasLock := strings.Cut(typ, "@"); hasLock {
		typ = typeString
		sig = lock
	}
	return Ref{Alias: alias, Digest: digest, TypeString: typ, Sig: sig}, true
}

const (
	receiptTypePrefix = "cutting_garden-capture-receipt-"
	receiptTypeSuffix = "-v1"
)

// KindFromReceiptType extracts the capture kind from a receipt
// type-string, e.g. "cutting_garden-capture-receipt-git-v1" → "git".
// ok is false for any string that is not a protocol receipt type —
// notably the underscored legacy fs tag
// "cutting_garden-capture_receipt-fs-v1", which this deliberately does
// not match, so callers can use it to discriminate protocol receipts
// from fs-v1 receipts.
func KindFromReceiptType(typeString string) (kind string, ok bool) {
	if !strings.HasPrefix(typeString, receiptTypePrefix) ||
		!strings.HasSuffix(typeString, receiptTypeSuffix) {
		return "", false
	}
	kind = strings.TrimSuffix(strings.TrimPrefix(typeString, receiptTypePrefix), receiptTypeSuffix)
	if kind == "" {
		return "", false
	}
	return kind, true
}
