package ajour

import (
	"strings"
	"testing"
)

// Stencil.Digest (cutting-garden#414): a blake2b256 markl id over the
// canonical Write bytes of the stencil tree alone, so every input spelling
// of one tree digests the same.

// completeDocumentHeadings is completeDocument in the heading rendering.
const completeDocumentHeadings = `# [moxy !clown-plugin-v1]
## [folio !mcp-server-v1]
### [_ !mcp-tool-v1 kind=read]
- [file:eng/repos !fs-path-v1 subtree]
- [_ !tool-input-v1 open]
### [write !mcp-tool-v1]
- [file:eng/repos/cutting-garden/.worktrees/swift-plum !fs-path-v1 subtree]
- [_ !tool-input-v1 open]
## [grit !mcp-server-v1]
### [_ !mcp-tool-v1 kind=read]
- [_ !tool-input-v1 open]
### [commit !mcp-tool-v1]
- [_ !tool-input-v1 open]
### [remote !mcp-tool-v1]
- [_ !tool-input-v1 field=subcommand value=[fetch, pull]]
- [_ !tool-input-v1 ^field=subcommand open]
## [smith !mcp-server-v1]
### [_ !mcp-tool-v1 kind=read]
- [_ !tool-input-v1 field=repo value=[linenisgreat/cutting-garden, linenisgreat/madder]]
- [_ !tool-input-v1 ^field=repo open]
### [issue-comment !mcp-tool-v1]
- [_ !tool-input-v1 field=repo value=[linenisgreat/cutting-garden, linenisgreat/madder]]
- [_ !tool-input-v1 ^field=repo open]
# [just-us !clown-plugin-v1]
## [just-us !mcp-server-v1]
### [run_recipe@blake2b256-s3 !mcp-tool-v1]
- [_ !just-recipe-v1 group=[build, test, lint]]
# [cutting-garden !clown-plugin-v1]
## [cutting-garden !mcp-server-v1]
### [list_nodes !mcp-tool-v1]
- [_ !tool-input-v1 open]
### [read_node !mcp-tool-v1]
### [mutate_nodes !mcp-tool-v1]
### [resources !mcp-resources-v1]
#### [ynab:main !cg-root-v1 subtree op=[read]]
#### [ynab:main !cg-root-v1 op=[patch]]
##### [budget/b1 !cutting_garden-ynab-budget-v1]
###### [transactions]
####### [_ !cutting_garden-ynab-transaction-v1 op=[patch]]
- [_ !cg-field-v1 field=[category, memo]]
# [_ !clown-plugin-v1 open]
## [_ !mcp-server-v1 open]
### [_ !mcp-tool-v1 open]
- [file:eng/repos/cutting-garden/infra/secrets !fs-path-v1 subtree deny]
`

func mustStencil(t *testing.T, src string) Stencil {
	t.Helper()
	s, err := ParseStencil(src)
	if err != nil {
		t.Fatalf("ParseStencil: %v", err)
	}
	return s
}

func TestStencilDigest(t *testing.T) {
	nested := mustStencil(t, completeDocument).Digest()
	if !strings.HasPrefix(nested, "blake2b256-") {
		t.Errorf("Digest() = %q, want a blake2b256 markl id", nested)
	}

	t.Run("A-50: nested and heading spellings give one digest", func(t *testing.T) {
		if got := mustStencil(t, completeDocumentHeadings).Digest(); got != nested {
			t.Errorf("heading spelling digests %s, nested %s", got, nested)
		}
	})

	t.Run("A-51: a /** and a subtree spelling give one digest", func(t *testing.T) {
		glob := mustStencil(t, `[read [-> file:/wt/** !fs-path-v1]]`).Digest()
		tag := mustStencil(t, `[read [-> file:/wt !fs-path-v1 subtree]]`).Digest()
		if glob != tag {
			t.Errorf("/** digests %s, subtree %s", glob, tag)
		}
	})

	t.Run("A-52: one atom changed changes it", func(t *testing.T) {
		changed := strings.Replace(completeDocument, "group=[build, test, lint]", "group=[build, test]", 1)
		if changed == completeDocument {
			t.Fatal("the fixture edit did not apply")
		}
		if got := mustStencil(t, changed).Digest(); got == nested {
			t.Errorf("an edited atom left the digest at %s", got)
		}
	})

	t.Run("A-53: the digest is blake2b256 over the Write bytes", func(t *testing.T) {
		// The `digest -a blake2b256` of toolbox(1) over these exact bytes,
		// computed outside Go: the Write of `[read [-> file:/wt !fs-path-v1
		// subtree]]` is "[read\n  [-> file:/wt !fs-path-v1 subtree]]\n".
		s := mustStencil(t, `[read [-> file:/wt !fs-path-v1 subtree]]`)
		var b strings.Builder
		s.Write(&b)
		if b.String() != writeBytesA53 {
			t.Fatalf("Write = %q, want %q (the bytes the independent digest was taken over)", b.String(), writeBytesA53)
		}
		if got := s.Digest(); got != digestA53 {
			t.Errorf("Digest() = %s, want %s", got, digestA53)
		}
	})
}

const (
	writeBytesA53 = "[read\n  [-> file:/wt !fs-path-v1 subtree]]\n"
	digestA53     = "blake2b256-afh700dptp242cttevgam735xq77kvcp4x48mjlh9g486xulyslqtqp70e"
)
