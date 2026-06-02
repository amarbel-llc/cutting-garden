package capture_plugin

import (
	"strings"

	"github.com/amarbel-llc/madder/go/pkgs/hyphence"
)

// Ref is one FDR-0001 typed blob reference: a named slot pointing at a
// previously-written node by its markl digest and type-string, with an
// optional type-signature lock.
type Ref struct {
	// Alias names the slot (e.g. "identity", "host", "payload"), or —
	// for the git payload's per-object refs — the git oid.
	Alias string
	// Digest is the referenced node's markl id (e.g. "sha256-…").
	Digest string
	// TypeString is the referenced node's type-string (the `!`-line
	// value), without a leading `!`.
	TypeString string
	// Sig is the optional type-signature (`@<sig>`) pinning the type
	// interpretation (RFC 0002 §Type Signatures). Empty = unlocked.
	Sig string
}

// LockedRef builds a reference with its type-signature filled from the
// build-time registry, locking the reference to the type's current
// definition. Unregistered types yield a sig-less (unlocked) reference.
func LockedRef(alias, digest, typeString string) Ref {
	r := Ref{Alias: alias, Digest: digest, TypeString: typeString}
	if sig, ok := SignatureFor(typeString); ok {
		r.Sig = sig
	}
	return r
}

// BuildNode is the exported entry point bindings use to materialize a
// plugin-defined node (e.g. the git payload node) in the same byte
// framing the protocol nodes use. typeString is the `!`-line value;
// refs are emitted in order; body is nil for metadata-only nodes.
func BuildNode(typeString string, refs []Ref, body []byte) []byte {
	return encodeNode(typeString, refs, body)
}

// JCS exposes the package's JCS-canonical JSON marshaler to bindings
// that build their own node bodies. The ASCII-key / no-float constraint
// documented on jcsMarshal applies.
func JCS(v any) ([]byte, error) {
	return jcsMarshal(v)
}

// encodeNode serializes one hyphence node: an opening boundary, the
// reference lines (in the given order), the `! type` line, the closing
// boundary, and — when body is non-nil — a blank line followed by the
// body (and a terminating newline if the body lacks one).
//
// The framing mirrors the proven capture_receipt layout
// (`---\n[refs]\n! type\n---\n\n[body]`), extended with FDR-0001 typed
// reference lines (`- <alias> < @<digest> !<type-string>`).
func encodeNode(typeString string, refs []Ref, body []byte) []byte {
	var b strings.Builder

	b.WriteString(hyphence.Boundary)
	b.WriteByte('\n')

	for _, r := range refs {
		b.WriteString("- ")
		b.WriteString(r.Alias)
		b.WriteString(" < @")
		b.WriteString(r.Digest)
		b.WriteString(" !")
		b.WriteString(r.TypeString)
		if r.Sig != "" {
			b.WriteByte('@')
			b.WriteString(r.Sig)
		}
		b.WriteByte('\n')
	}

	b.WriteString("! ")
	b.WriteString(typeString)
	b.WriteByte('\n')

	b.WriteString(hyphence.Boundary)
	b.WriteByte('\n')

	if body != nil {
		b.WriteByte('\n')
		b.Write(body)
		if len(body) == 0 || body[len(body)-1] != '\n' {
			b.WriteByte('\n')
		}
	}

	return []byte(b.String())
}
