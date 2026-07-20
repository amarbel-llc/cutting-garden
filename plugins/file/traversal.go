package cutting_garden_plugin_file

import (
	"context"
	"net/url"
	"os"
	"path/filepath"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

const (
	// typeDirectory is a filesystem directory — a container whose children
	// are its immediate entries.
	typeDirectory = "cutting_garden-file-directory-v1"
	// typeFile is a regular file (or any non-directory entry) — a leaf
	// captured as one file entry.
	typeFile = "cutting_garden-file-object-v1"
)

var (
	_ cutting_garden_plugins.RootLister   = (*Plugin)(nil)
	_ cutting_garden_plugins.RootProvider = (*Plugin)(nil)
)

// Types declares the file plugin's two traversal node types: a directory
// container and a file leaf. Hyphenated and horizontally versioned (#79)
// so a future shape change adds a -v2 beside the -v1.
func (Plugin) Types() []cutting_garden_plugins.NodeType {
	return []cutting_garden_plugins.NodeType{
		{Tag: typeDirectory, Container: true},
		{Tag: typeFile, Container: false},
	}
}

// ListRoots returns the immediate entries of the directory at node — one
// level of the filesystem tree, the shallow analogue of the capture-side
// walkRoot. A node that is not a directory (a file leaf) has no children.
// Read-only: it reads directory metadata only, never file contents.
//
// Symlinks are NEVER followed to a directory — neither as a child entry nor
// as node itself. Both the per-entry classification (fs.DirEntry.Info(),
// which resolves via Lstat, never the symlink target) and the top-level
// check below (os.Lstat, not os.Stat) agree: a symlinked directory
// classifies as typeFile and yields no children even when addressed
// directly. This is the safer of the two options FDR 0014 leaves open —
// following would risk cycles and let a consumer escape the tree an
// operator scoped a root to via an unexpected symlink — and it mirrors
// capture's existing walkRoot, which already records a symlink as its own
// entry (capture_receipt.TypeSymlink) rather than descending it.
//
// Hidden dotfiles are included: this lists the user's own working tree, not
// a shared or untrusted corpus, so there is no reason to hide them.
//
// Entries that cannot be stat'd (a TOCTOU race — the entry is removed or
// becomes unreadable between os.ReadDir and the per-entry Info() call) are
// skipped silently rather than failing the whole listing or logging.
func (Plugin) ListRoots(
	_ context.Context,
	node *url.URL,
) ([]cutting_garden_plugins.Node, error) {
	if node == nil {
		return nil, errors.ErrorWithStackf(
			"file plugin: ListRoots requires a node URI",
		)
	}
	path, err := pathFromURL(node)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.Wrapf(err, "file plugin: resolve %q", path)
	}
	// Lstat, not Stat: node itself must not be followed through a symlink
	// either, or a symlinked directory addressed directly would list its
	// target's children despite classifying as a leaf everywhere else.
	info, err := os.Lstat(abs)
	if err != nil {
		return nil, errors.Wrapf(err, "file plugin: stat %q", abs)
	}
	if !info.IsDir() {
		return nil, nil // a leaf (regular file, or a symlink) has no children
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, errors.Wrapf(err, "file plugin: read dir %q", abs)
	}
	nodes := make([]cutting_garden_plugins.Node, 0, len(entries))
	for _, e := range entries {
		entryInfo, err := e.Info()
		if err != nil {
			// Vanished or unreadable since ReadDir (a TOCTOU race): omit
			// this entry rather than failing the whole listing.
			continue
		}

		typ := typeFile
		if entryInfo.IsDir() {
			typ = typeDirectory
		}

		n := cutting_garden_plugins.Node{
			URI:  &url.URL{Scheme: "file", Path: filepath.Join(abs, e.Name())},
			Name: e.Name(),
			Type: typ,
		}

		// Facets ride the SAME stat this loop already fetched to classify
		// the entry (RFC 0012 §1's "same enumeration" rule) — never a
		// separate per-node fetch. Only regular files carry them: a
		// symlink's own Size()/ModTime() (from Lstat) describe the link,
		// not meaningful file content, so symlinks contribute nothing.
		if typ == typeFile && entryInfo.Mode().IsRegular() {
			n.Facets = fileFacets(entryInfo)
		}

		nodes = append(nodes, n)
	}
	return nodes, nil
}

// Roots returns the process working directory as the file plugin's single
// intrinsic root (RFC 0007 § Root Sources, intrinsic) — so a no-argument
// `mcp` / `list` surfaces the current tree with no configuration. Any
// broader intrinsic root (e.g. /) is deliberately left to opt-in
// configuration (RFC 0007 § Security Considerations).
func (Plugin) Roots(context.Context) ([]*url.URL, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, errors.Wrapf(err, "file plugin: getwd")
	}
	return []*url.URL{{Scheme: "file", Path: cwd}}, nil
}
