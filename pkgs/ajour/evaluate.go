package ajour

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/trellis"
)

// The verb field and its creation value: a creation path presents
// `op=create` on every step, and an `op=` atom is not checked against such a
// step, so the `+` node alone decides (FDR 0028 "Verbs", R 2026-11-10).
const (
	verbField  = "op"
	verbCreate = "create"
)

// A Call is one tool call as the enforcer presents it (FDR 0028 "The two
// inputs").
type Call struct {
	// Path runs from a root down to the dispatching node: plugin, server,
	// tool.
	Path []Subject
	// Values are the call's inputs, each independent and each a path walked
	// from the last Path node: [file] for a path field, [field] for an
	// input field, [recipe, arg…] for the recipe a run_recipe call runs.
	Values [][]Subject
	// Resources are the objects the call touches, each independent and each
	// a path walked from Path[Anchor]: [surface, root, container…, object]
	// and, for a patch, one more step per changed field.
	Resources [][]Subject
	// Anchor indexes the Path node resources hang under: the server.
	Anchor int
}

// A Subject is one step of a call's path.
type Subject struct {
	Type string
	// ID is "" when the subject has no id.
	ID string
	// New marks a creation: the object does not exist yet.
	New bool
	// Digest is the live catalog digest, when known.
	Digest string
	// Fields carries every field a stencil might name, the verb among them
	// (op=[patch]).
	Fields map[string][]string
}

// Kind is why a layer refused.
type Kind int

const (
	KindDeny Kind = iota
	KindNoAllowance
	KindLockMismatch
)

func (k Kind) String() string {
	switch k {
	case KindDeny:
		return "deny"
	case KindNoAllowance:
		return "no allowance"
	case KindLockMismatch:
		return "lock mismatch"
	}
	return "?"
}

// A Refusal is one layer refusing one subject.
type Refusal struct {
	// Layer indexes the layers Evaluate was given.
	Layer int
	Kind  Kind
	// Depth is 0 for the root; len(Path)+j for step j of a value;
	// Anchor+1+j for step j of a resource.
	Depth   int
	Subject Subject
	// Node is the spelled node, for KindDeny and KindLockMismatch.
	Node string

	layerKind layerKind
	at        string
}

// A Verdict is Evaluate's answer.
type Verdict struct {
	Admitted bool
	// Refusals lists every layer that refused, in stack order.
	Refusals []Refusal
}

// Evaluate checks call against the stencil stack (FDR 0028 "Evaluating a
// call against one layer", "The verdict"): admitted iff there is at least one
// stencil, every stencil admits every subject, and no layer denies any. Every
// layer is walked, so the verdict lists every refusal, in stack order.
func Evaluate(layers []Layer, call Call) Verdict {
	var v Verdict
	stencils := 0
	at := pathText(call.Path)
	for i, layer := range layers {
		kind, roots := layerParts(layer)
		if kind == layerStencil {
			stencils++
		}
		for _, r := range evaluateLayer(kind, roots, call) {
			r.Layer, r.layerKind, r.at = i, kind, at
			v.Refusals = append(v.Refusals, r)
		}
	}
	v.Admitted = stencils > 0 && len(v.Refusals) == 0
	return v
}

// String renders one line per refusal: `layer 2 (patch): deny [node] at
// moxy/folio/read`.
func (v Verdict) String() string {
	var b strings.Builder
	for _, r := range v.Refusals {
		fmt.Fprintf(&b, "layer %d (%s): ", r.Layer, r.layerKind)
		switch r.Kind {
		case KindNoAllowance:
			fmt.Fprintf(&b, "no allowance for {%s %s}", r.Subject.Type, r.Subject.ID)
		default:
			fmt.Fprintf(&b, "%s %s", r.Kind, r.Node)
		}
		fmt.Fprintf(&b, " at %s\n", r.at)
	}
	return b.String()
}

func layerParts(layer Layer) (layerKind, []Node) {
	switch l := layer.(type) {
	case Patch:
		return layerPatch, l.Roots
	case Stencil:
		return layerStencil, l.Roots
	}
	return layerStencil, nil
}

func pathText(path []Subject) string {
	ids := make([]string, len(path))
	for i, s := range path {
		ids[i] = s.ID
	}
	return strings.Join(ids, "/")
}

// A frontier is the set of nodes the next step's subject is matched against:
// the children of the last step's matches, plus every matched `subtree` node,
// which stays matched for the rest of the path by its atoms alone.
type frontier struct {
	nodes  []*Node
	sticky []*Node
	// lockShadow is the first node on this path that matched but for its
	// lock while another node (a wildcard) let the walk go on. When the walk
	// later finds no allowance, the lock is the refusal named, at its own
	// step, since it is what kept the path from its allowance
	// (cutting-garden#411, vector A-6).
	lockShadow *Refusal
}

func rootFrontier(roots []Node) frontier {
	var f frontier
	for i := range roots {
		f.nodes = append(f.nodes, &roots[i])
	}
	return f
}

// evaluateLayer walks the call through one layer: the path, then each value
// from the last path step and each resource from Path[Anchor],
// independently. A stencil refuses a subject no node matches; a patch is
// transparent there and stops walking that path.
func evaluateLayer(kind layerKind, roots []Node, call Call) []Refusal {
	front := rootFrontier(roots)
	var anchor *frontier
	reachedEnd := true
	for i, s := range call.Path {
		next, refusal, stopped := step(kind, front, s, i, false)
		if refusal != nil {
			return []Refusal{*refusal}
		}
		if stopped {
			reachedEnd = false
			break
		}
		front = next
		if i == call.Anchor {
			anchor = &next
		}
	}

	var refusals []Refusal
	walk := func(from frontier, steps []Subject, depth0 int, resource bool) {
		for j, s := range steps {
			next, refusal, stopped := step(kind, from, s, depth0+j, resource)
			if refusal != nil {
				refusals = append(refusals, *refusal)
				return
			}
			if stopped {
				return
			}
			from = next
		}
	}
	if reachedEnd {
		for _, value := range call.Values {
			walk(front, value, len(call.Path), false)
		}
	}
	switch {
	case anchor != nil:
		for _, resource := range call.Resources {
			walk(*anchor, resource, call.Anchor+1, true)
		}
	case reachedEnd && kind == layerStencil:
		// An Anchor that indexes no Path step leaves the resources nowhere
		// to hang: closed by default, a stencil admits none of them.
		for _, resource := range call.Resources {
			var s Subject
			if len(resource) > 0 {
				s = resource[0]
			}
			refusals = append(refusals, Refusal{Kind: KindNoAllowance, Depth: call.Anchor + 1, Subject: s})
		}
	}
	return refusals
}

// step matches subject s against front at depth: it refuses (a deny among
// the matches; no match in a stencil), stops (no match in a patch), or
// returns the next frontier. resource marks a step of a resource path, the
// only place a creation verb is presented.
func step(kind layerKind, front frontier, s Subject, depth int, resource bool) (frontier, *Refusal, bool) {
	creation := resource && slices.Contains(s.Fields[verbField], verbCreate)
	var matched, sticky []*Node
	var lockFailed *Node
	for _, n := range front.nodes {
		switch matchNode(n, s, creation) {
		case matchOK:
			matched = append(matched, n)
		case matchLockFailed:
			if lockFailed == nil {
				lockFailed = n
			}
		}
	}
	for _, n := range front.sticky {
		if !s.New && atomsHold(n, s, creation) {
			sticky = append(sticky, n)
		}
	}
	all := uniqueNodes(append(matched, sticky...))
	for _, n := range all {
		if n.Deny {
			return frontier{}, &Refusal{Kind: KindDeny, Depth: depth, Subject: s, Node: spellNode(n)}, false
		}
	}
	if len(all) == 0 {
		if kind == layerPatch {
			return frontier{}, nil, true
		}
		switch {
		case lockFailed != nil:
			return frontier{}, &Refusal{Kind: KindLockMismatch, Depth: depth, Subject: s, Node: spellNode(lockFailed)}, false
		case front.lockShadow != nil:
			return frontier{}, front.lockShadow, false
		}
		return frontier{}, &Refusal{Kind: KindNoAllowance, Depth: depth, Subject: s}, false
	}
	next := frontier{lockShadow: front.lockShadow}
	if next.lockShadow == nil && lockFailed != nil {
		next.lockShadow = &Refusal{Kind: KindLockMismatch, Depth: depth, Subject: s, Node: spellNode(lockFailed)}
	}
	for _, n := range all {
		for i := range n.Children {
			next.nodes = append(next.nodes, &n.Children[i])
		}
	}
	next.sticky = sticky
	for _, n := range matched {
		if n.Subtree {
			next.sticky = append(next.sticky, n)
		}
	}
	next.nodes, next.sticky = uniqueNodes(next.nodes), uniqueNodes(next.sticky)
	return next, nil, false
}

// uniqueNodes drops repeats, keeping first-seen order: a subtree node can be
// both a sticky survivor and a child of another sticky node.
func uniqueNodes(nodes []*Node) []*Node {
	seen := make(map[*Node]bool, len(nodes))
	out := nodes[:0:0]
	for _, n := range nodes {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

type matchResult int

const (
	matchNo matchResult = iota
	matchOK
	matchLockFailed
)

// matchNode is FDR 0028 "Matching one node against one subject": type, id,
// atoms, then the lock (a node that matches but for its lock is reported as
// a lock mismatch when nothing else matches).
func matchNode(n *Node, s Subject, creation bool) matchResult {
	if n.Type != "" && n.Type != s.Type {
		return matchNo
	}
	switch {
	case n.New:
		if !s.New {
			return matchNo
		}
	case s.New:
		return matchNo
	case n.HasID:
		if n.ID != s.ID && !(n.Subtree && segmentPrefix(n.ID, s.ID)) {
			return matchNo
		}
	}
	if !atomsHold(n, s, creation) {
		return matchNo
	}
	if n.Lock != "" && s.Digest != n.Lock {
		return matchLockFailed
	}
	return matchOK
}

// segmentPrefix reports that prefix contains id by path segment: equal, or
// followed by `/` in id (`file:/wt` contains `file:/wt/a`, not `file:/wtx`).
func segmentPrefix(prefix, id string) bool {
	return id == prefix || strings.HasPrefix(id, prefix+"/")
}

// atomsHold reports that every atom of n holds against s's fields; an `op=`
// atom is skipped on a creation step (a resource step presenting
// `op=create`).
func atomsHold(n *Node, s Subject, creation bool) bool {
	for i, fp := range n.Atoms {
		if creation && !fp.Field.Quoted && fp.Field.Name == verbField {
			continue
		}
		var compiled []*regexp.Regexp
		if i < len(n.compiled) {
			compiled = n.compiled[i]
		}
		negated := i < len(n.Negated) && n.Negated[i]
		if !atomHolds(fp, negated, compiled, s.Fields[fp.Field.Name]) {
			return false
		}
	}
	return true
}

// atomHolds is rule 4: a positive operator holds when at least one of the
// field's values satisfies it (a value list distributing as OR); `!=` and a
// `^`-negated atom hold when every value fails the positive form; an atom
// naming a field the subject lacks never holds.
func atomHolds(fp trellis.FieldPred, negated bool, compiled []*regexp.Regexp, values []string) bool {
	if len(values) == 0 {
		return false
	}
	op := fp.Op
	universal := negated
	if op == trellis.FieldOpNotEq {
		op, universal = trellis.FieldOpEq, !universal
	}
	patterns := make([]*regexp.Regexp, len(fp.Values))
	if op == trellis.FieldOpRegex {
		for k, want := range fp.Values {
			re := regexpFor(trellis.ValueText(want), compiled, k)
			if re == nil {
				// A pattern that does not compile (a Node built in code; a
				// parsed one was refused) never holds, negated or not.
				return false
			}
			patterns[k] = re
		}
	}
	matchesAny := func(got string) bool {
		for k, want := range fp.Values {
			if satisfies(op, got, trellis.ValueText(want), patterns[k]) {
				return true
			}
		}
		return false
	}
	if universal {
		return !slices.ContainsFunc(values, matchesAny)
	}
	return slices.ContainsFunc(values, matchesAny)
}

// satisfies applies op to one subject value got and one predicate value
// want; re is want compiled, when op is `~=` and the parse compiled it. The
// semantics are trellis_eval's (vector A-16): bytes compare, RE2 unanchored.
func satisfies(op trellis.FieldOp, got, want string, re *regexp.Regexp) bool {
	if op != trellis.FieldOpRegex {
		return trellis.CompareFieldOp(op, got, want)
	}
	if re == nil {
		var err error
		if re, err = regexp.Compile(want); err != nil {
			return false
		}
	}
	return re.MatchString(got)
}

// regexpFor is pattern compiled: the parse-time compile when it is still
// pattern (an atom edited after parse no longer lines up), else a fresh
// compile; nil when pattern does not compile.
func regexpFor(pattern string, compiled []*regexp.Regexp, k int) *regexp.Regexp {
	if k < len(compiled) && compiled[k] != nil && compiled[k].String() == pattern {
		return compiled[k]
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}
	return re
}

func spellNode(n *Node) string {
	head := *n
	head.Children = nil
	return spellHead(toTree(head))
}
