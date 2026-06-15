package cutting_garden_plugin_file

import (
	"context"
	"net/url"
	"os"
	"path/filepath"

	"github.com/amarbel-llc/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
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
	info, err := os.Stat(abs)
	if err != nil {
		return nil, errors.Wrapf(err, "file plugin: stat %q", abs)
	}
	if !info.IsDir() {
		return nil, nil // a leaf (regular file) has no children
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, errors.Wrapf(err, "file plugin: read dir %q", abs)
	}
	nodes := make([]cutting_garden_plugins.Node, 0, len(entries))
	for _, e := range entries {
		typ := typeFile
		if e.IsDir() {
			typ = typeDirectory
		}
		nodes = append(nodes, cutting_garden_plugins.Node{
			URI:  &url.URL{Scheme: "file", Path: filepath.Join(abs, e.Name())},
			Name: e.Name(),
			Type: typ,
		})
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
