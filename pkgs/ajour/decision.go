package ajour

import (
	"code.linenisgreat.com/cutting-garden/pkgs/trellis"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The decision field (FDR 0028 "Facts are pins, perm is a decision"): an
// unquoted `perm=` atom is never matched against a subject. It says what the
// node decides for the calls whose walk matched it.
const (
	permField   = "perm"
	permAllow   = "allow"
	permAsk     = "ask"
	permDynamic = "dynamic"
)

// Outcome is the verdict's short answer. Its zero value refuses, so an
// outcome nobody set never runs a call.
type Outcome int

const (
	OutcomeRefused Outcome = iota
	OutcomeAsk
	OutcomeAdmitted
)

func (o Outcome) String() string {
	switch o {
	case OutcomeAsk:
		return "ask"
	case OutcomeAdmitted:
		return "admitted"
	}
	return "refused"
}

// Perm is a node's decision, and an admitted verdict's: PermDefault leaves
// the call to the enforcer's own tier (no `perm` atom, or `perm=dynamic`),
// PermAllow says the enforcer skips its ask tier for the call (and nothing
// more: it never admits what a layer refuses, and a dynamic tool's
// predicate stays the enforcer's floor), PermAsk admits subject to a human.
type Perm int

const (
	PermDefault Perm = iota
	PermAllow
	PermAsk
)

func (p Perm) String() string {
	switch p {
	case PermAllow:
		return permAllow
	case PermAsk:
		return permAsk
	}
	return "default"
}

// rank orders decisions for "strongest wins": allow < default < ask (a deny
// is a refusal and outranks them all).
func (p Perm) rank() int {
	switch p {
	case PermAllow:
		return 0
	case PermAsk:
		return 2
	}
	return 1
}

// An Ask is one node that said `perm=ask` on the call's walk through a layer
// that admitted the call.
type Ask struct {
	// Layer indexes the layers Evaluate was given.
	Layer int
	// Depth is the step the node first matched, numbered as Refusal.Depth.
	Depth   int
	Subject Subject
	// Node is the spelled node.
	Node string

	layerKind layerKind
	at        string
}

// Perm is the node's decision: its `perm` atom read, or PermDefault when it
// has none. A Node built in code is read the same way, from its atoms.
func (n Node) Perm() Perm {
	p, _ := nodePerm(&n)
	return p
}

// nodePerm reads n's `perm` atom; ok is false when n carries none.
func nodePerm(n *Node) (Perm, bool) {
	for _, fp := range n.Atoms {
		if !isPermAtom(fp) {
			continue
		}
		if len(fp.Values) != 1 {
			return PermDefault, true
		}
		switch trellis.ValueText(fp.Values[0]) {
		case permAllow:
			return PermAllow, true
		case permAsk:
			return PermAsk, true
		}
		return PermDefault, true
	}
	return PermDefault, false
}

func isPermAtom(fp trellis.FieldPred) bool {
	return !fp.Field.Quoted && fp.Field.Name == permField
}

// checkPerm refuses a `perm` atom the fragment excludes: anything but one
// `perm=allow|ask|dynamic` per node, not negated, never beside `deny`, and
// none in a patch (a patch only refuses; asking from a patch awaits a
// ruling).
func checkPerm(tree trellis.TreeNode, deny bool, kind layerKind) error {
	seen := false
	for _, atom := range tree.Atoms {
		if !isPermAtom(atom.Pred) {
			continue
		}
		spelled := trellis.SpellTreeAtom(atom)
		refuse := func(why string) error {
			return errors.BadRequestf("atom `%s`: %s", spelled, why)
		}
		switch {
		case kind == layerPatch:
			return refuse("a patch may only deny; `perm` decides, so it belongs in a stencil")
		case seen:
			return refuse("a node carries one `perm` atom")
		case atom.Negate:
			return refuse("`perm` is a decision, not a condition, so it cannot be negated")
		case atom.Pred.Op != trellis.FieldOpEq:
			return refuse("`perm` takes `=` only")
		case len(atom.Pred.Values) != 1:
			return refuse("`perm` takes one value")
		}
		switch trellis.ValueText(atom.Pred.Values[0]) {
		case permAllow, permAsk, permDynamic:
		default:
			return refuse("`perm` is allow, ask or dynamic")
		}
		if deny {
			return refuse("a node that carries `deny` decides nothing else")
		}
		seen = true
	}
	return nil
}

// Posture is the session's: attended (a human can answer a prompt) or
// unattended. It is the enforcer's input to Resolve, never Evaluate's.
type Posture int

const (
	PostureAttended Posture = iota
	PostureUnattended
)

func (p Posture) String() string {
	if p == PostureUnattended {
		return "unattended"
	}
	return "attended"
}

// Action is what an enforcer does with a verdict. Its zero value refuses.
type Action int

const (
	ActionRefuse Action = iota
	ActionRun
	ActionPrompt
	ActionEscalate
)

func (a Action) String() string {
	switch a {
	case ActionRun:
		return "run"
	case ActionPrompt:
		return "prompt"
	case ActionEscalate:
		return "escalate"
	}
	return "refuse"
}

// Resolve maps the verdict onto the session posture, one mapping for every
// enforcer: an admitted call runs, a refused one is refused, and an Ask
// prompts a human when the session is attended. Unattended, it escalates
// only when the enforcer has a channel to the operator (canEscalate: a chat
// or a card), and is refused otherwise, so an Ask never becomes a run that
// nobody answered.
func (v Verdict) Resolve(posture Posture, canEscalate bool) Action {
	switch v.Outcome {
	case OutcomeAdmitted:
		return ActionRun
	case OutcomeAsk:
		switch {
		case posture == PostureAttended:
			return ActionPrompt
		case canEscalate:
			return ActionEscalate
		}
	}
	return ActionRefuse
}

// layerDecision accumulates one layer's decision over the nodes its walk
// matched: the strongest explicit `perm`, and every asking node once.
type layerDecision struct {
	explicit bool
	perm     Perm
	asks     []Ask
	asked    map[*Node]bool
}

func (d *layerDecision) note(n *Node, s Subject, depth int) {
	p, ok := nodePerm(n)
	if !ok {
		return
	}
	if !d.explicit || p.rank() > d.perm.rank() {
		d.explicit, d.perm = true, p
	}
	if p != PermAsk || d.asked[n] {
		return
	}
	if d.asked == nil {
		d.asked = map[*Node]bool{}
	}
	d.asked[n] = true
	d.asks = append(d.asks, Ask{Depth: depth, Subject: s, Node: spellNode(n)})
}
