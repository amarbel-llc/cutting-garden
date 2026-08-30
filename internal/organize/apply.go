package organize

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"code.linenisgreat.com/cutting-garden/internal/command_components"
	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
	"code.linenisgreat.com/piggy/go/pkgs/markl"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	"github.com/charmbracelet/huh"
)

// move is one node whose bucket changed between the pinned base and the edited
// document — a facet-bucket reassignment the apply engine writes through.
type move struct {
	URI  string
	From string
	To   string
	Node cgp.Node // the live node, carrying the component/type the write needs
}

// runApply reads an edited organize document from a path (or stdin for "-") and
// applies it. At a terminal it is wet-run by default — showing the #224 diff and
// confirming before writing; piped or redirected it stays dry-run unless -commit,
// so a headless invocation never writes silently (cutting-garden#213). -dry-run
// forces preview everywhere.
func (cmd *Organize) runApply(ctx errors.Context, applyPath string) error {
	editedText, err := readApplyInput(applyPath)
	if err != nil {
		return err
	}
	tty := stdoutIsTerminal()
	commit, interactive := applyMode(cmd.DryRun, cmd.Commit, tty)
	_, err = cmd.applyDocument(ctx, editedText, commit, interactive, tty)
	return err
}

// applyMode resolves whether an apply writes (commit) and whether it engages the
// interactive confirm gate, from the -dry-run/-commit flags and whether stdout is
// a terminal — the wet-run-by-default rule (cutting-garden#213). At a terminal
// organize writes by default, confirming after the #224 diff; piped or redirected
// it stays dry-run unless -commit, so a headless invocation never writes silently;
// -dry-run forces preview everywhere. interactive is true only at a terminal, so
// the huh confirm prompt is never run without one.
func applyMode(dryRun, commitFlag, tty bool) (commit, interactive bool) {
	switch {
	case dryRun:
		return false, false
	case tty:
		return true, true
	default:
		return commitFlag, false
	}
}

// runCommitDirectly reads an edited document from stdin and applies it,
// committing the writes — the scripted re-apply-a-saved-dry-run path (dodder's
// commit-directly mode). The mode itself is the commit assertion.
func (cmd *Organize) runCommitDirectly(ctx errors.Context) error {
	editedText, err := readApplyInput("-")
	if err != nil {
		return err
	}
	_, err = cmd.applyDocument(ctx, editedText, true, false, stdoutIsTerminal())
	return err
}

// rejectTagAtoms refuses an edited document whose box carries bare tag tokens
// (`- [x.ics work-x location=Bank]`): the parser round-trips them (design G13)
// but nothing writes them until native tags slice 2, and a silent drop would
// discard the user's edit — so the refusal is loud, naming the object and the
// tags as the user spelled them.
func rejectTagAtoms(doc document) error {
	for _, ln := range doc.objectLines() {
		if len(ln.Tags) == 0 {
			continue
		}
		spelled := make([]string, len(ln.Tags))
		for i, tag := range ln.Tags {
			spelled[i] = trellis.QuoteIfNeeded(tag)
		}
		return errors.BadRequestf(
			"organize --apply: object %s carries tag atoms %s: tag atoms are not "+
				"writable yet (native tags slice 2)",
			ln.ID, strings.Join(spelled, " "),
		)
	}
	return nil
}

// applyDocument three-way-merges an edited organize document against its pinned
// base and the re-queried live state, then writes the resulting bucket moves
// through the plugin's NodeMutator. commit gates dry-run vs writes; interactive
// enables the large-batch confirmation gate. It returns whether writes were
// actually performed (the effective commit state after any declined gate), which
// the interactive caller uses to decide the edited buffer's fate.
func (cmd *Organize) applyDocument(
	ctx errors.Context, editedText string, commit, interactive, color bool,
) (committed bool, err error) {
	edited, err := parseDocument(editedText)
	if err != nil {
		return false, err
	}
	if err := rejectTagAtoms(edited); err != nil {
		return false, err
	}
	// The dimension heading carries the whole grouping spec: the bare dimension
	// AND, for a date grouping, the persisted granularity (`date_due:month=`,
	// cutting-garden#230) — apply coarsens live values from the document alone,
	// never from config. The BARE dimension keys the write surface below.
	spec, err := edited.groupedSpec()
	if err != nil {
		return false, err
	}
	dim := spec.Dim
	if edited.Anchor == "" || dim == "" {
		return false, errors.BadRequestf(
			"organize --apply: document is missing its `- _anchor` field or its " +
				"grouping (a `# <dim>=` heading or a `- _group-by` directive)",
		)
	}
	if edited.BaseDigest == "" {
		return false, errors.BadRequestf(
			"organize --apply: document has no `- _base = @<digest>` pin to merge against",
		)
	}

	store := command_components.MakeBlobStoreEnv(ctx).GetDefaultBlobStore()
	baseBody, err := readBase(store, edited.BaseDigest)
	if err != nil {
		return false, err
	}
	base, err := parseDocument(baseBody)
	if err != nil {
		return false, errors.Wrapf(err, "organize --apply: parse pinned base blob")
	}

	u, lister, err := command_components.ResolveRootListerPlugin(edited.Anchor)
	if err != nil {
		return false, err
	}
	liveNodes, err := selectNodes(ctx, lister, u, edited.Query)
	if err != nil {
		return false, errors.Wrapf(err, "organize --apply: re-query live state")
	}

	// Dispatch on the grouped dimension's write cardinality BEFORE planMoves: a
	// multi-valued (write:many) dimension's document files one object under several
	// buckets, which planMoves' assignments() dup-guard rejects as "appears twice".
	// The membership path merges the tag SET (planMemberships) instead. A
	// single-valued (write:one), read-only, or absent dimension takes the unchanged
	// facet path below (RFC 0019, #231 slice 2).
	multiValued, err := groupedIsMultiValued(lister, dim, liveNodes)
	if err != nil {
		return false, err
	}
	if multiValued {
		// The grouped dimension's tag interpreter is resolved from the field's
		// declared default plus the global [tags] config override (RFC 0019 §4,
		// #231 slice 3). Load the config here — on the membership path only — the
		// same way generate does; the single-valued facet path needs no interpreter.
		cfg, err := command_components.LoadDefaultConfig(nil)
		if err != nil {
			return false, err
		}
		return cmd.applyMemberships(
			ctx, edited, base, liveNodes, lister, dim, spec.Namespace, cfg.Tags.Interpreter,
			commit, interactive, color,
		)
	}

	// Two independent delta kinds merge against the same pinned base and live
	// state: bucket moves (a facet re-file, applied via FacetWriteApplier) and
	// field/trailer edits (a box-atom or description change, applied via
	// FieldWriteApplier, cutting-garden#218). A read-only or cleared field edit
	// is surfaced as a non-blocking notice rather than silently dropped.
	moves, err := planMoves(edited, base, spec, liveNodes)
	if err != nil {
		return false, err
	}
	writable, trailer := fieldWriteSchema(lister)
	fieldEdits, notices, err := planFieldEdits(
		edited, base, liveNodes, edited.Anchor, writable, trailer, boxAtomPresenter(lister),
	)
	if err != nil {
		return false, err
	}
	if len(notices) > 0 {
		fmt.Fprintf(cmd.output,
			"organize: note — some field edits were not applied on %d line(s): %s "+
				"(read-only fields such as dates are cutting-garden#218 slice 2; "+
				"clearing a field is #215)\n",
			len(notices), strings.Join(notices, ", "))
	}

	// Writability precheck (cutting-garden#221): resolve the move write surface and
	// validate each move's grouped-dimension mode BEFORE the diff and confirm, so a
	// move onto a read-only dimension refuses immediately rather than rendering a
	// diff and prompting "Apply these N change(s)?" only to fail after the user
	// confirms. The resolved surface is reused by executePlan below.
	var (
		moveMutator cgp.NodeMutator
		moveApplier cgp.FacetWriteApplier
		moveWrites  map[string]cgp.FacetWrite
	)
	if len(moves) > 0 {
		var werr error
		if moveMutator, moveApplier, moveWrites, werr = resolveWrites(lister, dim); werr != nil {
			return false, werr
		}
		for _, mv := range moves {
			if err := checkMoveWritable(moveWrites, mv); err != nil {
				return false, err
			}
		}
	}

	// The diff (cutting-garden#224): fold the moves and field edits into one box
	// per object and show it BEFORE any write, so the user reviews exactly what
	// lands. An interactive commit then confirms; a dry-run notes it wrote
	// nothing; a scripted commit asserts intent by its mode and skips the prompt.
	changes := buildChanges(edited, base, moves, fieldEdits, dim, trailer, edited.Anchor)
	if len(changes) == 0 {
		fmt.Fprintln(cmd.output, "organize: no changes to apply")
		return commit, nil
	}

	fmt.Fprintf(cmd.output, "organize: %d change(s):\n\n", len(changes))
	renderDiff(cmd.output, changes, color)
	fmt.Fprintln(cmd.output)

	write, err := cmd.reviewGate(len(changes), commit, interactive)
	if err != nil {
		return false, err
	}
	if !write {
		return false, nil
	}

	if len(moves) > 0 {
		if err := cmd.executePlan(ctx, moveMutator, moveApplier, moveWrites, moves); err != nil {
			return false, err
		}
	}
	if len(fieldEdits) > 0 {
		mutator, applier, ferr := resolveFieldWrites(lister)
		if ferr != nil {
			return false, ferr
		}
		if err := cmd.executeFieldEdits(ctx, mutator, applier, fieldEdits); err != nil {
			return false, err
		}
	}
	fmt.Fprintf(cmd.output, "organize: wrote %d change(s)\n", len(changes))
	return true, nil
}

// planMoves computes the three-way merge, keyed by box id. The stored box ids
// (base and edited) and the live nodes' re-derived relativeID(node.URI, anchor)
// key alike because both run relativeID over the plugin's canonical node URIs, so
// the URI spelling never has to match by string. A node whose edited bucket
// differs from its base bucket is a move, UNLESS the live state has already
// drifted from the base — a conflict, reported as a structured rejection rather
// than silently overwritten (RFC 0015). Additions/deletions vs the base are out
// of scope this slice and ignored.
//
// The base and edited assignments come from the documents' own `=<value>`
// headings, which a date grouping already rendered coarse — so only the LIVE
// day-precise value needs coarsening to the spec's granularity for the three
// sides to compare like for like (cutting-garden#230).
func planMoves(edited, base document, spec groupSpec, liveNodes []cgp.Node) ([]move, error) {
	anchor := edited.Anchor

	editedAsg, err := edited.assignments()
	if err != nil {
		return nil, err
	}
	baseAsg, err := base.assignments()
	if err != nil {
		return nil, errors.Wrapf(err, "organize --apply: pinned base is malformed")
	}

	liveByKey := make(map[string]cgp.Node, len(liveNodes))
	liveAsg := make(map[string]string, len(liveNodes))
	for _, n := range liveNodes {
		key := relativeID(n.URIString(), anchor)
		liveByKey[key] = n
		// Mode-one: "" or the value, coarsened to the document's granularity.
		liveAsg[key] = coarsenBucket(firstFacetKey(n.Facets[spec.Dim]), spec.Granularity)
	}

	var moves []move
	var conflicts []string
	for key, to := range editedAsg {
		from, inBase := baseAsg[key]
		if !inBase || to == from {
			continue // an added line, or an unmoved node
		}
		liveBucket, inLive := liveAsg[key]
		if !inLive || liveBucket != from {
			conflicts = append(conflicts, fmt.Sprintf(
				"%s: base=%q live=%q (your edit moved it to %q)",
				key, from, liveBucket, to,
			))
			continue
		}
		node := liveByKey[key]
		moves = append(moves, move{URI: node.URIString(), From: from, To: to, Node: node})
	}

	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return nil, errors.ErrorWithStackf(
			"organize --apply: %d conflict(s) — the live state drifted from the "+
				"pinned base; regenerate and re-edit:\n  %s",
			len(conflicts), strings.Join(conflicts, "\n  "),
		)
	}

	sort.Slice(moves, func(i, j int) bool { return moves[i].URI < moves[j].URI })
	return moves, nil
}

// resolveWrites resolves the plugin's write surface for the grouped dimension:
// the NodeMutator that performs writes, the FacetWriteApplier that builds each
// move's patch, and a per-node-type FacetWrite mapping for the dimension. A plugin
// exposing no writes, declaring no mapping for the dimension, or declaring
// writable facets without an applier to build the patch is rejected loudly
// (FDR 0023 "writability must be declared").
func resolveWrites(
	lister cgp.RootLister, dim string,
) (cgp.NodeMutator, cgp.FacetWriteApplier, map[string]cgp.FacetWrite, error) {
	mutator, ok := lister.(cgp.NodeMutator)
	if !ok {
		return nil, nil, nil, errors.BadRequestf(
			"organize --apply: plugin does not support writes (no NodeMutator)",
		)
	}
	describer, ok := lister.(cgp.FacetWriteDescriber)
	if !ok {
		return nil, nil, nil, errors.BadRequestf(
			"organize --apply: plugin declares no writable facets (no "+
				"FacetWriteDescriber); dimension %q cannot be reorganized", dim,
		)
	}
	applier, ok := lister.(cgp.FacetWriteApplier)
	if !ok {
		return nil, nil, nil, errors.BadRequestf(
			"organize --apply: plugin declares writable facets but no "+
				"FacetWriteApplier to build the patch; dimension %q cannot be "+
				"reorganized", dim,
		)
	}

	writes := make(map[string]cgp.FacetWrite)
	for _, nt := range describer.DescribeFacetWrites() {
		for _, w := range nt.Writes {
			if w.DimensionKey == dim {
				writes[nt.Tag] = w
			}
		}
	}
	if len(writes) == 0 {
		return nil, nil, nil, errors.BadRequestf(
			"organize --apply: dimension %q has no write mapping declared", dim,
		)
	}
	return mutator, applier, writes, nil
}

// confirmApply presents the yes/no gate an interactive commit shows after the
// diff (cutting-garden#224), returning the user's decision.
func confirmApply(changeCount int) (bool, error) {
	confirmed := false
	prompt := huh.NewConfirm().
		Title(fmt.Sprintf("Apply these %d change(s)?", changeCount)).
		Affirmative("Apply").
		Negative("Cancel").
		Value(&confirmed)
	if err := prompt.Run(); err != nil {
		return false, errors.Wrapf(err, "organize: confirmation prompt")
	}
	return confirmed, nil
}

// reviewGate renders the standard confirm/dry-run footer for a change batch and
// reports whether to write — the single source of the wet-run gate (#213/#224)
// shared by the single-valued facet path and the multi-valued membership path. It
// assumes the caller already printed the change header and the preview: an
// interactive commit confirms after it, a dry-run notes it wrote nothing, and a
// scripted commit asserts intent by its mode and skips the prompt.
func (cmd *Organize) reviewGate(changeCount int, commit, interactive bool) (bool, error) {
	switch {
	case commit && interactive:
		ok, cerr := confirmApply(changeCount)
		if cerr != nil {
			return false, cerr
		}
		if !ok {
			fmt.Fprintln(cmd.output, "organize: not confirmed — nothing written")
			return false, nil
		}
	case !commit:
		fmt.Fprintln(cmd.output, "organize: dry-run — nothing written")
		return false, nil
	}
	return true, nil
}

// groupedIsMultiValued reports whether the grouped dimension writes as a
// multi-valued (write:many) tag SET for the live nodes' types — the dispatch key
// the apply engine reads BEFORE planMoves (RFC 0019, #231 slice 2). It reads the
// plugin's FacetWriteDescriber and looks the dimension's mode up across
// distinctTypes(liveNodes): many selects the membership path; one, none, an
// unmapped type, or a plugin with no write declarations selects the unchanged
// facet path (which itself rejects a read-only/unmapped move loudly). A dimension
// declared many for some present types and single-valued for others is rejected
// loudly — mixed single/multi-valued grouping is out of scope this slice
// (categories is uniformly many across every caldav component, so this is a safety
// guard).
func groupedIsMultiValued(
	lister cgp.RootLister, dim string, liveNodes []cgp.Node,
) (bool, error) {
	describer, ok := lister.(cgp.FacetWriteDescriber)
	if !ok {
		return false, nil
	}
	modeByType := make(map[string]cgp.FacetWriteMode)
	for _, nt := range describer.DescribeFacetWrites() {
		for _, w := range nt.Writes {
			if w.DimensionKey == dim {
				modeByType[nt.Tag] = w.Mode
			}
		}
	}
	var many, other bool
	for _, typ := range distinctTypes(liveNodes) {
		if modeByType[typ] == cgp.FacetWriteMany {
			many = true
		} else {
			other = true
		}
	}
	if many && other {
		return false, errors.BadRequestf(
			"organize --apply: dimension %q is multi-valued for some present node "+
				"type(s) and single-valued for others — mixed single/multi-valued "+
				"grouping is out of scope in this slice", dim,
		)
	}
	return many, nil
}

// executePlan writes each bucket move through the plugin's applier. It is called
// only on a confirmed commit — the diff preview and its confirmation happen in
// applyDocument. Mode/writability is checked per move so a mixed-type node set is
// validated node by node against its own type's mapping.
func (cmd *Organize) executePlan(
	ctx context.Context,
	mutator cgp.NodeMutator,
	applier cgp.FacetWriteApplier,
	writes map[string]cgp.FacetWrite,
	moves []move,
) error {
	for _, mv := range moves {
		if err := checkMoveWritable(writes, mv); err != nil {
			return err
		}
		w := writes[mv.Node.Type] // presence validated by checkMoveWritable

		body, err := applier.BuildFacetWritePatch(ctx, mv.Node, w, mv.To)
		if err != nil {
			return errors.Wrapf(err, "organize: build patch for %s", mv.URI)
		}
		if _, err := mutator.PatchNode(ctx, mv.Node.URI, bytes.NewReader(body)); err != nil {
			return errors.Wrapf(err, "organize: patch %s", mv.URI)
		}
	}
	return nil
}

// interpreterForDimension resolves the tag interpreter a grouped dimension uses
// (RFC 0019 §4 selection): the field's plugin-declared default (its
// UnifiedField.Interpreter, read via the optional UnifiedDescriber capability)
// with the global [tags] config override layered on top — the override wins,
// per A2's ResolveTagInterpreter. A lister that declares no unified fields, no
// field for the dimension, or an empty declared interpreter defaults the
// field-default to "naive" (the RFC 0019 §4 default and the Slice-1/2 behavior),
// NOT an error; only an unknown interpreter NAME (from either source) is the
// loud bad request ResolveTagInterpreter raises. The resolved name is returned
// alongside the interpreter so a caller can name it in an error (e.g. a naive
// interpreter rejecting a namespace grouping).
func interpreterForDimension(
	lister cgp.RootLister, dim string, tagsOverride string,
) (cgp.TagInterpreter, string, error) {
	fieldDefault := "naive"
	if describer, ok := lister.(cgp.UnifiedDescriber); ok {
		if declared := declaredTagInterpreter(describer, dim); declared != "" {
			fieldDefault = declared
		}
	}
	name := tagsOverride
	if name == "" {
		name = fieldDefault
	}
	interp, err := command_components.ResolveTagInterpreter(fieldDefault, tagsOverride)
	return interp, name, err
}

// declaredTagInterpreter returns the interpreter a plugin declares for the
// dimension's unified field — the first field whose Key == dim across the node
// types' codecs — or "" when no such field is declared (or it names no
// interpreter). A tag field's interpreter is a property of the dimension, so the
// first Key match is authoritative; caller defaults "" to naive.
func declaredTagInterpreter(describer cgp.UnifiedDescriber, dim string) string {
	for _, nt := range describer.DescribeUnified() {
		for _, codec := range nt.Codecs {
			for _, field := range codec.Fields() {
				if field.Key == dim {
					return field.Interpreter
				}
			}
		}
	}
	return ""
}

// applyMemberships is the multi-valued-dimension apply path (RFC 0019, #231 slice
// 2): it three-way-merges each object's tag SET via planMemberships and writes the
// resulting full-set replacements through the plugin's MembershipWriteApplier,
// sharing the wet-run gate (reviewGate) and the field-edit path with the
// single-valued facet path. Field/trailer edits still apply here, but a
// multi-membership object's line appears N times in the document, so
// planFieldEdits' returned edits are deduped by URI to keep a single-appearance
// atom edit applying once while a multi-appearance object is never patched N times
// (full agree/conflict reconciliation across divergent appearances is slice 2b).
func (cmd *Organize) applyMemberships(
	ctx context.Context,
	edited, base document,
	liveNodes []cgp.Node,
	lister cgp.RootLister,
	dim string,
	namespace string,
	tagsOverride string,
	commit, interactive, color bool,
) (committed bool, err error) {
	// Resolve the grouped dimension's tag interpreter from the field's declared
	// default plus the global [tags] config override (RFC 0019 §4, #231 slice 3).
	// namespace (spec.Namespace) is "" for a whole-dimension grouping — buckets are
	// full tags, folded exactly (the slice-2 behavior, identical under naive and
	// dodder-hyphen) — and the segment prefix (`project`) for a namespace rollup, in
	// which planMemberships reconstructs the add tag and enumerates the remove
	// subtree (RFC 0019 §6.2, #231 slice 3 B4).
	interp, _, err := interpreterForDimension(lister, dim, tagsOverride)
	if err != nil {
		return false, err
	}

	memberships, err := planMemberships(edited, base, liveNodes, edited.Anchor, interp, dim, namespace)
	if err != nil {
		return false, err
	}

	// Resolve the membership write surface up front (mirroring the single-valued
	// move precheck) so a plugin declaring a many dimension but no
	// MembershipWriteApplier refuses before rendering a preview and prompting.
	var (
		memberMutator cgp.NodeMutator
		memberApplier cgp.MembershipWriteApplier
		memberWrites  map[string]cgp.FacetWrite
	)
	if len(memberships) > 0 {
		if memberMutator, memberApplier, memberWrites, err = resolveMembershipWrites(lister, dim); err != nil {
			return false, err
		}
	}

	writable, trailer := fieldWriteSchema(lister)
	fieldEdits, notices, err := planFieldEdits(
		edited, base, liveNodes, edited.Anchor, writable, trailer, boxAtomPresenter(lister),
	)
	if err != nil {
		return false, err
	}
	// A multi-membership object appears N times, so planFieldEdits can emit its
	// field edit N times — dedup by URI so a single-appearance atom edit still
	// applies once and a multi-appearance object is never patched N times.
	fieldEdits = dedupFieldEditsByURI(fieldEdits)
	if len(notices) > 0 {
		fmt.Fprintf(cmd.output,
			"organize: note — some field edits were not applied on %d line(s): %s "+
				"(read-only fields such as dates are cutting-garden#218 slice 2; "+
				"clearing a field is #215)\n",
			len(notices), strings.Join(notices, ", "))
	}

	total := len(memberships) + len(fieldEdits)
	if total == 0 {
		fmt.Fprintln(cmd.output, "organize: no changes to apply")
		return commit, nil
	}

	fmt.Fprintf(cmd.output, "organize: %d change(s):\n\n", total)
	renderMembershipChanges(cmd.output, memberships, dim, edited.Anchor, color)
	if len(fieldEdits) > 0 {
		renderDiff(cmd.output, buildChanges(edited, base, nil, fieldEdits, dim, trailer, edited.Anchor), color)
	}
	fmt.Fprintln(cmd.output)

	write, err := cmd.reviewGate(total, commit, interactive)
	if err != nil {
		return false, err
	}
	if !write {
		return false, nil
	}

	if len(memberships) > 0 {
		if err := cmd.executeMemberships(ctx, memberMutator, memberApplier, memberWrites, memberships); err != nil {
			return false, err
		}
	}
	if len(fieldEdits) > 0 {
		fmutator, fapplier, ferr := resolveFieldWrites(lister)
		if ferr != nil {
			return false, ferr
		}
		if err := cmd.executeFieldEdits(ctx, fmutator, fapplier, fieldEdits); err != nil {
			return false, err
		}
	}
	fmt.Fprintf(cmd.output, "organize: wrote %d change(s)\n", total)
	return true, nil
}

// resolveMembershipWrites resolves the plugin's write surface for a multi-valued
// grouped dimension: the NodeMutator, the MembershipWriteApplier that builds each
// full-set replacement patch, and the per-node-type FacetWrite mapping. It is the
// membership sibling of resolveWrites — a plugin declaring a many dimension but
// exposing no mutator, no FacetWriteDescriber, or no MembershipWriteApplier is
// rejected loudly (FDR 0023 "writability must be declared").
func resolveMembershipWrites(
	lister cgp.RootLister, dim string,
) (cgp.NodeMutator, cgp.MembershipWriteApplier, map[string]cgp.FacetWrite, error) {
	mutator, ok := lister.(cgp.NodeMutator)
	if !ok {
		return nil, nil, nil, errors.BadRequestf(
			"organize --apply: plugin does not support writes (no NodeMutator)",
		)
	}
	describer, ok := lister.(cgp.FacetWriteDescriber)
	if !ok {
		return nil, nil, nil, errors.BadRequestf(
			"organize --apply: plugin declares no writable facets (no "+
				"FacetWriteDescriber); dimension %q cannot be reorganized", dim,
		)
	}
	applier, ok := lister.(cgp.MembershipWriteApplier)
	if !ok {
		return nil, nil, nil, errors.BadRequestf(
			"organize --apply: plugin declares a multi-valued dimension but no "+
				"MembershipWriteApplier to build the patch; dimension %q cannot be "+
				"reorganized", dim,
		)
	}

	writes := make(map[string]cgp.FacetWrite)
	for _, nt := range describer.DescribeFacetWrites() {
		for _, w := range nt.Writes {
			if w.DimensionKey == dim {
				writes[nt.Tag] = w
			}
		}
	}
	if len(writes) == 0 {
		return nil, nil, nil, errors.BadRequestf(
			"organize --apply: dimension %q has no write mapping declared", dim,
		)
	}
	return mutator, applier, writes, nil
}

// executeMemberships writes each membership edit's complete replacement tag set
// through the plugin's MembershipWriteApplier — one full-set PatchNode per object.
// Called only on a confirmed commit; the preview and its confirmation happen in
// applyMemberships. It uses BuildMembershipWritePatch (NOT BuildFacetWritePatch)
// with the FULL NewTags, so the multi-valued codec's full-set Parse persists
// exactly the interpreter-resolved set.
func (cmd *Organize) executeMemberships(
	ctx context.Context,
	mutator cgp.NodeMutator,
	applier cgp.MembershipWriteApplier,
	writes map[string]cgp.FacetWrite,
	edits []membershipEdit,
) error {
	for _, e := range edits {
		// writes[e.Node.Type] needs no presence check: groupedIsMultiValued has
		// already established every present live type maps to a many write for this
		// dimension before applyMemberships runs, so unlike executePlan's per-move
		// re-check there is no mixed/unmapped type to guard against here.
		body, err := applier.BuildMembershipWritePatch(ctx, e.Node, writes[e.Node.Type], e.NewTags)
		if err != nil {
			return errors.Wrapf(err, "organize: build membership patch for %s", e.URI)
		}
		if _, err := mutator.PatchNode(ctx, e.Node.URI, bytes.NewReader(body)); err != nil {
			return errors.Wrapf(err, "organize: patch %s", e.URI)
		}
	}
	return nil
}

// checkMoveWritable validates a move's grouped-dimension write mode against its
// type's mapping: an unmapped type, a read-only (none) dimension, or a
// multi-valued (many) dimension is a bad request. The apply engine runs it UP
// FRONT — before rendering the diff and prompting to confirm — so a move onto a
// read-only dimension refuses immediately rather than after the user confirms
// changes that can never be written (cutting-garden#221 exposed this: grouping by
// a read-only dimension and moving a line reached the confirm prompt, then
// failed).
func checkMoveWritable(writes map[string]cgp.FacetWrite, mv move) error {
	w, ok := writes[mv.Node.Type]
	if !ok {
		return errors.BadRequestf(
			"organize: type %q declares no write mapping for the grouped dimension",
			mv.Node.Type,
		)
	}
	switch w.Mode {
	case cgp.FacetWriteNone:
		return errors.BadRequestf(
			"organize: the grouped dimension is read-only for type %q — you can group "+
				"by it to view, but its buckets cannot be reorganized", mv.Node.Type,
		)
	case cgp.FacetWriteMany:
		return errors.BadRequestf(
			"organize: multi-valued (mode many) dimension apply is out of scope in "+
				"this slice (type %q)", mv.Node.Type,
		)
	}
	return nil
}

// firstFacetKey returns the first bucket key of a Mode-one facet membership, or
// "" when the node contributes no value.
func firstFacetKey(values []cgp.FacetValue) string {
	if len(values) == 0 {
		return ""
	}
	return values[0].Key
}

// readApplyInput reads the edited document from a file path, or from stdin when
// path is "-".
func readApplyInput(path string) (string, error) {
	if path == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", errors.Wrap(err)
		}
		return string(data), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", errors.Wrapf(err, "organize --apply: read %s", path)
	}
	return string(data), nil
}

// readBase reads the pinned base blob's bytes back from the store by digest.
func readBase(
	store blob_stores.BlobStoreInitialized, digest string,
) (text string, err error) {
	var id markl.Id
	if err = id.Set(digest); err != nil {
		return "", errors.Wrapf(err, "organize --apply: parse base digest %q", digest)
	}
	reader, err := store.MakeBlobReader(&id)
	if err != nil {
		return "", errors.Wrapf(err, "organize --apply: open base blob %s", digest)
	}
	defer errors.DeferredCloser(&err, reader)

	data, err := io.ReadAll(reader)
	if err != nil {
		return "", errors.Wrap(err)
	}
	return string(data), nil
}
