package organize

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Box tag atoms are writable membership edits (native tags slice 2 Task 3,
// design G7): adding or removing a bare tag term in an object's box is a
// membership add/remove on the plugin's designated TAG dimension, folded
// through the SAME interpreter Complete path as a heading move — a typed atom
// is EXACT under every interpreter (RFC 0019 §6.2; typing `project` adds the
// literal tag `project`, never a namespace). The planner here diffs the box
// atoms; the placement (bucket) half of a membership stays planMemberships'
// business. The two halves compose per the document's effective `_tag-strip`:
//
//   - `placement` (the default; also every FIELD grouping, which has no tag
//     placement): a box tag that some placement of the object DERIVES
//     (bucketOfTag — the bucket tag itself under `(tags)`, any tag rolling to
//     a continuation, the bare namespace under a G10a root) is
//     placement-expressed and never an atom delta; the remaining NON-PLACEMENT
//     tags diff against the base's. The bucket diff (planMemberships) carries
//     the placement changes.
//   - `none`: NOTHING is placement-derived — the box is authoritative. An
//     object's membership is the union of its box tags and its current
//     placements' reconstructed bucket tags, and the delta is the diff of that
//     EFFECTIVE set between base and edited, folded exactly. Consequences,
//     pinned by vectors: a MOVED line whose box still carries the old tag
//     keeps it (the tag is removed only when it is gone from both every box
//     and the placement), and placement pop/removal alone never strips a
//     namespace subtree (that requires the placement-strip dialect).
//
// Conflicts (exit 2, mirroring planMoves/planFieldEdits' batched shape):
//
//   - N-way disagreement: an object's appearances must agree on the tags
//     placement does not explain; an atom added to (or removed from) one box
//     but not its siblings' is ambiguous and refuses naming the appearances.
//   - placement-vs-box (G7 rule 1; `placement` strip only): a tag REMOVED
//     from a matched box while the object still sits under that tag's bucket
//     is "placement says X, box says not-X".
//
// A stale atom whose placement vanished re-asserts its tag (box authoritative
// for non-placement tags): removing an object's bucket line while another
// appearance's box still carries the tag as an atom folds to a no-op, never a
// silent removal. `%`-marked display-only atoms (RFC 0015) do not reach this
// planner: `%` is a reserved trellis rune, so a `%`-marked box term is a loud
// parse error today (nothing emits them yet).

// tagDelta is one object's box-driven tag edit in TAG space: full tags to add
// and remove, folded through the interpreter's exact Complete AFTER any
// placement (bucket) folds.
type tagDelta struct {
	adds    []string
	removes []string
}

// planTagAtomDeltas diffs the box tag atoms of the edited document against the
// pinned base, per the semantics above. stripNone selects the `_tag-strip =
// none` effective-set reading (only ever true for a TAG grouping); interp is
// the tag dimension's resolved interpreter (nil when the plugin declares no
// tag dimension — bucketOfTag then derives nothing beyond the whole-dimension
// identity). Ids absent from the base (hand-added lines) are out of scope,
// like everywhere else in the merge. The returned map holds only ids with a
// non-empty delta.
func planTagAtomDeltas(
	edited, base document, spec groupSpec, stripNone bool, interp cgp.TagInterpreter,
) (map[string]tagDelta, error) {
	editedLed, err := tagLedgerOf(edited)
	if err != nil {
		return nil, err
	}
	baseLed, err := tagLedgerOf(base)
	if err != nil {
		return nil, errors.Wrapf(err, "organize --apply: pinned base is malformed")
	}

	deltas := map[string]tagDelta{}
	var conflicts []string
	for _, id := range editedLed.order {
		if _, inBase := baseLed.appearances[id]; !inBase {
			continue // an added line — out of scope, like everywhere in the merge
		}
		apps := editedLed.appearances[id]

		// N-way reconcile: every appearance must carry the same set of tags
		// placement does not explain.
		if len(apps) > 1 {
			comparable := make([][]string, len(apps))
			for i, app := range apps {
				if stripNone {
					comparable[i] = subtractTags(app.tags, editedLed.placementTags(id, spec))
				} else {
					comparable[i] = editedLed.appearanceExtras(id, app, spec, interp)
				}
			}
			conflicts = append(conflicts, disagreementConflicts(id, apps, comparable)...)
		}

		if stripNone {
			editedEff := editedLed.effectiveTags(id, spec)
			baseEff := baseLed.effectiveTags(id, spec)
			d := tagDelta{
				adds:    subtractTags(editedEff, baseEff),
				removes: subtractTags(baseEff, editedEff),
			}
			if len(d.adds)+len(d.removes) > 0 {
				deltas[id] = d
			}
			continue
		}

		// Placement-vs-box (G7 rule 1): a tag removed from a MATCHED
		// appearance's box while the object still sits under that tag's bucket.
		for _, app := range apps {
			baseTags, matched := baseLed.appearanceTags(id, app.bucket)
			if !matched {
				continue
			}
			for _, t := range subtractTags(baseTags, app.tags) {
				if b := bucketOfTag(t, spec, interp); b != "" && editedLed.hasPlacement(id, b) {
					conflicts = append(conflicts, fmt.Sprintf(
						"object %s: placement says %s (still under %s), box says not-%s (removed under %s)",
						id, trellis.QuoteIfNeeded(t), spellBucket(b),
						trellis.QuoteIfNeeded(t), spellBucket(app.bucket),
					))
				}
			}
		}

		editedExtra := editedLed.extraTags(id, spec, interp)
		baseExtra := baseLed.extraTags(id, spec, interp)
		d := tagDelta{adds: subtractTags(editedExtra, baseExtra)}
		// A base extra now expressed by an EDITED placement is not a removal —
		// the tag migrated from box to placement (the bucket diff adds it), as
		// in an ungrouped→bucket move whose editor also cleaned the box.
		for _, t := range subtractTags(baseExtra, editedExtra) {
			if b := bucketOfTag(t, spec, interp); b != "" && editedLed.hasPlacement(id, b) {
				continue
			}
			d.removes = append(d.removes, t)
		}
		if len(d.adds)+len(d.removes) > 0 {
			deltas[id] = d
		}
	}

	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return nil, errors.ErrorWithStackf(
			"organize --apply: %d tag conflict(s) — box tag atoms disagree with "+
				"placement or across appearances; re-edit the document:\n  %s",
			len(conflicts), strings.Join(conflicts, "\n  "),
		)
	}
	return deltas, nil
}

// disagreementConflicts reports, per tag in the symmetric difference of the
// appearances' comparable sets, which appearances carry it and which do not.
func disagreementConflicts(
	id string, apps []tagAppearance, comparable [][]string,
) []string {
	var union []string
	for _, c := range comparable {
		union = appendMissing(union, c)
	}
	var out []string
	for _, t := range union {
		var with, without []string
		for i, c := range comparable {
			if slices.Contains(c, t) {
				with = append(with, spellBucket(apps[i].bucket))
			} else {
				without = append(without, spellBucket(apps[i].bucket))
			}
		}
		if len(with) > 0 && len(without) > 0 {
			out = append(out, fmt.Sprintf(
				"object %s: appearances disagree on tag %s: present under %s, absent under %s",
				id, trellis.QuoteIfNeeded(t),
				strings.Join(with, ", "), strings.Join(without, ", "),
			))
		}
	}
	return out
}

// planAtomMembershipEdits folds pure box-atom deltas onto the live tag sets —
// the single-valued (facet) apply branch's membership planner, where the
// GROUPED dimension is not the tag dimension and no placement folds exist. One
// full-set membershipEdit per object whose folded set differs from live.
func planAtomMembershipEdits(
	deltas map[string]tagDelta, live []cgp.Node, anchor string,
	interp cgp.TagInterpreter, dim string,
) ([]membershipEdit, error) {
	liveByID := make(map[string]cgp.Node, len(live))
	for _, n := range live {
		liveByID[relativeID(n.URIString(), anchor)] = n
	}

	ids := make([]string, 0, len(deltas))
	for id := range deltas {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var edits []membershipEdit
	for _, id := range ids {
		liveNode, ok := liveByID[id]
		if !ok {
			continue // in the documents but not live — out of scope, mirrors planFieldEdits
		}
		liveTags := facetKeys(liveNode.Facets[dim])
		newTags, err := foldTagDelta(liveTags, deltas[id], interp, id)
		if err != nil {
			return nil, err
		}
		if setEqual(newTags, liveTags) {
			continue
		}
		edits = append(edits, membershipEdit{
			URI:     liveNode.URIString(),
			Node:    liveNode,
			NewTags: newTags,
		})
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].URI < edits[j].URI })
	return edits, nil
}

// foldTagDelta applies one object's box-atom delta — removes then adds, each
// EXACT — onto a tag set through the interpreter's Complete.
func foldTagDelta(
	tags []string, d tagDelta, interp cgp.TagInterpreter, id string,
) ([]string, error) {
	var err error
	for _, t := range d.removes {
		if tags, err = interp.Complete(tags, cgp.TagRemove, t); err != nil {
			return nil, errors.Wrapf(err, "organize: %s remove tag atom %q", id, t)
		}
	}
	for _, t := range d.adds {
		if tags, err = interp.Complete(tags, cgp.TagAdd, t); err != nil {
			return nil, errors.Wrapf(err, "organize: %s add tag atom %q", id, t)
		}
	}
	return tags, nil
}

// tagAppearance is one object line's placement in the tag ledger: the bucket
// value it sits under ("" = ungrouped) and its box tags (deduplicated).
type tagAppearance struct {
	bucket string
	tags   []string
}

// docTagLedger is one document's per-object tag view: each object's
// appearances (bucket + box tags) in document order, keyed by box id, with ids
// in document order.
type docTagLedger struct {
	order       []string
	appearances map[string][]tagAppearance
}

// tagLedgerOf collects a document's tag ledger via the same walk memberships
// uses (walkSectionValues), but tolerantly — duplicate placements are the
// membership path's error to raise, not this planner's (a repeated (id,
// bucket) pair merges its tags).
func tagLedgerOf(doc document) (docTagLedger, error) {
	led := docTagLedger{appearances: map[string][]tagAppearance{}}
	record := func(ln objectLine, value string) error {
		apps := led.appearances[ln.ID]
		if apps == nil {
			led.order = append(led.order, ln.ID)
		}
		idx := -1
		for i := range apps {
			if apps[i].bucket == value {
				idx = i
				break
			}
		}
		if idx < 0 {
			led.appearances[ln.ID] = append(apps, tagAppearance{bucket: value})
			idx = len(led.appearances[ln.ID]) - 1
		}
		app := &led.appearances[ln.ID][idx]
		app.tags = appendMissing(app.tags, ln.Tags)
		return nil
	}
	for _, ln := range doc.Ungrouped {
		_ = record(ln, "")
	}
	if err := doc.walkSectionValues(record); err != nil {
		return docTagLedger{}, err
	}
	return led, nil
}

// appearanceTags returns the object's box tags under bucket, and whether the
// document has that appearance at all.
func (led docTagLedger) appearanceTags(id, bucket string) ([]string, bool) {
	for _, app := range led.appearances[id] {
		if app.bucket == bucket {
			return app.tags, true
		}
	}
	return nil, false
}

// hasPlacement reports whether the object has an appearance under bucket.
func (led docTagLedger) hasPlacement(id, bucket string) bool {
	_, ok := led.appearanceTags(id, bucket)
	return ok
}

// extraTags returns the object's NON-PLACEMENT box tags across every
// appearance: each box tag (first-appearance order, deduplicated) whose
// realized bucket (bucketOfTag, derived once per tag) is not among the
// object's placements in this document. For a field-grouped document no tag
// realizes a bucket, so this is the full box tag set.
func (led docTagLedger) extraTags(
	id string, spec groupSpec, interp cgp.TagInterpreter,
) []string {
	var out []string
	for _, app := range led.appearances[id] {
		out = appendMissing(out, led.appearanceExtras(id, app, spec, interp))
	}
	return out
}

// appearanceExtras returns ONE appearance's box tags that no placement of the
// object (anywhere in this document) derives.
func (led docTagLedger) appearanceExtras(
	id string, app tagAppearance, spec groupSpec, interp cgp.TagInterpreter,
) []string {
	var out []string
	for _, t := range app.tags {
		if b := bucketOfTag(t, spec, interp); b != "" && led.hasPlacement(id, b) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// placementTags returns the reconstructed bucket tag of each non-ungrouped
// appearance of the object (deduplicated): the bucket itself under `(tags)`,
// the namespace-reconstructed leaf (`project-client`; the bare namespace for
// the G10a root) under a namespace grouping — the `_tag-strip = none`
// membership's placement half.
func (led docTagLedger) placementTags(id string, spec groupSpec) []string {
	var out []string
	for _, app := range led.appearances[id] {
		if app.bucket == "" {
			continue
		}
		out = appendMissing(out, []string{placementBucketTag(app.bucket, spec)})
	}
	return out
}

// effectiveTags is the `_tag-strip = none` membership reading: the union of
// every box's tags and the placements' reconstructed bucket tags.
func (led docTagLedger) effectiveTags(id string, spec groupSpec) []string {
	var out []string
	for _, app := range led.appearances[id] {
		out = appendMissing(out, app.tags)
	}
	return appendMissing(out, led.placementTags(id, spec))
}

// placementBucketTag reconstructs the ONE tag a bucket placement expresses
// under the `none` strip reading: the bucket value itself for a whole-tag
// grouping, the namespace-reconstructed leaf for a namespace grouping.
func placementBucketTag(bucket string, spec groupSpec) string {
	if spec.Kind == groupKindTagNamespace {
		return reconstructNamespaceTag(spec.Namespace, bucket)
	}
	return bucket
}

// spellBucket re-spells a bucket value for a diagnostic through the one
// quoting rule; the ungrouped pseudo-bucket is named in prose.
func spellBucket(b string) string {
	if b == "" {
		return "ungrouped"
	}
	return trellis.QuoteIfNeeded(b)
}

// appendMissing appends each tag of add not already in dst, preserving order.
func appendMissing(dst, add []string) []string {
	for _, t := range add {
		if !slices.Contains(dst, t) {
			dst = append(dst, t)
		}
	}
	return dst
}

// subtractTags returns the members of a not in b, preserving a's order.
func subtractTags(a, b []string) []string {
	var out []string
	for _, t := range a {
		if !slices.Contains(b, t) {
			out = append(out, t)
		}
	}
	return out
}
