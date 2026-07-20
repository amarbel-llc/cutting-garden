package capture_plugin

import (
	"sort"
	"strings"
	"sync"

	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
	"code.linenisgreat.com/piggy/go/pkgs/markl"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Type signatures (the `@<sig>` on FDR-0001 reference lines) are resolved
// through a BUILD-TIME EMBEDDED REGISTRY — RFC 0002 §Type Signatures,
// mechanism (1). Each protocol/binding type ships a canonical type-blob
// (its interface keys, serialized as deterministic TOML); the signature
// is that blob's markl id. The registry is populated at init() by
// capture_plugin (the protocol-defined types) and by each binding (its
// payload/leaf/receipt types), so a single binary holds the closed set
// of (type-string → signature) pairs it can emit and verify.
//
// Signatures are sha256 markl ids over the canonical TOML, matching the
// blob hash family this implementation writes nodes in. They are stable
// for a given type definition: changing a type's interface keys changes
// its signature, which is exactly the version-pinning the lock provides.

// TypeDef is the registrable interface definition of one node type. It
// is the source the canonical type-blob (and therefore the signature) is
// derived from.
type TypeDef struct {
	// TypeString is the `!`-line / reference type-string this defines.
	TypeString string
	// IANAMediaType is the type's media type (RFC 0002 §IANA Media Type
	// Interface). Required.
	IANAMediaType string
	// PayloadCardinality is "single" or "list" for payload types; empty
	// for non-payload types (RFC 0002 §payload_cardinality).
	PayloadCardinality string
}

type typeEntry struct {
	def       TypeDef
	signature string
}

var (
	typeRegistryMu sync.RWMutex
	typeRegistry   = map[string]typeEntry{}
)

// RegisterType installs def's type-blob and computes its signature.
// Intended for init() of the protocol package and each binding. Panics
// on a duplicate type-string or an empty/invalid definition — both are
// programmer errors caught at startup.
func RegisterType(def TypeDef) {
	if def.TypeString == "" {
		panic(errors.ErrorWithStackf("capture_plugin: RegisterType with empty type-string"))
	}
	if def.IANAMediaType == "" {
		panic(errors.ErrorWithStackf(
			"capture_plugin: type %q missing iana_media_type", def.TypeString,
		))
	}

	sig, err := signatureOf(def)
	if err != nil {
		panic(errors.Wrapf(err, "capture_plugin: sign type %q", def.TypeString))
	}

	typeRegistryMu.Lock()
	defer typeRegistryMu.Unlock()
	if _, ok := typeRegistry[def.TypeString]; ok {
		panic(errors.ErrorWithStackf(
			"capture_plugin: type %q already registered", def.TypeString,
		))
	}
	typeRegistry[def.TypeString] = typeEntry{def: def, signature: sig}
}

// SignatureFor returns the registered type's signature, and whether the
// type is known. Unknown types are sig-less (unlocked) per RFC 0002.
func SignatureFor(typeString string) (string, bool) {
	typeRegistryMu.RLock()
	defer typeRegistryMu.RUnlock()
	e, ok := typeRegistry[typeString]
	if !ok {
		return "", false
	}
	return e.signature, true
}

// MediaTypeFor returns the registered type's IANA media type, and
// whether the type is known.
func MediaTypeFor(typeString string) (string, bool) {
	typeRegistryMu.RLock()
	defer typeRegistryMu.RUnlock()
	e, ok := typeRegistry[typeString]
	if !ok {
		return "", false
	}
	return e.def.IANAMediaType, true
}

// VerifyRef checks a parsed reference's type lock against the registry.
// A sig-less reference is unlocked and always passes (RFC 0002). A
// signed reference to a registered type must match the registry's
// signature; a mismatch signals the reference was written against an
// incompatible type-definition version. References to types unknown to
// this binary pass (nothing to verify against).
func VerifyRef(r Ref) error {
	if r.Sig == "" {
		return nil
	}
	want, ok := SignatureFor(r.TypeString)
	if !ok {
		return nil
	}
	if r.Sig != want {
		return errors.ErrorWithStackf(
			"capture_plugin: type-lock mismatch for %q: reference signature %s, "+
				"this binary's signature %s", r.TypeString, r.Sig, want,
		)
	}
	return nil
}

// signatureOf computes the markl id of a type definition's canonical
// type-blob (TOML), via a discard-store digester so the id is a real,
// re-parseable markl id without persisting anything.
func signatureOf(def TypeDef) (string, error) {
	toml := canonicalTypeBlob(def)

	writer, err := blob_stores.NewDiscardBlobStore(markl.FormatHashSha256).
		MakeBlobWriter(markl.FormatHashSha256)
	if err != nil {
		return "", errors.Wrap(err)
	}
	if _, err := writer.Write([]byte(toml)); err != nil {
		return "", errors.Wrap(err)
	}
	if err := writer.Close(); err != nil {
		return "", errors.Wrap(err)
	}
	return writer.GetMarklId().String(), nil
}

// canonicalTypeBlob serializes a TypeDef's interface keys as
// deterministic TOML (keys in fixed lexical order, the optional
// payload_cardinality omitted when empty). This byte sequence is the
// type-blob whose markl id is the signature; its stability across runs
// and builds is what makes the embedded registry well-defined.
func canonicalTypeBlob(def TypeDef) string {
	kv := map[string]string{
		"iana_media_type": def.IANAMediaType,
	}
	if def.PayloadCardinality != "" {
		kv["payload_cardinality"] = def.PayloadCardinality
	}

	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(" = ")
		b.WriteString(tomlQuote(kv[k]))
		b.WriteByte('\n')
	}
	return b.String()
}

// tomlQuote renders a TOML basic string. The interface-key values used
// here (media types, "single"/"list") contain no characters needing
// escaping beyond the surrounding quotes; a backslash or quote in a
// value would be a definition error caught by review.
func tomlQuote(s string) string {
	return `"` + s + `"`
}
