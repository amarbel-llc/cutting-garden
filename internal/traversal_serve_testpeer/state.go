package traversal_serve_testpeer

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"

	dewey_errors "code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
)

// nodeSnapshot is memNode's persisted form (StateFileEnv). Children keeps
// its nil-vs-empty distinction through JSON (null vs []) because
// memNode.container() keys on it.
type nodeSnapshot struct {
	Name       string                                         `json:"name"`
	Type       string                                         `json:"type"`
	Facets     map[string][]cutting_garden_plugins.FacetValue `json:"facets"`
	Structured map[string]any                                 `json:"structured"`
	Raw        []byte                                         `json:"raw"`
	RawMime    string                                         `json:"raw_mime"`
	Children   []string                                       `json:"children"`
}

type treeSnapshot struct {
	Nodes      map[string]nodeSnapshot `json:"nodes"`
	Generation int64                   `json:"generation"`
	Assigned   int64                   `json:"assigned"`
}

// loadState replaces the tree with the snapshot at statePath; a missing
// file keeps the fresh fixture (the first invocation of a lane).
func (p *TreePlugin) loadState() error {
	data, err := os.ReadFile(p.statePath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return dewey_errors.Wrap(err)
	}

	var snapshot treeSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return dewey_errors.Wrapf(err, "testpeer state %s", p.statePath)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.nodes = make(map[string]*memNode, len(snapshot.Nodes))
	for uri, node := range snapshot.Nodes {
		p.nodes[uri] = &memNode{
			name:       node.Name,
			typ:        node.Type,
			facets:     node.Facets,
			structured: node.Structured,
			raw:        node.Raw,
			rawMime:    node.RawMime,
			children:   node.Children,
		}
	}
	p.generation = snapshot.Generation
	p.assigned = snapshot.Assigned

	return nil
}

// persistLocked writes the tree to statePath (a no-op without one). Caller
// holds p.mu.
func (p *TreePlugin) persistLocked() error {
	if p.statePath == "" {
		return nil
	}

	snapshot := treeSnapshot{
		Nodes:      make(map[string]nodeSnapshot, len(p.nodes)),
		Generation: p.generation,
		Assigned:   p.assigned,
	}
	for uri, node := range p.nodes {
		snapshot.Nodes[uri] = nodeSnapshot{
			Name:       node.name,
			Type:       node.typ,
			Facets:     node.facets,
			Structured: node.structured,
			Raw:        node.raw,
			RawMime:    node.rawMime,
			Children:   node.children,
		}
	}

	data, err := json.Marshal(snapshot)
	if err != nil {
		return dewey_errors.Wrap(err)
	}

	if err := os.WriteFile(p.statePath, data, 0o600); err != nil {
		return dewey_errors.Wrap(err)
	}

	return nil
}
