package cutting_garden_plugins

import (
	"strings"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// TagMembership is one node's placement in one grouping bucket, as computed by
// TagInterpreter.Buckets (RFC 0019 §1). It is a pure value pair, safe to
// serialize.
type TagMembership struct {
	// Bucket is the grouping bucket a tag places the node under — a whole
	// normalized tag (whole-dimension grouping) or an immediate-segment rollup
	// key (namespace grouping). MUST be non-empty.
	Bucket string
	// Via is the full, normalized tag that produced this membership. It is
	// REQUIRED, not decorative: the write path (Complete) and conflict messages
	// need to name the exact tag a bucket came from, since a rollup bucket
	// (dodder-hyphen's "-client") may be produced by several distinct tags. Via
	// MUST be a tag actually present (after Normalize) in the input set.
	Via string
}

// TagMembershipOp selects the direction of a membership edit passed to
// TagInterpreter.Complete (RFC 0019 §1).
type TagMembershipOp string

const (
	// TagAdd adds a membership: the returned tag set includes whatever tag the
	// bucket edit implies, if not already present.
	TagAdd TagMembershipOp = "add"
	// TagRemove removes a membership: the returned tag set drops whatever tag
	// the bucket edit implies, if present.
	TagRemove TagMembershipOp = "remove"
)

// TagInterpreter governs the match/group/write-back semantics of one tag field
// (RFC 0019 §1). It is applied HOST-side over the raw tag strings a data plugin
// emits (RFC 0019 §2); a data plugin — linked or wire (RFC 0013) — never
// implements it. Every method is a pure function of its arguments: same inputs,
// same outputs, no side effects, no hidden state, no I/O.
//
// The contract is wire-shaped and this is normative (RFC 0019 §2): every
// method's arguments and results are plain values (strings, string slices,
// TagMembership, TagMembershipOp) precisely so a future RFC 0013-style JSON-RPC
// transport (RFC 0019 §8) can carry the exact method set unchanged. An
// implementation MUST keep this interface serializable — no method may take or
// return a callback, an iterator/Seq, a channel, a context.Context, or any
// interface whose behavior cannot be reduced to values on a wire.
type TagInterpreter interface {
	// Normalize returns the canonical form of a single tag. It MUST be
	// idempotent: Normalize(Normalize(t)) == Normalize(t). It is the identity
	// for both builtins (naive and dodder-hyphen impose no canonical rewrite —
	// `_` is literal, no lift, RFC 0019 §7); a future interpreter MAY define a
	// non-identity canonicalization.
	Normalize(tag string) string

	// SortKey returns the lexical sort key for a single tag — the string a
	// consumer orders normalized tags and buckets by. Both builtins use plain
	// lexical order (a leading `_`/`_ ` sorts high as a natural consequence of
	// ASCII order, needing no special lift, RFC 0019 §7); a future interpreter
	// MAY return a key that differs from the tag for interpreter-specific
	// ordering.
	SortKey(tag string) string

	// Buckets computes the node's grouping memberships for a grouping request.
	// tags is the node's full, un-normalized tag set (batch by node). namespace
	// selects the grouping dimension: the empty namespace is WHOLE-DIMENSION
	// grouping (each normalized tag is its own bucket, Bucket == Via); a
	// non-empty namespace is NAMESPACE grouping — an interpreter that declares
	// no namespaces (naive) MUST reject it as a bad request (RFC 0019 §5). The
	// result is deduplicated by Bucket per node. An empty result (the node has
	// no tags in the requested dimension) is normal and MUST NOT be an error.
	Buckets(tags []string, namespace string) ([]TagMembership, error)

	// Matches reports whether the node's tag set satisfies a bare query term.
	// tags is the node's full un-normalized set; term is a single tag query
	// term (the bare-identifier term RFC 0014 defers to this contract). naive
	// matches exactly (RFC 0019 §5); dodder-hyphen matches transitively along
	// the segment path (RFC 0019 §6.2).
	Matches(tags []string, term string) bool

	// Complete computes the new full tag set after a membership edit: adding or
	// removing (per op) the node's placement in bucket. tags is the node's
	// current full set; bucket is the grouping bucket the placement changed
	// under. The result is the complete replacement tag set the write path
	// persists. Membership is a SET: TagAdd of a bucket already represented is a
	// no-op returning the set unchanged, and TagRemove of an absent bucket
	// likewise. What tag a bucket edit implies is interpreter-defined (naive:
	// the exact bucket string, RFC 0019 §5). An edit the interpreter cannot
	// express as a tag-set delta MUST be a bad request, never a silent no-op.
	Complete(tags []string, op TagMembershipOp, bucket string) ([]string, error)
}

// tagInterpreterRegistry maps a name to its registered TagInterpreter
// (RFC 0019 §3). Builtins register at package init; a future
// [[tag_interpreters]] wire stanza (RFC 0019 §8) would register wire-backed
// names into this SAME namespace, indistinguishable to a consumer from a
// builtin.
var tagInterpreterRegistry = map[string]TagInterpreter{
	"naive":         naiveTagInterpreter{},
	"dodder-hyphen": dodderHyphenTagInterpreter{},
}

// LookupTagInterpreter resolves a registered interpreter by name (RFC 0019 §3).
// ok is false for an unregistered name; a consumer MUST NOT fall back to a
// default on a miss (that decision belongs to selection, RFC 0019 §4) — a miss
// at a point that required a named interpreter is a bad request.
func LookupTagInterpreter(name string) (ti TagInterpreter, ok bool) {
	ti, ok = tagInterpreterRegistry[name]
	return ti, ok
}

// wholeDimensionBuckets is the empty-namespace grouping shared by every
// interpreter (RFC 0019 §1): one TagMembership per distinct tag, with
// Bucket == Via == the tag, deduplicated in first-occurrence order. Both
// builtins' empty-namespace path is exactly this.
func wholeDimensionBuckets(tags []string) []TagMembership {
	ms := make([]TagMembership, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		if _, dup := seen[tag]; dup {
			continue
		}
		seen[tag] = struct{}{}
		ms = append(ms, TagMembership{Bucket: tag, Via: tag})
	}
	return ms
}

// naiveTagInterpreter is the exact-match degenerate (RFC 0019 §5) — the
// interpreter for a flat tag field with no hierarchy and no user lift
// convention. It is the default (RFC 0019 §4) and the only builtin implemented
// in slice 1.
type naiveTagInterpreter struct{}

var _ TagInterpreter = naiveTagInterpreter{}

// Normalize is the identity: naive imposes no canonical form, so `_inbox` and
// `inbox` are distinct tags (RFC 0019 §5).
func (naiveTagInterpreter) Normalize(tag string) string { return tag }

// SortKey is the identity: naive ordering is plain lexical over the tag string,
// with no lift (RFC 0019 §5).
func (naiveTagInterpreter) SortKey(tag string) string { return tag }

// Buckets returns one whole-dimension membership per distinct tag for the empty
// namespace, and rejects any non-empty namespace: naive declares no namespaces,
// so there is no rollup to compute (RFC 0019 §5).
func (naiveTagInterpreter) Buckets(
	tags []string,
	namespace string,
) ([]TagMembership, error) {
	if namespace != "" {
		return nil, errors.BadRequestf(
			"tag interpreter %q declares no namespaces", "naive",
		)
	}
	return wholeDimensionBuckets(tags), nil
}

// Matches is exact set membership: true iff term equals some tag in the node's
// set (RFC 0019 §5).
func (naiveTagInterpreter) Matches(tags []string, term string) bool {
	for _, tag := range tags {
		if tag == term {
			return true
		}
	}
	return false
}

// Complete appends bucket when absent (TagAdd) or drops it when present
// (TagRemove), returning the set unchanged on a no-op edit. The tag a naive
// bucket edit implies is the exact bucket string (RFC 0019 §5).
func (naiveTagInterpreter) Complete(
	tags []string,
	op TagMembershipOp,
	bucket string,
) ([]string, error) {
	present := false
	for _, tag := range tags {
		if tag == bucket {
			present = true
			break
		}
	}

	switch op {
	case TagAdd:
		if present {
			return tags, nil
		}
		return append(append([]string{}, tags...), bucket), nil
	case TagRemove:
		if !present {
			return tags, nil
		}
		out := make([]string, 0, len(tags))
		for _, tag := range tags {
			if tag != bucket {
				out = append(out, tag)
			}
		}
		return out, nil
	default:
		return nil, errors.BadRequestf(
			"tag interpreter %q: unknown membership op %q", "naive", op,
		)
	}
}

// dodderHyphenTagInterpreter imports dodder's tag algebra (RFC 0019 §6): hyphen
// (`-`) separated segments form a hierarchy, grouping by a namespace rolls
// deeper tags up to their immediate next segment (§6.1), bare-term matching is
// transitive along the segment path (§6.2), and write-back removes a bucket's
// whole segment subtree (§6.2). `_` is literal — no lift, no alias (§7) — so
// Normalize and SortKey stay the identity exactly as naive's do.
type dodderHyphenTagInterpreter struct{}

var _ TagInterpreter = dodderHyphenTagInterpreter{}

// Normalize is the identity: dodder-hyphen imposes no canonical form, and `_`
// is literal (no lift), so `_inbox` and `inbox` are distinct tags (RFC 0019
// §6, §7).
func (dodderHyphenTagInterpreter) Normalize(tag string) string { return tag }

// SortKey is the identity: ordering is plain lexical over the tag string. A
// leading `_`/`_ ` sorts high as a natural consequence of ASCII order, needing
// no special lift (RFC 0019 §7).
func (dodderHyphenTagInterpreter) SortKey(tag string) string { return tag }

// Buckets rolls the node's tags up to their immediate next segment beneath the
// namespace (RFC 0019 §6.1). The empty namespace is whole-dimension grouping —
// one membership per distinct tag, Bucket == Via — exactly as naive's is. A
// non-empty namespace N buckets each tag of the form `N-<rest>` by its first
// rest segment: Bucket is the continuation form "-<segment>" (§6.3), Via is the
// producing tag. A tag equal to N (no segment beneath it) or not under N
// contributes nothing. The result is deduplicated by Bucket per node — several
// tags rolling to the same bucket (project-client-acme, project-client-baxter
// → both -client) yield one membership, whose Via is the first contributor —
// and an empty result (no tags under the namespace) is normal, not an error.
func (dodderHyphenTagInterpreter) Buckets(
	tags []string,
	namespace string,
) ([]TagMembership, error) {
	if namespace == "" {
		return wholeDimensionBuckets(tags), nil
	}

	ms := make([]TagMembership, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	prefix := namespace + "-"
	for _, tag := range tags {
		rest, under := strings.CutPrefix(tag, prefix)
		if !under || rest == "" {
			continue
		}
		segment := rest
		if i := strings.IndexByte(rest, '-'); i >= 0 {
			segment = rest[:i]
		}
		bucket := "-" + segment
		if _, dup := seen[bucket]; dup {
			continue
		}
		seen[bucket] = struct{}{}
		ms = append(ms, TagMembership{Bucket: bucket, Via: tag})
	}
	return ms, nil
}

// Matches is transitive along the segment path: true iff some tag equals term
// or has term as a segment-prefix — i.e. starts with `term-` (RFC 0019 §6.2).
// So `project` matches `project-client-acme`, `project-client` matches it, but
// a non-segment-boundary prefix (`pro`) does not match `project`. naive's exact
// match is the degenerate where the only matching prefix is the whole tag.
func (dodderHyphenTagInterpreter) Matches(tags []string, term string) bool {
	prefix := term + "-"
	for _, tag := range tags {
		if tag == term || strings.HasPrefix(tag, prefix) {
			return true
		}
	}
	return false
}

// Complete edits the node's full tag set with EXACT add/remove of the bucket
// tag — identical to naive's (RFC 0019 §6.2 normatively pins only the exact
// append). It is deliberately NOT transitive: Complete receives a full tag
// string and cannot tell a whole-dimension bucket (`work`) from a reconstructed
// rollup tag (`project-client`), so a segment-prefix removal would wrongly strip
// an independent sibling like `work-urgent` when a node is removed from `work`.
// The hierarchy lives in Buckets (rollup) and Matches (transitive); the
// rollup-bucket write-back mechanics — reconstructing a bucket's namespace tag
// for an add (`-client` under namespace `project` → `project-client`) and
// enumerating a node's realizing tags for a remove — are the apply layer's
// responsibility (it holds the node's tags plus the namespace), so Complete
// always receives a full tag and edits it exactly. Both ops are idempotent and
// never mutate the input: the result is always a fresh slice.
func (dodderHyphenTagInterpreter) Complete(
	tags []string,
	op TagMembershipOp,
	bucket string,
) ([]string, error) {
	present := false
	for _, tag := range tags {
		if tag == bucket {
			present = true
			break
		}
	}

	switch op {
	case TagAdd:
		if present {
			return append([]string{}, tags...), nil
		}
		return append(append([]string{}, tags...), bucket), nil
	case TagRemove:
		if !present {
			return append([]string{}, tags...), nil
		}
		out := make([]string, 0, len(tags))
		for _, tag := range tags {
			if tag != bucket {
				out = append(out, tag)
			}
		}
		return out, nil
	default:
		return nil, errors.BadRequestf(
			"tag interpreter %q: unknown membership op %q", "dodder-hyphen", op,
		)
	}
}
