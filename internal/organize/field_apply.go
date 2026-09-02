package organize

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// objectFieldEdit is one live node and the batch of its changed writable box
// atoms (and/or its description trailer) the field-write path applies as a single
// PatchNode (cutting-garden#218). Distinct from a bucket move (a facet re-file),
// which the FacetWriteApplier path handles.
type objectFieldEdit struct {
	URI   string
	Node  cgp.Node
	Edits []cgp.FieldEdit
}

// fieldWriteSchema resolves, per node type, the set of writable field keys and
// the trailer field key from the plugin's declared listing-field schema
// (ListingField.Writable/Trailer — the single source of writability, #218). A
// plugin without the ListingFieldsDescriber capability declares nothing writable,
// so an edit to its fields stays a read-only notice.
func fieldWriteSchema(
	lister cgp.RootLister,
) (writable map[string]map[string]bool, trailer map[string]string) {
	describer, ok := lister.(cgp.ListingFieldsDescriber)
	if !ok {
		return nil, nil
	}
	writable = map[string]map[string]bool{}
	trailer = map[string]string{}
	for _, nt := range describer.DescribeListingFields() {
		for _, f := range nt.Fields {
			if f.Writable {
				if writable[nt.Tag] == nil {
					writable[nt.Tag] = map[string]bool{}
				}
				writable[nt.Tag][f.Key] = true
			}
			if f.Trailer {
				trailer[nt.Tag] = f.Key
			}
		}
	}
	return writable, trailer
}

// descriptionOf resolves a node's box description trailer: the plugin-declared
// trailer field's value when a trailer is declared for its type, else the legacy
// summary->title->name projection (nodeDescription). A declared-but-empty trailer
// yields an empty trailer (no name fallback), so the rendered trailer and the
// value written back stay the SAME field — a title edit round-trips to the field
// it came from. Newlines collapse to single spaces exactly as nodeDescription's
// do (collapseToSingleLine), so a live multiline value compares equal to the
// document's single-line rendering and an untouched trailer never writes back.
func descriptionOf(n cgp.Node, trailerField string) string {
	if trailerField != "" {
		if s, ok := n.Fields[trailerField].(string); ok {
			return collapseToSingleLine(s)
		}
		return ""
	}
	return nodeDescription(n)
}

// atomMap indexes a box's atoms by name for value comparison.
func atomMap(atoms []cgp.BoxAtom) map[string]string {
	m := make(map[string]string, len(atoms))
	for _, a := range atoms {
		m[a.Name] = a.Value
	}
	return m
}

// planFieldEdits three-way-merges each object's writable box atoms and trailer
// across the pinned base, the edited document, and the re-queried live node
// (cutting-garden#218) — the field-edit sibling of planMoves. An attribute whose
// edited value differs from base is applied UNLESS the live value has drifted
// from base to a THIRD value (a conflict → hard reject, mirroring planMoves). A
// changed attribute that is not writable, or an edit that CLEARS a value (empty
// new value — deletion is deferred to #215; read-only dates are #218 slice 2), is
// not applied but surfaced as a non-blocking notice id. Only ids present in BOTH
// base and live are considered; an added or removed line is out of scope here.
func planFieldEdits(
	edited, base document,
	liveNodes []cgp.Node,
	anchor string,
	writable map[string]map[string]bool,
	trailer map[string]string,
	present func(cgp.Node) []cgp.BoxAtom,
) (edits []objectFieldEdit, notices []string, err error) {
	baseLines := make(map[string]objectLine)
	for _, ln := range base.objectLines() {
		baseLines[ln.ID] = ln
	}
	liveByKey := make(map[string]cgp.Node, len(liveNodes))
	for _, n := range liveNodes {
		liveByKey[relativeID(n.URIString(), anchor)] = n
	}

	var conflicts []string
	noticed := make(map[string]bool)

	for _, eln := range edited.objectLines() {
		bln, inBase := baseLines[eln.ID]
		if !inBase {
			continue
		}
		live, inLive := liveByKey[eln.ID]
		if !inLive {
			continue
		}
		wset := writable[live.Type]
		trailerField := trailer[live.Type]

		// The live atoms carry each atom's source Field (a caldav date_start's
		// Field is "dtstart", so its writability is governed by the dtstart listing
		// field); the parsed doc atoms do not, so the gate reads the field from the
		// live presentation. A plain atom's Field defaults to its own Name.
		liveAtoms := map[string]string{}
		liveField := map[string]string{}
		if present != nil {
			for _, a := range present(live) {
				liveAtoms[a.Name] = a.Value
				f := a.Field
				if f == "" {
					f = a.Name
				}
				liveField[a.Name] = f
			}
		}
		editedAtoms := atomMap(eln.Fields)
		baseAtoms := atomMap(bln.Fields)

		var objEdits []cgp.FieldEdit
		consider := func(name, editedVal, baseVal, liveVal string) {
			if editedVal == baseVal {
				return // unchanged
			}
			field := liveField[name]
			if field == "" {
				field = name
			}
			if !wset[field] || editedVal == "" {
				// read-only field, an atom with no live source (e.g. adding a
				// time to an all-day object — the all-day<->timed conversion is
				// #222), or a clear (deletion, #215) — surfaced, not applied.
				noticed[eln.ID] = true
				return
			}
			if liveVal != baseVal && liveVal != editedVal {
				conflicts = append(conflicts, fmt.Sprintf(
					"%s.%s: base=%q live=%q (your edit set %q)",
					eln.ID, name, baseVal, liveVal, editedVal,
				))
				return
			}
			if liveVal == editedVal {
				return // live already equals the edit — nothing to write
			}
			objEdits = append(objEdits, cgp.FieldEdit{Name: name, Value: editedVal})
		}

		for name := range unionAtomNames(editedAtoms, baseAtoms) {
			consider(name, editedAtoms[name], baseAtoms[name], liveAtoms[name])
		}
		if trailerField != "" {
			consider(trailerField, eln.Desc, bln.Desc, descriptionOf(live, trailerField))
		}

		if len(objEdits) > 0 {
			sort.Slice(objEdits, func(i, j int) bool { return objEdits[i].Name < objEdits[j].Name })
			edits = append(edits, objectFieldEdit{URI: live.URIString(), Node: live, Edits: objEdits})
		}
	}

	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return nil, nil, errors.ErrorWithStackf(
			"organize --apply: %d field conflict(s) — the live state drifted from the "+
				"pinned base; regenerate and re-edit:\n  %s",
			len(conflicts), strings.Join(conflicts, "\n  "),
		)
	}

	for id := range noticed {
		notices = append(notices, id)
	}
	sort.Strings(notices)
	// SliceStable, not Slice: on the multi-valued apply path a multi-appearance
	// object yields several equal-URI edits in document order, and
	// dedupFieldEditsByURI keeps the first. A stable sort preserves document order
	// among equal URIs, so "first" is deterministically the document-first
	// appearance rather than whatever pdqsort's partitioning happened to surface.
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].URI < edits[j].URI })
	return edits, notices, nil
}

// dedupFieldEditsByURI keeps the first objectFieldEdit per URI in document order
// (preserved by planFieldEdits' stable sort), dropping later ones. On the
// multi-valued (membership) apply path a two-appearance object's line is parsed
// once per bucket it sits under, so planFieldEdits can return the same object's
// field edit N times; collapsing to one per URI keeps a legitimate
// single-appearance atom edit applying once and prevents a multi-appearance object
// being patched N times (RFC 0019, #231 slice 2). A deterministic single apply is
// acceptable now; full agree/conflict reconciliation across divergent appearances
// is slice 2b.
func dedupFieldEditsByURI(edits []objectFieldEdit) []objectFieldEdit {
	seen := make(map[string]struct{}, len(edits))
	var out []objectFieldEdit
	for _, e := range edits {
		if _, dup := seen[e.URI]; dup {
			continue
		}
		seen[e.URI] = struct{}{}
		out = append(out, e)
	}
	return out
}

func unionAtomNames(a, b map[string]string) map[string]struct{} {
	out := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}

// resolveFieldWrites resolves the plugin's field-write surface: the NodeMutator
// that performs writes and the FieldWriteApplier that builds each object's patch.
// A plugin that declared writable fields but exposes no applier (or no mutator)
// is rejected loudly (FDR 0023 "writability must be declared").
func resolveFieldWrites(
	lister cgp.RootLister,
) (cgp.NodeMutator, cgp.FieldWriteApplier, error) {
	mutator, ok := lister.(cgp.NodeMutator)
	if !ok {
		return nil, nil, errors.BadRequestf(
			"organize --apply: plugin declares writable fields but supports no writes " +
				"(no NodeMutator)",
		)
	}
	applier, ok := lister.(cgp.FieldWriteApplier)
	if !ok {
		return nil, nil, errors.BadRequestf(
			"organize --apply: plugin declares writable fields but no FieldWriteApplier " +
				"to build the patch",
		)
	}
	return mutator, applier, nil
}

// executeFieldEdits writes each object's batched field edits via one PatchNode
// per object. Called only on a confirmed commit — the diff preview and its
// confirmation happen in applyDocument.
func (cmd *Organize) executeFieldEdits(
	ctx context.Context,
	mutator cgp.NodeMutator,
	applier cgp.FieldWriteApplier,
	edits []objectFieldEdit,
) error {
	for _, oe := range edits {
		body, err := applier.BuildFieldWritePatch(ctx, oe.Node, oe.Edits)
		if err != nil {
			return errors.Wrapf(err, "organize: build field patch for %s", oe.URI)
		}
		if _, err := mutator.PatchNode(ctx, oe.Node.URI, bytes.NewReader(body)); err != nil {
			return errors.Wrapf(err, "organize: patch %s", oe.URI)
		}
	}
	return nil
}
