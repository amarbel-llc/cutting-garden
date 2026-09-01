package organize

import (
	"sort"
	"strings"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// membershipEdit is one live node and the complete replacement tag set the
// multi-valued-dimension write path persists for it (RFC 0019 §1). Distinct from
// an objectFieldEdit (a single-valued box-atom patch): a membership edit re-files
// a node's tag SET, computed by folding the edited document's per-bucket set-diff
// through the tag interpreter onto the live tags.
type membershipEdit struct {
	// URI is the live node's URI string — the sort key and the write-back address.
	URI string
	// Node is the live node the interpreter's replacement set is written back to.
	Node cgp.Node
	// NewTags is the complete replacement tag set to persist (not a delta).
	NewTags []string
}

// planMemberships is the multi-valued-dimension sibling of planFieldEdits: it
// three-way-merges each object's tag-set membership across the pinned base, the
// edited document, and the re-queried live node. Because tag adds/removes commute
// and TagInterpreter.Complete is idempotent, the merge folds the document's
// per-object set-diff (buckets added and removed relative to base) onto the LIVE
// tag set through the interpreter — a bucket a concurrent edit already changed
// simply folds to a no-op, so this slice needs no hard conflict path (unlike the
// single-valued field merge). Only ids present in base are iterated (a brand-new
// object is out of scope, mirroring planFieldEdits ignoring added lines); an id in
// base but not live is skipped. Deleting an object's last line — an id in base and
// live but ABSENT from the edited document entirely — is out of scope
// (cutting-garden#215's `%:allow-deletion` gate) and rejects loudly; an object
// still present but moved out of every bucket (present ungrouped, empty membership)
// is a legal remove-all.
//
// namespace is EMPTY for a whole-dimension (or field) grouping — the document
// buckets are then FULL tags and each add/remove folds through the interpreter
// exactly (`Complete(TagAdd/Remove, "work")`). For a tag-NAMESPACE rollup grouping
// (`--group-by project` over categories, RFC 0019 §6) namespace is the segment
// prefix (`project`) and the document buckets are namespace SEGMENTS (`-client`,
// carrying their leading `-`): an ADD reconstructs the namespace tag
// (`project-client`, the only unambiguous leaf, §6.2) and a REMOVE enumerates the
// live tags realizing that rollup subtree and exact-removes each — the interpreter
// Complete is EXACT (never a subtree remove), so subtree removal is this apply
// layer's job (§6.2). The G10a ROOT bucket — the bucket whose value IS the
// namespace, i.e. a line placed directly under the `# project` heading — is a tag
// bucket whose reconstruction is exactly the bare namespace tag: ADD appends
// `project`, and REMOVE drops the bare `project` ONLY, never the subtree — the
// deeper `project-*` tags realize the CONTINUATION buckets, whose removal is
// governed by their own bucket rows, so the one-bucket-one-realizing-set rule the
// continuations follow gives the root exactly the bare tag.
func planMemberships(
	edited, base document,
	live []cgp.Node,
	anchor string,
	interp cgp.TagInterpreter,
	dim string,
	namespace string,
) ([]membershipEdit, error) {
	baseM, err := base.memberships(true)
	if err != nil {
		return nil, errors.Wrapf(err, "organize: base memberships")
	}
	editedM, err := edited.memberships(true)
	if err != nil {
		return nil, errors.Wrapf(err, "organize: edited memberships")
	}

	// editedIDs records mere PRESENCE in the edited document (any line for the id,
	// regardless of membership), so a legal remove-all (present ungrouped) is
	// distinguished from a last-line-vanish (absent entirely).
	editedIDs := make(map[string]struct{})
	for _, ln := range edited.objectLines() {
		editedIDs[ln.ID] = struct{}{}
	}

	liveByID := make(map[string]cgp.Node, len(live))
	for _, n := range live {
		liveByID[relativeID(n.URIString(), anchor)] = n
	}

	ids := make([]string, 0, len(baseM))
	for id := range baseM {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var edits []membershipEdit
	var deleted []string
	for _, id := range ids {
		liveNode, ok := liveByID[id]
		if !ok {
			continue // in base but not live — out of scope, mirrors planFieldEdits.
		}
		if _, present := editedIDs[id]; !present {
			// Deleting an object's last line is out of scope (cutting-garden#215).
			// Accumulate every violation and batch them into one error after the
			// loop, so a user sees all deletions in a single apply attempt — the
			// pattern planMoves and planFieldEdits follow for conflicts.
			deleted = append(deleted, id)
			continue
		}

		baseSet := stringSet(baseM[id])
		editedSet := stringSet(editedM[id])
		adds := setDifference(editedSet, baseSet)
		removes := setDifference(baseSet, editedSet)
		if len(adds) == 0 && len(removes) == 0 {
			continue
		}

		liveTags := facetKeys(liveNode.Facets[dim])
		newTags := liveTags
		if namespace == "" {
			// Whole-dimension (or field) grouping: buckets ARE full tags, folded
			// through the interpreter exactly — the byte-for-byte slice-2 behavior.
			for _, bucket := range removes {
				newTags, err = interp.Complete(newTags, cgp.TagRemove, bucket)
				if err != nil {
					return nil, errors.Wrapf(err, "organize: %s remove %q", id, bucket)
				}
			}
			for _, bucket := range adds {
				newTags, err = interp.Complete(newTags, cgp.TagAdd, bucket)
				if err != nil {
					return nil, errors.Wrapf(err, "organize: %s add %q", id, bucket)
				}
			}
		} else {
			// Namespace rollup: a bucket is a segment (`-client`), not a full tag.
			// A REMOVE enumerates the live tags under the reconstructed namespace tag
			// and exact-removes each realizing tag (the subtree, apply-owned §6.2 —
			// Complete never does a subtree remove); an ADD reconstructs and appends
			// the namespace tag exactly (the unambiguous leaf).
			for _, bucket := range removes {
				if bucket == namespace {
					// The G10a ROOT bucket: the only tag realizing direct-root
					// placement is the bare namespace tag itself (deeper
					// `project-*` tags realize the continuation buckets), so
					// leaving the root removes exactly `project` — a subtree
					// enumeration here would wrongly strip continuation
					// memberships the document still shows.
					newTags, err = interp.Complete(newTags, cgp.TagRemove, namespace)
					if err != nil {
						return nil, errors.Wrapf(err, "organize: %s remove %q", id, namespace)
					}
					continue
				}
				fullTag := reconstructNamespaceTag(namespace, bucket)
				for _, liveTag := range liveTags {
					if liveTag == fullTag || strings.HasPrefix(liveTag, fullTag+"-") {
						newTags, err = interp.Complete(newTags, cgp.TagRemove, liveTag)
						if err != nil {
							return nil, errors.Wrapf(err, "organize: %s remove %q", id, liveTag)
						}
					}
				}
			}
			for _, bucket := range adds {
				fullTag := reconstructNamespaceTag(namespace, bucket)
				newTags, err = interp.Complete(newTags, cgp.TagAdd, fullTag)
				if err != nil {
					return nil, errors.Wrapf(err, "organize: %s add %q", id, fullTag)
				}
			}
		}

		if setEqual(newTags, liveTags) {
			// Live already matches the intended set (e.g. a concurrent edit applied
			// it) — nothing to write. Compared as SETS, not slice order.
			continue
		}

		edits = append(edits, membershipEdit{
			URI:     liveNode.URIString(),
			Node:    liveNode,
			NewTags: newTags,
		})
	}

	if len(deleted) > 0 {
		sort.Strings(deleted)
		return nil, errors.BadRequestf(
			"organize --apply: %d object(s) deleted from the edited document "+
				"(last-line deletion is out of scope, cutting-garden#215); "+
				"regenerate and re-edit:\n  %s",
			len(deleted), strings.Join(deleted, "\n  "),
		)
	}

	sort.Slice(edits, func(i, j int) bool { return edits[i].URI < edits[j].URI })
	return edits, nil
}

// reconstructNamespaceTag rebuilds the full namespace tag a rollup bucket stands
// for (RFC 0019 §6.2): the bucket carries its leading `-` (dodder-hyphen's
// continuation form, `-client`), so `"project" + "-client" = "project-client"`.
// The G10a ROOT bucket — the bucket value equal to the namespace itself, from a
// line placed directly under the `# project` heading — reconstructs to exactly
// the BARE namespace tag (`project`). The interpreter always yields a
// `-<segment>` continuation bucket, so plain concatenation is correct there; the
// leading-`-` guard is defence against a bucket that somehow lacks it.
func reconstructNamespaceTag(namespace, bucket string) string {
	if bucket == namespace {
		return namespace
	}
	if strings.HasPrefix(bucket, "-") {
		return namespace + bucket
	}
	return namespace + "-" + bucket
}

// facetKeys projects a dimension's live facet values to their tag strings.
func facetKeys(values []cgp.FacetValue) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, v.Key)
	}
	return out
}

// stringSet dedups a slice into a set.
func stringSet(items []string) map[string]struct{} {
	set := make(map[string]struct{}, len(items))
	for _, s := range items {
		set[s] = struct{}{}
	}
	return set
}

// setDifference returns the sorted elements in a not present in b.
func setDifference(a, b map[string]struct{}) []string {
	var out []string
	for s := range a {
		if _, ok := b[s]; !ok {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// setEqual reports whether two slices carry the same SET of strings, ignoring
// order and duplicates.
func setEqual(a, b []string) bool {
	sa, sb := stringSet(a), stringSet(b)
	if len(sa) != len(sb) {
		return false
	}
	for s := range sa {
		if _, ok := sb[s]; !ok {
			return false
		}
	}
	return true
}
