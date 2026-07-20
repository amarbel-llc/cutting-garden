package capture_plugin

import (
	"io"
	"strings"

	"code.linenisgreat.com/hyphence/go/hyphence"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
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
	// receiptTypePrefixHyphen is the legacy protocol-receipt prefix
	// (`capture-receipt`, hyphen-separated) carried by the git/web
	// receipts shipped before #112. It stays recognized on read forever:
	// those receipts are immutable and must keep dispatching.
	receiptTypePrefixHyphen = "cutting_garden-capture-receipt-"
	// receiptTypePrefixUnderscore is the converged prefix (#112):
	// `capture_receipt`, with the underscore binding `capture`+`receipt`
	// into one compound noun. New protocol families (caldav, …) and the
	// next version of the existing ones write this form. It is also the
	// prefix of the flat fs tag below, which is why flatFSTag must be
	// excluded explicitly.
	receiptTypePrefixUnderscore = "cutting_garden-capture_receipt-"

	// flatFSTag is the one and only flat (non-protocol) receipt tag. It
	// shares receiptTypePrefixUnderscore with new protocol families, so
	// KindFromReceiptType excludes it explicitly: a flat fs receipt is an
	// NDJSON store-group receipt (internal/capture_receipt), not a
	// protocol merkle tree, and must NOT be reported as a protocol receipt
	// of kind "fs". Kept as a literal here to avoid importing
	// internal/capture_receipt (and a dependency cycle); the two are
	// pinned equal by TestKindFromReceiptType.
	flatFSTag = "cutting_garden-capture_receipt-fs-v1"
)

// KindFromReceiptType extracts the capture kind from a protocol receipt
// type-string, e.g. "cutting_garden-capture-receipt-git-v1" → "git" or
// "cutting_garden-capture_receipt-caldav-v1" → "caldav". Both the legacy
// hyphen prefix and the converged underscore prefix (#112) are
// recognized.
//
// ok is false for any string that is not a protocol receipt type —
// notably the flat fs tag "cutting_garden-capture_receipt-fs-v1", which
// shares the underscore prefix but is a flat NDJSON receipt, not a
// protocol merkle tree. Callers rely on that exclusion to discriminate
// protocol receipts from the flat fs receipt.
func KindFromReceiptType(typeString string) (kind string, ok bool) {
	if typeString == flatFSTag {
		return "", false
	}

	var rest string
	switch {
	case strings.HasPrefix(typeString, receiptTypePrefixHyphen):
		rest = strings.TrimPrefix(typeString, receiptTypePrefixHyphen)
	case strings.HasPrefix(typeString, receiptTypePrefixUnderscore):
		rest = strings.TrimPrefix(typeString, receiptTypePrefixUnderscore)
	default:
		return "", false
	}

	// rest is `<kind>-v<N>`; split off the trailing `-v<digits>` so any
	// version is recognized, not just v1. (Whether a coder is registered
	// for that (kind, version) is the reader's concern — see Dispatch in
	// RFC 0010; this only classifies the type-string.)
	kind, version, ok := splitKindVersion(rest)
	if !ok || kind == "" || version == "" {
		return "", false
	}
	return kind, true
}

// splitKindVersion splits a `<kind>-v<N>` tail into its kind and the
// decimal version digits, requiring a non-empty kind and at least one
// digit after `-v`. It scans from the end so a kind containing `-v…`
// (none today, but the grammar allows hyphens in kinds) is not
// mis-split.
func splitKindVersion(s string) (kind, version string, ok bool) {
	i := strings.LastIndex(s, "-v")
	if i < 0 {
		return "", "", false
	}
	digits := s[i+len("-v"):]
	if digits == "" {
		return "", "", false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return "", "", false
		}
	}
	return s[:i], digits, true
}
