package ajour

import (
	"strings"

	"code.linenisgreat.com/piggy/go/pkgs/markl"
)

// PurposeStencilDigest is the markl purpose a pinned stencil digest carries
// when it is written purpose-full (`cutting_garden-stencil-digest-v1@…`);
// its one format is blake2b256 (cutting-garden#414, agreed with piggy, the
// RFC 0013 owner).
const PurposeStencilDigest = "cutting_garden-stencil-digest-v1"

// Digest is the stencil's content address: a blake2b256 markl id over its
// canonical Write bytes, the tree alone (no hyphence head, nothing around
// it), spelled `blake2b256-<blech32>` as madder prints ids. Because Write is
// canonical, every input spelling of one tree (the heading rendering, a
// `/**` id) digests the same, and any change to the tree changes it.
func (s Stencil) Digest() string {
	var b strings.Builder
	s.Write(&b)
	hash, repoolHash := markl.FormatHashBlake2b256.Get()
	defer repoolHash()
	_, _ = hash.Write([]byte(b.String())) // a hash.Hash never fails a write
	id, repoolID := hash.GetMarklId()
	defer repoolID()
	return id.String()
}
