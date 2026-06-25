package jira

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// decomposedIssue is one issue split into the leaf bodies that become its
// own merkle child nodes: the field values (with the independently-edited
// blobs lifted out), the ADF description, and one body per comment. Each
// body is canonical JSON (Go marshals map keys sorted), so an unchanged
// part hashes identically across captures and dedups.
type decomposedIssue struct {
	fields      []byte
	description []byte // nil when the issue has no description
	comments    []decomposedComment
}

// decomposedComment is one comment's stable id and canonical-JSON body.
type decomposedComment struct {
	id   string
	body []byte
}

// decomposeIssue splits a captured `*all` issue into its merkle leaves. The
// description and comments are removed from the fields body and emitted as
// their own nodes, so editing a comment or the description rewrites only
// that leaf — not the (large) fields blob (FDR 0019 §Dedup properties). The
// issue key and any non-fields top-level members are preserved in the
// fields body so it remains a faithful, self-describing record.
func decomposeIssue(iss issue) (decomposedIssue, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(iss.data, &top); err != nil {
		return decomposedIssue{}, errors.Wrapf(err, "jira plugin: decompose issue %s", iss.key)
	}

	var fields map[string]json.RawMessage
	if raw, ok := top["fields"]; ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &fields); err != nil {
			return decomposedIssue{}, errors.Wrapf(err, "jira plugin: parse fields of %s", iss.key)
		}
	}

	var d decomposedIssue

	// Lift the ADF description out of fields into its own node.
	if raw, ok := fields["description"]; ok && !isJSONNull(raw) {
		body, err := canonicalJSON(raw)
		if err != nil {
			return decomposedIssue{}, errors.Wrapf(err, "jira plugin: description of %s", iss.key)
		}
		d.description = body
		delete(fields, "description")
	}

	// Lift each comment out of fields.comment.comments into its own node.
	if raw, ok := fields["comment"]; ok && len(raw) > 0 {
		comments, err := decomposeComments(iss.key, raw)
		if err != nil {
			return decomposedIssue{}, err
		}
		d.comments = comments
		delete(fields, "comment")
	}

	// Re-attach the trimmed fields under the issue's top-level members so
	// the fields node still records the issue key, self link, etc.
	if fields != nil {
		trimmed, err := json.Marshal(fields)
		if err != nil {
			return decomposedIssue{}, errors.Wrap(err)
		}
		top["fields"] = trimmed
	}
	fieldsBody, err := canonicalJSONMap(top)
	if err != nil {
		return decomposedIssue{}, errors.Wrapf(err, "jira plugin: fields body of %s", iss.key)
	}
	d.fields = fieldsBody

	return d, nil
}

// decomposeComments extracts the comments array from the issue's `comment`
// field (the `{comments: [...], total, ...}` shape returned under
// fields.comment). Each comment is keyed by Jira's stable comment id; when a
// comment carries no id (a defensive case — Jira Cloud always assigns one),
// the alias falls back to a content hash of the canonical body rather than
// the array position, so deleting one comment never shifts the aliases of
// the others (which a positional index would, spuriously rehashing every
// following comment and breaking the issue-subtree reuse contract).
func decomposeComments(issueKey string, rawComment json.RawMessage) ([]decomposedComment, error) {
	var wrapper struct {
		Comments []json.RawMessage `json:"comments"`
	}
	if err := json.Unmarshal(rawComment, &wrapper); err != nil {
		return nil, errors.Wrapf(err, "jira plugin: parse comments of %s", issueKey)
	}

	out := make([]decomposedComment, 0, len(wrapper.Comments))
	for _, raw := range wrapper.Comments {
		var meta struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil {
			return nil, errors.Wrapf(err, "jira plugin: parse comment id of %s", issueKey)
		}
		body, err := canonicalJSON(raw)
		if err != nil {
			return nil, errors.Wrapf(err, "jira plugin: canonicalize comment of %s", issueKey)
		}
		id := meta.ID
		if id == "" {
			id = "anon-" + contentToken(body)
		}
		out = append(out, decomposedComment{id: id, body: body})
	}
	return out, nil
}

// contentToken is a short, position-independent identity token derived from a
// blob's canonical bytes — used as a stable fallback alias for a comment that
// carries no Jira id.
func contentToken(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:8])
}

// canonicalJSONMap canonicalizes an already-parsed object (the issue's
// top-level members). It marshals the map (sorted keys) and routes the
// result back through canonicalJSON: the round-trip is load-bearing, not
// redundant — each value is a json.RawMessage emitted verbatim, so without
// re-canonicalizing, *nested* object key order (which Jira does not
// guarantee stable) would leak through and an unchanged issue could hash
// differently across captures.
func canonicalJSONMap(m map[string]json.RawMessage) ([]byte, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return canonicalJSON(raw)
}

// isJSONNull reports whether a raw JSON value is the literal null.
func isJSONNull(raw json.RawMessage) bool {
	return string(raw) == "null"
}
