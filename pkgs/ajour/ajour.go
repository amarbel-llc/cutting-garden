// Package ajour reads the stencil stack's documents (cutting-garden FDR
// 0028; ajour(7)): a capability profile's body as a Stencil, an act's own
// body as a Patch, both in the stencil fragment of trellis (RFC 0014
// "Stencil fragment"). This is the parser half; the evaluator over a call is
// phase 2 (cutting-garden#346).
//
// It is real source in pkgs/, consuming pkgs/trellis as an out-of-tree
// plugin would, and imports nothing from internal/: a dagnabit copy facade
// over an internal/ajour would keep its internal/trellis import (checked
// 2026-11-10), so Node.Atoms would be a type no consumer outside this module
// can name. Its closure is pkgs/trellis, dewey's errors and the standard
// library.
package ajour

import (
	"regexp"
	"slices"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/trellis"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The tags ajour reads; every other tag is carried and inert.
const (
	TagDeny    = "deny"
	TagSubtree = "subtree"
	// TagOpen is the required spelling of a wildcard in a stencil: a typed
	// `_` node with no atoms must carry it (operator ruling 2026-11-10, FDR
	// 0028 open item 3). ajour checks its presence and nothing more.
	TagOpen = "open"
)

// A Layer is one document of the stack, chain start down: a Stencil or a
// Patch.
type Layer interface{ ajourLayer() }

// A Stencil is a capability profile's body: allowances (holes) unless a
// node carries `deny`.
type Stencil struct{ Roots []Node }

// A Patch is an act's own body: it only denies, so every leaf carries
// `deny`.
type Patch struct{ Roots []Node }

func (Stencil) ajourLayer() {}
func (Patch) ajourLayer()   {}

// A Node is one literal of the stencil fragment.
type Node struct {
	// ID is the real id, or a temp id's handle (empty for a bare `+`). It is
	// empty for the null id `_`.
	ID string
	// HasID is false for the null id `_` (a pattern over existing objects)
	// and for a temp id; true for a real id, including one literally named
	// `_` (spelled `"_"`).
	HasID bool
	// New marks a temp id `+`, `+id`, `+"id"`: a creation allowance, matched
	// only by a subject that does not exist yet.
	New bool
	// Lock is the digest of a locked id `name@<digest>`, empty when unlocked.
	Lock string
	Type string
	// Tags are every tag, deny and subtree included, in order. A `/**` id
	// suffix arrives here as a `subtree` tag.
	Tags    []string
	Deny    bool
	Subtree bool
	// Atoms are the field predicates in order, as pkgs/trellis parses them;
	// Negated[i] is atom i's `^` prefix.
	Atoms    []trellis.FieldPred
	Negated  []bool
	Children []Node

	// compiled[i][k] is atom i's value k compiled, for a `~=` atom; set at
	// parse so Evaluate never recompiles (nil for a Node built in code,
	// which Evaluate then compiles on demand).
	compiled [][]*regexp.Regexp
}

// ParseStencil parses src, in the canonical nested spelling or the heading
// rendering, as a stencil. Every refusal is a bad request naming the
// offending term.
func ParseStencil(src string) (Stencil, error) {
	roots, err := parseLayer(src, layerStencil)
	if err != nil {
		return Stencil{}, err
	}
	return Stencil{Roots: roots}, nil
}

// ParsePatch parses src as a patch: the stencil fragment, with every leaf
// carrying `deny`. A body with an allowance-shaped leaf is refused, naming
// the leaf; the act that carried it admits nothing (RFC 0013 §4.9).
func ParsePatch(src string) (Patch, error) {
	roots, err := parseLayer(src, layerPatch)
	if err != nil {
		return Patch{}, err
	}
	return Patch{Roots: roots}, nil
}

// Write writes the stencil in its canonical spelling, nested espalier:
// never a heading, never a `/**` suffix (trellis.WriteTree).
func (s Stencil) Write(b *strings.Builder) { writeNodes(b, s.Roots) }

// Write writes the patch in its canonical spelling, as Stencil.Write does.
func (p Patch) Write(b *strings.Builder) { writeNodes(b, p.Roots) }

// layerKind says which fragment conditions a parse applies on top of the
// shared ones: the wildcard rule (stencil) or the deny-only rule (patch).
type layerKind int

const (
	layerStencil layerKind = iota
	layerPatch
)

func (k layerKind) String() string {
	if k == layerPatch {
		return "patch"
	}
	return "stencil"
}

func parseLayer(src string, kind layerKind) ([]Node, error) {
	trees, err := trellis.ParseTree(src)
	if err != nil {
		return nil, errors.BadRequestf("%s: %w", kind, err)
	}
	roots := make([]Node, len(trees))
	for i, tree := range trees {
		node, err := projectNode(tree, kind)
		if err != nil {
			return nil, errors.BadRequestf("%s: %w", kind, err)
		}
		roots[i] = node
	}
	return roots, nil
}

// projectNode applies the stencil fragment's conditions beyond the literal
// tree's own grammar (FDR 0028 "The stencil fragment"), recursively.
func projectNode(tree trellis.TreeNode, kind layerKind) (Node, error) {
	node := Node{
		ID:    tree.ID,
		HasID: tree.Kind == trellis.IDReal,
		New:   tree.Kind == trellis.IDTemp,
		Lock:  tree.Lock,
		Type:  tree.Type,
		Tags:  slices.Clone(tree.Tags),
	}
	if tree.Digest != "" {
		return Node{}, errors.BadRequestf(
			"node %s: a standalone `@%s` term is not in the stencil fragment (a lock is `name@digest` in the id slot)",
			spellHead(tree), tree.Digest,
		)
	}
	if tree.Globstar && !slices.Contains(node.Tags, TagSubtree) {
		node.Tags = append(node.Tags, TagSubtree)
	}
	node.Deny = slices.Contains(node.Tags, TagDeny)
	node.Subtree = slices.Contains(node.Tags, TagSubtree)
	if err := checkPerm(tree, node.Deny, kind); err != nil {
		return Node{}, err
	}

	for _, atom := range tree.Atoms {
		compiled, err := checkAtom(atom)
		if err != nil {
			return Node{}, err
		}
		node.Atoms = append(node.Atoms, atom.Pred)
		node.Negated = append(node.Negated, atom.Negate)
		node.compiled = append(node.compiled, compiled)
	}
	if !slices.ContainsFunc(node.compiled, func(c []*regexp.Regexp) bool { return c != nil }) {
		node.compiled = nil // no `~=` atom: a parsed Node equals its code-built twin
	}

	switch kind {
	case layerStencil:
		// A `perm` atom decides and matches nothing, so it does not keep a
		// node from being a wildcard.
		conditions := slices.ContainsFunc(tree.Atoms, func(a trellis.TreeAtom) bool { return !isPermAtom(a.Pred) })
		if tree.Kind == trellis.IDNull && tree.Type != "" && !conditions &&
			!slices.Contains(node.Tags, TagOpen) {
			return Node{}, errors.BadRequestf(
				"node %s: a wildcard (a typed `_` node with no conditions) must carry the `open` tag",
				spellHead(tree),
			)
		}
	case layerPatch:
		if len(tree.Children) == 0 && !node.Deny {
			return Node{}, errors.BadRequestf(
				"a patch may only deny; leaf %s carries no deny", spellHead(tree),
			)
		}
	}

	for _, child := range tree.Children {
		c, err := projectNode(child, kind)
		if err != nil {
			return Node{}, err
		}
		node.Children = append(node.Children, c)
	}
	return node, nil
}

// checkAtom refuses what the fragment excludes from an atom: a
// framework-reserved `_` field name (unquoted; a quoted name is opaque), a
// `(qualifier)` value, and a regex that does not compile (V-D1-7d: a layer
// that errors has no holes). It returns a `~=` atom's values compiled, nil
// for any other operator.
func checkAtom(atom trellis.TreeAtom) ([]*regexp.Regexp, error) {
	refuse := func(why string, args ...any) error {
		return errors.BadRequestf("atom `%s`: "+why, append([]any{trellis.SpellTreeAtom(atom)}, args...)...)
	}
	if !atom.Pred.Field.Quoted && strings.HasPrefix(atom.Pred.Field.Name, "_") {
		return nil, refuse("a `_`-prefixed field name is framework-reserved")
	}
	var compiled []*regexp.Regexp
	for _, v := range atom.Pred.Values {
		if _, isQualifier := v.(trellis.Qualifier); isQualifier {
			return nil, refuse("a `(qualifier)` value is not in the stencil fragment")
		}
		if atom.Pred.Op == trellis.FieldOpRegex {
			re, err := regexp.Compile(trellis.ValueText(v))
			if err != nil {
				return nil, refuse("the regex does not compile: %v", err)
			}
			compiled = append(compiled, re)
		}
	}
	return compiled, nil
}

// spellHead spells a node without its children, `[id !type tags atoms]`,
// for a refusal to name.
func spellHead(tree trellis.TreeNode) string {
	tree.Children = nil
	var b strings.Builder
	trellis.WriteTree(&b, []trellis.TreeNode{tree})
	return strings.TrimSuffix(b.String(), "\n")
}

func writeNodes(b *strings.Builder, roots []Node) {
	trees := make([]trellis.TreeNode, len(roots))
	for i, n := range roots {
		trees[i] = toTree(n)
	}
	trellis.WriteTree(b, trees)
}

func toTree(n Node) trellis.TreeNode {
	// The Deny and Subtree bools decide the two reserved tags, so a Node
	// edited or built in code writes what its bools say.
	var tags []string
	for _, tag := range n.Tags {
		if (tag == TagDeny && !n.Deny) || (tag == TagSubtree && !n.Subtree) {
			continue
		}
		tags = append(tags, tag)
	}
	for _, reserved := range []struct {
		set bool
		tag string
	}{{n.Subtree, TagSubtree}, {n.Deny, TagDeny}} {
		if reserved.set && !slices.Contains(tags, reserved.tag) {
			tags = append(tags, reserved.tag)
		}
	}
	tree := trellis.TreeNode{
		ID: n.ID, Lock: n.Lock, Type: n.Type, Tags: tags,
	}
	switch {
	case n.New:
		tree.Kind = trellis.IDTemp
	case !n.HasID:
		tree.Kind = trellis.IDNull
	}
	for i, pred := range n.Atoms {
		negate := i < len(n.Negated) && n.Negated[i]
		tree.Atoms = append(tree.Atoms, trellis.TreeAtom{Negate: negate, Pred: pred})
	}
	for _, c := range n.Children {
		tree.Children = append(tree.Children, toTree(c))
	}
	return tree
}
