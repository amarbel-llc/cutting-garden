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
	dim := edited.groupedDimension()
	if edited.Anchor == "" || dim == "" {
		return false, errors.BadRequestf(
			"organize --apply: document is missing its `- _anchor` field or its " +
				"`<dim>=` grouping heading",
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

	// Two independent delta kinds merge against the same pinned base and live
	// state: bucket moves (a facet re-file, applied via FacetWriteApplier) and
	// field/trailer edits (a box-atom or description change, applied via
	// FieldWriteApplier, cutting-garden#218). A read-only or cleared field edit
	// is surfaced as a non-blocking notice rather than silently dropped.
	moves, err := planMoves(edited, base, dim, liveNodes)
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

	switch {
	case commit && interactive:
		ok, cerr := confirmApply(len(changes))
		if cerr != nil {
			return false, cerr
		}
		if !ok {
			commit = false
			fmt.Fprintln(cmd.output, "organize: not confirmed — nothing written")
		}
	case !commit:
		fmt.Fprintln(cmd.output, "organize: dry-run — nothing written")
	}

	if !commit {
		return false, nil
	}

	if len(moves) > 0 {
		mutator, applier, writes, werr := resolveWrites(lister, dim)
		if werr != nil {
			return false, werr
		}
		if err := cmd.executePlan(ctx, mutator, applier, writes, moves); err != nil {
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
func planMoves(edited, base document, dim string, liveNodes []cgp.Node) ([]move, error) {
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
		liveAsg[key] = firstFacetKey(n.Facets[dim]) // Mode-one: "" or the value
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
				"organize: the grouped dimension is read-only for type %q; it "+
					"cannot be reorganized", mv.Node.Type,
			)
		case cgp.FacetWriteMany:
			return errors.BadRequestf(
				"organize: multi-valued (mode many) dimension apply is out of "+
					"scope in this slice (type %q)", mv.Node.Type,
			)
		}

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
