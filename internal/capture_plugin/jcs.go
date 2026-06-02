package capture_plugin

import (
	"bytes"
	"encoding/json"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// jcsMarshal serializes v as a JCS-canonical JSON byte slice (RFC 8785),
// single line, no trailing newline.
//
// Go's encoding/json already provides the two properties that matter for
// the node bodies this package emits: map keys are sorted
// lexicographically, and json.Marshal produces compact output with no
// insignificant whitespace. The one divergence from JCS — Go's default
// HTML escaping of `<`, `>`, `&` into `<` etc. — is disabled here.
//
// This is JCS-equivalent only for the value shapes the protocol uses:
// objects with ASCII keys, string values, booleans, and small
// non-negative integers (where Go's integer formatting matches JCS's
// ECMAScript number formatting). Callers MUST NOT pass floating-point
// numbers or non-ASCII keys, whose JCS canonicalization Go does not
// reproduce. The bodies defined in this package and its bindings honor
// that constraint.
func jcsMarshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, errors.Wrap(err)
	}
	// json.Encoder appends a trailing newline; strip it so the caller
	// controls body framing.
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
