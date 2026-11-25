// Package mutate is the write engine behind both the `mutate_nodes` MCP tool
// (cutting-garden#315, FDR 0020) and the `mutate` command: one request shape
// (Input, a list of groups), one dispatch path, one result shape. It speaks
// no protocol — internal/mcp wraps it in a tool, Cmd wraps it in a
// subcommand — so a write means the same thing from either front and a
// behavior fix lands once.
//
// A call is a list of groups, each an anchor plus its ops (or a sweep),
// applied in order and independently, so one call can span several anchors
// and plugins (cutting-garden#323). Ops address nodes by the id organize
// prints for them, relative to the group's anchor (node_view.NodeURIFor), and
// a create may name a temp id (`+name`) that later ops in the group address.
package mutate

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/node_view"
	"code.linenisgreat.com/cutting-garden/internal/trellis_eval"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Input is one call: groups applied in order, plus the call-wide strict flag
// (cutting-garden#404): when true a patch op naming a key its node type does
// not accept is refused before anything is written for it.
type Input struct {
	Strict bool         `json:"strict"`
	Groups []GroupInput `json:"groups"`
}

// OpInput is one caller op. ID is the node as organize prints it under the
// anchor, a full URI under it, or a temp id (+name, +"name", bare +) on a
// create.
type OpInput struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	In   string `json:"in"`
	Body string `json:"body"`
	Type string `json:"type"`
}

// SweepInput applies one op to every node under the anchor matching Filter.
type SweepInput struct {
	Filter string  `json:"filter"`
	Op     OpInput `json:"op"`
}

// GroupInput is one anchor's changeset: exactly one of Ops or Sweep.
type GroupInput struct {
	Anchor    string      `json:"anchor"`
	Atomicity string      `json:"atomicity"`
	Ops       []OpInput   `json:"ops"`
	Sweep     *SweepInput `json:"sweep"`
}

// FailureView is one failed op in the caller's id.
type FailureView struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

// CreatedView is one created node: the caller's temp id, the id organize
// would print for it, and its URI.
type CreatedView struct {
	TempID string `json:"temp_id"`
	ID     string `json:"id"`
	URI    string `json:"uri"`
}

// OutcomeView is one op's outcome in the caller's id: Status is the
// canonical reading (applied, patched_nothing, failed, deleted, not_found; a
// deleted node is also in applied, a not_found one also in failed). Fields
// are the keys a patch applied; URI is the node's address afterwards, present
// only when it changed; Name is a deleted node's name.
type OutcomeView struct {
	ID     string   `json:"id"`
	Status string   `json:"status"`
	Fields []string `json:"fields,omitempty"`
	// Ignored names the patch body's keys the plugin tolerated but did not
	// apply (cutting-garden#376).
	Ignored []string `json:"ignored,omitempty"`
	URI     string   `json:"uri,omitempty"`
	Name    string   `json:"name,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// GroupView is one group's result.
type GroupView struct {
	Anchor         string        `json:"anchor"`
	Applied        []string      `json:"applied"`
	PatchedNothing []string      `json:"patched_nothing,omitempty"`
	Failed         []FailureView `json:"failed,omitempty"`
	Created        []CreatedView `json:"created,omitempty"`
	Outcomes       []OutcomeView `json:"outcomes,omitempty"`
	Atomic         bool          `json:"atomic,omitempty"`
	// Error is the reason a whole group was refused at dispatch (an atomic
	// batch the plugin cannot transact, a sweep it cannot resolve), in a
	// call of several groups; the other groups still ran.
	Error string `json:"error,omitempty"`
	// Complete and Partial mark a sweep whose enumeration a plugin reported
	// bounded — the same signal list_nodes carries (cutting-garden#266).
	Complete *bool                `json:"complete,omitempty"`
	Partial  []cgp.PartialListing `json:"partial,omitempty"`
}

// Engine applies Inputs against the plugin registry. Its fields are the
// seams: production wires the command_components resolvers, tests plug in
// fakes. ResolveBulk and Forget may be nil.
type Engine struct {
	// ResolveLister resolves the RootLister for an anchor (required).
	ResolveLister func(uri string) (*url.URL, cgp.RootLister, error)
	// ResolveNode resolves the NodeMutator a plugin with only single-node
	// writes is driven through (required).
	ResolveNode func(uri string) (*url.URL, cgp.NodeMutator, error)
	// ResolveBulk resolves the BulkMutator (RFC 0017), preferred over
	// ResolveNode when the plugin has one.
	ResolveBulk func(uri string) (*url.URL, cgp.BulkMutator, error)
	// TagsOverride is the global `[tags] interpreter` config override a
	// sweep filter's residual bare-tag term resolves through (RFC 0019 §4).
	TagsOverride string
	// Log receives each op's outcome line (cutting-garden#392) so a reply
	// lost with its connection leaves a record of what applied. nil is
	// silent.
	Log io.Writer
	// Forget is called with the anchor after every dispatch — even a failed
	// one, which may have applied part of a batch — so a read surface that
	// memoizes can drop its caches for the written source.
	Forget func(anchor string)
}

// Result is a whole call's outcome: one GroupView per group, in order.
type Result struct {
	Groups []GroupView `json:"groups"`
}

// NothingApplied reports a call where no group applied anything yet
// something was refused — the case a caller must not read as done
// (cutting-garden#180). An empty-handed call with nothing refused (a sweep
// that matched no node) is not "nothing applied": it is a clean no-op.
func (r Result) NothingApplied() bool {
	return !anyApplied(r.Groups) && anyRefused(r.Groups)
}

// mutateGroup is one group resolved against its plugin: the domain request
// plus what rendering needs to report results in the caller's own ids.
type mutateGroup struct {
	anchor string
	lister cgp.RootLister
	req    cgp.BulkRequest
	// strict is the call's strict flag (cutting-garden#404): see screenPatches.
	strict   bool
	idOf     map[string]string // node URI → the id the caller sent
	spelling map[string]string // temp key → the caller's spelling
	// residual is the sweep filter's host-side remainder (RFC 0012 §6); when
	// non-empty the host resolves the sweep itself (resolveResidualSweep).
	residual trellis_eval.Residual
}

// Apply builds every group, then dispatches them in order. Every group is
// built before any is dispatched, so a malformed group refuses the whole call
// with nothing written (cutting-garden#323). via is an audit suffix for the
// per-op log lines (a serve-mcp caller's principal), "" otherwise.
func (e *Engine) Apply(ctx context.Context, in Input, via string) (Result, error) {
	if len(in.Groups) == 0 {
		return Result{}, errors.BadRequestf("mutate_nodes: groups is empty")
	}

	groups := make([]*mutateGroup, 0, len(in.Groups))
	for i, groupIn := range in.Groups {
		group, err := e.buildGroup(groupIn, in.Strict)
		if err != nil {
			if len(in.Groups) > 1 {
				return Result{}, errors.BadRequestf("groups[%d]: %s", i, err)
			}
			return Result{}, err
		}
		groups = append(groups, group)
	}

	result := Result{Groups: make([]GroupView, 0, len(groups))}
	for _, group := range groups {
		view, err := e.applyGroup(ctx, group, via)
		if err != nil && len(groups) == 1 {
			return Result{}, err
		}
		if err != nil {
			view = GroupView{Anchor: group.anchor, Applied: []string{}, Error: err.Error()}
		}
		result.Groups = append(result.Groups, view)
	}

	return result, nil
}

// applyGroup dispatches one built group and renders its result. Groups are
// independent: each has its own temp ids and atomicity, and one group's
// refusal does not stop the next.
func (e *Engine) applyGroup(
	ctx context.Context, group *mutateGroup, via string,
) (GroupView, error) {
	ctx, partial := cgp.WithPartialListings(ctx)
	ctx = cgp.WithBulkOutcomeObserver(ctx, group.logOutcome(e.Log, via))
	result, err := e.dispatch(ctx, group)
	if e.Forget != nil {
		e.Forget(group.anchor)
	}
	if err != nil {
		return GroupView{}, err
	}
	return group.render(result, partial.All()), nil
}

func anyApplied(views []GroupView) bool {
	for _, v := range views {
		if len(v.Applied) > 0 {
			return true
		}
	}
	return false
}

func anyRefused(views []GroupView) bool {
	for _, v := range views {
		if v.Error != "" || len(v.Failed) > 0 || len(v.PatchedNothing) > 0 {
			return true
		}
	}
	return false
}

func (e *Engine) buildGroup(in GroupInput, strict bool) (*mutateGroup, error) {
	if in.Anchor == "" {
		return nil, errors.BadRequestf("mutate_nodes: group has no anchor")
	}
	if strict && in.Sweep != nil &&
		cgp.BulkOpKind(in.Sweep.Op.Kind) == cgp.BulkPatch {
		return nil, errors.BadRequestf(
			"mutate_nodes: strict does not apply to a patch sweep yet (its" +
				" matches' node types are not known before the write): send the" +
				" patches as ops, or drop strict")
	}
	anchorURL, lister, err := e.ResolveLister(in.Anchor)
	if err != nil {
		return nil, err
	}

	group := &mutateGroup{
		anchor:   in.Anchor,
		lister:   lister,
		strict:   strict,
		idOf:     make(map[string]string),
		spelling: make(map[string]string),
		req: cgp.BulkRequest{
			Atomicity: cgp.BulkAtomicity(in.Atomicity),
		},
	}
	if in.Atomicity == "" {
		group.req.Atomicity = cgp.BulkBestEffort
	}

	if in.Sweep != nil {
		if len(in.Ops) > 0 {
			return nil, errors.BadRequestf(
				"mutate_nodes: a group takes exactly one of ops or sweep",
			)
		}
		filter, residual, err := trellis_eval.SplitFilterFor(in.Sweep.Filter, lister)
		if err != nil {
			return nil, errors.BadRequestf("mutate_nodes: sweep filter: %s", err)
		}
		group.residual = residual
		group.req.Sweep = &cgp.BulkSweep{
			Root:   anchorURL,
			Filter: filter,
			Op: cgp.BulkOp{
				Kind: cgp.BulkOpKind(in.Sweep.Op.Kind),
				Body: []byte(in.Sweep.Op.Body),
			},
		}
	}

	defined := make(map[string]bool)
	for i, op := range in.Ops {
		built, err := group.buildOp(i, op, defined)
		if err != nil {
			return nil, err
		}
		group.req.Ops = append(group.req.Ops, built)
	}

	if err := group.req.Validate(); err != nil {
		return nil, err
	}

	return group, nil
}

// buildOp turns one caller op into a BulkOp: a temp id becomes a TempID
// create (when it is new) or a Ref (when an earlier create defined it); any
// other id resolves to the node URI under the anchor.
func (g *mutateGroup) buildOp(
	i int, in OpInput, defined map[string]bool,
) (cgp.BulkOp, error) {
	op := cgp.BulkOp{
		Kind: cgp.BulkOpKind(in.Kind),
		Type: in.Type,
	}
	op.Body = []byte(in.Body)

	key, isTemp, err := tempKey(i, in.ID)
	if err != nil {
		return op, errors.BadRequestf("mutate_nodes: ops[%d]: %s", i, err)
	}

	if op.Kind == cgp.BulkCreate && op.Type != "" {
		if err := g.checkCreateType(i, op.Type, isTemp); err != nil {
			return op, err
		}
	}

	switch {
	case isTemp && !defined[key]:
		if op.Kind != cgp.BulkCreate {
			return op, errors.BadRequestf(
				"mutate_nodes: ops[%d] %s %s: no earlier create defines it",
				i, in.Kind, in.ID,
			)
		}
		defined[key] = true
		g.spelling[key] = in.ID
		op.TempID = key
		if err := g.setContainer(i, &op, in.In, defined); err != nil {
			return op, err
		}

	case isTemp:
		if op.Kind == cgp.BulkCreate {
			return op, errors.BadRequestf(
				"mutate_nodes: ops[%d] creates %s again", i, in.ID,
			)
		}
		op.Ref = key

	default:
		if in.In != "" {
			return op, errors.BadRequestf(
				"mutate_nodes: ops[%d]: in applies only to a temp-id create", i,
			)
		}
		uri, err := node_view.NodeURIFor(g.lister, in.ID, g.anchor)
		if err != nil {
			return op, errors.BadRequestf("mutate_nodes: ops[%d]: %s", i, err)
		}
		op.URI = uri
		g.idOf[uri.String()] = in.ID
	}

	return op, nil
}

// checkCreateType refuses a create the plugin's describe_node_types says
// cannot work, before anything is sent: a type it does not list as writable,
// or a caller-chosen id for a type whose source assigns identity (jira's
// issue keys) — that one needs a temp id. A plugin with no BodyDescriber
// declares nothing, so its creates go through unchecked.
func (g *mutateGroup) checkCreateType(i int, typeTag string, isTemp bool) error {
	describer, ok := g.lister.(cgp.BodyDescriber)
	if !ok {
		return nil
	}

	bodies := describer.DescribeBodies()
	writable := make([]string, 0, len(bodies))
	for _, body := range bodies {
		if body.Tag != typeTag {
			writable = append(writable, body.Tag)
			continue
		}
		if body.ServerAssignedIdentity && !isTemp {
			return errors.BadRequestf(
				"mutate_nodes: ops[%d]: the source names %s nodes; create it"+
					" with a temp id (+name) instead of an id, in the container it"+
					" belongs under (the group's anchor, or the op's in): %s",
				i, typeTag, createInHint(body),
			)
		}
		return nil
	}

	return errors.BadRequestf(
		"mutate_nodes: ops[%d]: type %q is not creatable here (writable"+
			" types: %s)", i, typeTag, strings.Join(writable, ", "),
	)
}

// createInHint names the container a server-assigned type is created under:
// the plugin's own hint when it declares one, else a pointer to
// describe_node_types, which carries it as createIn when there is one.
func createInHint(body cgp.NodeTypeBody) string {
	if body.CreateIn != "" {
		return body.CreateIn
	}
	return "a container of the source, never the new node's own URI (describe_node_types lists" +
		" the writable types; this source does not say which container)"
}

// setContainer fills a temp-id create's container: the anchor by default,
// an id under it, or an earlier create's temp id.
func (g *mutateGroup) setContainer(
	i int, op *cgp.BulkOp, in string, defined map[string]bool,
) error {
	if in == "" {
		anchor, err := url.Parse(g.anchor)
		if err != nil {
			return errors.BadRequestf("mutate_nodes: anchor %q: %s", g.anchor, err)
		}
		op.In = anchor
		return nil
	}

	key, isTemp, err := tempKey(-1, in)
	if err != nil {
		return errors.BadRequestf("mutate_nodes: ops[%d] in: %s", i, err)
	}
	if isTemp {
		if !defined[key] || key == op.TempID {
			return errors.BadRequestf(
				"mutate_nodes: ops[%d] in %s: no earlier create defines it", i, in,
			)
		}
		op.InRef = key
		return nil
	}

	container, err := node_view.NodeURIFor(g.lister, in, g.anchor)
	if err != nil {
		return errors.BadRequestf("mutate_nodes: ops[%d] in: %s", i, err)
	}
	op.In = container
	return nil
}

// tempKey recognizes a temp id — `+name`, `+"name"`, or bare `+` (RFC 0015
// §Creation's spellings) — and returns the key BulkOp carries: `+name` for a
// named one, and for a bare `+`, which nothing can refer to, a key unique to
// its op index i (i < 0 when a bare `+` is not allowed).
func tempKey(i int, id string) (key string, isTemp bool, err error) {
	rest, ok := strings.CutPrefix(id, "+")
	if !ok {
		return "", false, nil
	}

	switch {
	case rest == "":
		if i < 0 {
			return "", true, fmt.Errorf("a bare + cannot be referred to")
		}
		return fmt.Sprintf("+\x00%d", i), true, nil
	case strings.HasPrefix(rest, `"`):
		name, err := strconv.Unquote(rest)
		if err != nil || name == "" {
			return "", true, fmt.Errorf("malformed temp id %s", id)
		}
		return "+" + name, true, nil
	default:
		return id, true, nil
	}
}

// dispatch sends the request to the plugin's BulkMutator, or — for a plugin
// with only single-node writes — applies it through the SDK's best-effort
// loop over its NodeMutator, which reports the same per-op result (an atomic
// request is refused there).
func (e *Engine) dispatch(
	ctx context.Context, g *mutateGroup,
) (cgp.BulkResult, error) {
	var result cgp.BulkResult
	var err error

	if g.req.Sweep != nil && !g.residual.Empty() {
		if err := e.resolveResidualSweep(ctx, g); err != nil {
			return result, err
		}
		if len(g.req.Ops) == 0 {
			return result, nil
		}
	}

	req := g.req
	var (
		fullOps []cgp.BulkOp
		screen  patchScreen
	)
	if len(req.Ops) > 0 {
		fullOps = slices.Clone(req.Ops)
		screen, err = screenPatches(
			fullOps, patchKeysFor(g.lister), g.strict,
			req.Atomicity == cgp.BulkAtomic,
		)
		if err != nil {
			return result, err
		}

		req.Ops = make([]cgp.BulkOp, 0, len(screen.dispatched))
		for _, i := range screen.dispatched {
			req.Ops = append(req.Ops, fullOps[i])
		}

		// Every op was settled by the host: there is nothing to dispatch.
		if len(req.Ops) == 0 {
			mergeScreen(&result, fullOps, screen)
			return result, nil
		}
	}

	if bulk := e.bulkMutatorFor(g.anchor); bulk != nil {
		result, err = bulk.BulkMutate(ctx, req)
	} else {
		_, nm, rerr := e.ResolveNode(g.anchor)
		if rerr != nil {
			return result, errors.BadRequestf(
				"mutate_nodes: the plugin for %q cannot write: %s", g.anchor, rerr,
			)
		}
		enriched, _ := g.lister.(cgp.EnrichedLister)
		if g.req.Sweep != nil && enriched == nil {
			return result, errors.BadRequestf(
				"mutate_nodes: the plugin for %q cannot sweep (it has no"+
					" filtered listing); send ops instead", g.anchor,
			)
		}
		result, err = cgp.BestEffortBulkMutate(ctx, nm, enriched, req)
	}

	if errors.Is(err, cgp.ErrBulkAtomicUnsupported) {
		return result, errors.BadRequestf(
			"mutate_nodes: the plugin for %q cannot apply a batch atomically;"+
				" omit atomicity for best-effort", g.anchor,
		)
	}
	if err == nil && fullOps != nil {
		mergeScreen(&result, fullOps, screen)
	}
	return result, err
}

// resolveResidualSweep turns a sweep whose filter has a residual into the
// explicit changeset of the nodes it matches (RFC 0012 §6). A plugin is sent
// only the pushdown, so it cannot be trusted to enumerate the match set: the
// host lists under the pushdown, re-checks both halves on every listed node,
// and writes only those. As for any sweep, a plugin without an enriched
// listing, or one that declines at this level, is refused rather than widened
// to every child. This is the one case RFC 0017 §Selection's "the plugin owns
// the enumeration" does not cover: the plugin cannot evaluate the residual.
func (e *Engine) resolveResidualSweep(ctx context.Context, g *mutateGroup) error {
	sweep := g.req.Sweep
	enriched, ok := g.lister.(cgp.EnrichedLister)
	if !ok {
		return errors.BadRequestf(
			"mutate_nodes: the plugin for %q cannot sweep (it has no filtered listing); send ops instead",
			g.anchor)
	}
	listed, handled, err := enriched.ListEnriched(ctx, sweep.Root, sweep.Filter)
	if err != nil {
		return err
	}
	if !handled {
		return errors.BadRequestf(
			"mutate_nodes: cannot sweep %q: the enriched listing declined at this level"+
				" (its children are containers) — sweep each child container instead", sweep.Root)
	}
	aliases := cgp.NoValueAliasesFor(g.lister)
	match := g.residual.Matcher(g.lister, sweep.Root, trellis_eval.WithTagsInterpreter(e.TagsOverride))
	var ops []cgp.BulkOp
	for _, n := range listed {
		if n.URI == nil || !sweep.Filter.MatchesNode(aliases, n.Type, n.Facets) {
			continue
		}
		matched, err := match(ctx, n)
		if err != nil {
			return err
		}
		if matched {
			op := sweep.Op
			op.URI = n.URI
			ops = append(ops, op)
		}
	}
	g.req.Sweep = nil
	g.req.Ops = ops
	return nil
}

func (e *Engine) bulkMutatorFor(anchor string) cgp.BulkMutator {
	if e.ResolveBulk == nil {
		return nil
	}
	if _, bulk, err := e.ResolveBulk(anchor); err == nil {
		return bulk
	}
	return nil
}

// CanWrite reports whether the plugin for uri can write at all —
// BulkMutator, or NodeMutator through the best-effort fallback. The mcp
// server advertises mutate_nodes only where some root can.
func (e *Engine) CanWrite(uri string) bool {
	if e.bulkMutatorFor(uri) != nil {
		return true
	}
	_, _, err := e.ResolveNode(uri)
	return err == nil
}

// render reports the result in the caller's own ids: the id an op was sent
// with, else the id organize would print (sweep matches, created nodes).
func (g *mutateGroup) render(
	result cgp.BulkResult, partial []cgp.PartialListing,
) GroupView {
	view := GroupView{
		Anchor:  g.anchor,
		Applied: make([]string, 0, len(result.AppliedNodes)),
		Atomic:  result.Atomic,
	}
	// Marks a sweep whose enumeration a plugin reported bounded; the zero
	// value (fields omitted) otherwise.
	if len(partial) > 0 {
		complete := false
		view.Complete = &complete
		view.Partial = partial
	}

	for _, uri := range result.AppliedNodes {
		view.Applied = append(view.Applied, g.id(uri))
	}
	for _, uri := range result.PatchedNothing {
		view.PatchedNothing = append(view.PatchedNothing, g.id(uri))
	}
	for _, failure := range result.Failed {
		id := g.spelling[failure.Ref]
		if id == "" && failure.Ref != "" {
			id = failure.Ref
		}
		if id == "" {
			id = g.id(failure.URI)
		}
		view.Failed = append(view.Failed, FailureView{
			ID: id, Error: failure.Err,
		})
	}
	for _, created := range result.Created {
		if created.URI == nil {
			continue
		}
		view.Created = append(view.Created, CreatedView{
			TempID: g.spelling[created.TempID],
			ID:     g.id(created.URI),
			URI:    created.URI.String(),
		})
	}

	view.Outcomes = g.outcomes(result)

	return view
}

// outcomes is the per-op detail the plugin reported, one entry per op in
// request order; nil — and so no outcomes key — when it reported none, in
// which case the four lists above are all there is (rebuilding a status-only
// entry per id from them would only repeat them).
func (g *mutateGroup) outcomes(result cgp.BulkResult) []OutcomeView {
	var out []OutcomeView
	for _, o := range result.Outcomes {
		out = append(out, g.outcomeView(o))
	}
	return out
}

func (g *mutateGroup) outcomeView(o cgp.BulkOutcome) OutcomeView {
	v := OutcomeView{
		Status: string(o.Status), Fields: o.Fields, Ignored: o.Ignored,
		Name: o.Name, Error: o.Err,
	}
	if o.NewURI != nil {
		v.URI = o.NewURI.String()
	}
	switch {
	case o.URI != nil:
		v.ID = g.id(o.URI)
	case o.Ref != "":
		v.ID = g.spelling[o.Ref]
		if v.ID == "" {
			v.ID = o.Ref
		}
	}
	return v
}

// logOutcome writes each op's outcome to log as the plugin's best-effort
// loop records it, so a reply lost with its connection (cutting-garden#392)
// leaves a record of what applied. A sweep's op count is not known ahead,
// so it logs "op N". via is the caller suffix of a serve-mcp connection (an
// audit aid with a self-asserted principal), "" otherwise.
func (g *mutateGroup) logOutcome(log io.Writer, via string) cgp.BulkOutcomeObserver {
	return func(i int, o cgp.BulkOutcome) {
		if log == nil {
			return
		}
		v := g.outcomeView(o)
		position := fmt.Sprintf("op %d", i+1)
		if g.req.Sweep == nil {
			position = fmt.Sprintf("op %d/%d", i+1, len(g.req.Ops))
		}
		line := fmt.Sprintf("mutate_nodes %s %s: %s %s", g.anchor, position, v.Status, v.ID)
		if o.URI != nil {
			line += " " + o.URI.String()
		}
		line += via
		if v.Error != "" {
			line += ": " + v.Error
		}
		fmt.Fprintln(log, line)
	}
}

func (g *mutateGroup) id(uri *url.URL) string {
	if uri == nil {
		return ""
	}
	if id, ok := g.idOf[uri.String()]; ok {
		return id
	}
	return node_view.RelativeIDFor(g.lister, uri.String(), g.anchor)
}
