package cutting_garden_plugins

import (
	"context"
	stderrors "errors"
	"net/url"
)

// SearchMode selects the matching algorithm a search request evaluates
// (RFC 0016 §1).
type SearchMode string

const (
	// SearchSubstring is a plain, unanchored, EXACT-CASE substring match
	// over the same raw text `_body` reads: a node matches exactly when
	// trellis's `_body*=` walk would match it. A Searcher MUST implement it.
	SearchSubstring SearchMode = "substring"
	// SearchRegex is an unanchored RE2 (Go regexp) match, as trellis's
	// `_body~=`. Optional: a plugin may return ErrSearchModeUnsupported.
	SearchRegex SearchMode = "regex"
	// SearchWords is a whole-word, case-insensitive match under the
	// plugin's own documented tokenizer (which words it indexes, how it
	// folds accents, what text and which nodes it covers) — trellis's
	// `_words` field. Unlike the other modes it has no walk to agree with:
	// the plugin's definition IS the answer, so a plugin MAY match
	// containers whose text it indexes. Optional.
	SearchWords SearchMode = "words"
)

// SearchRequest is one search over a subtree (RFC 0016 §1).
type SearchRequest struct {
	// Root is the subtree to search, recursively, including Root itself.
	// MUST be non-nil.
	Root *url.URL
	// Query is the substring or RE2 pattern, per Mode. MUST be non-empty.
	Query string
	// Mode selects the matching algorithm.
	Mode SearchMode
	// Filter narrows the matches by facet membership, AND-composed with
	// the text match (RFC 0012 §6, reused unmodified). Empty matches all.
	Filter FacetFilter
	// Limit caps the returned Nodes; zero is unbounded. Truncating to
	// honor it makes the result incomplete.
	Limit int
}

// SearchResult is the set of matching nodes plus completeness (RFC 0016
// §1, §4).
type SearchResult struct {
	// Nodes match anywhere in Root's subtree, in no particular order.
	Nodes []Node
	// Complete is false when the result is known not to cover every
	// match (a capped or sampled index, or Limit truncation). Consumers
	// MUST NOT present a false Complete as exhaustive.
	Complete bool
}

// Searcher is the OPTIONAL capability that returns every node in a
// subtree whose searchable text matches a query, so the trellis
// evaluator answers `_body*=` / `_body~=` without fetching every leaf
// (RFC 0016). By default the searched text is exactly what `_body`
// tests — a leaf's text body — so a search and the evaluator's walk agree
// on which nodes match (§2). Probed by type assertion.
type Searcher interface {
	RootLister

	// Search answers req for any node in this plugin's namespace. It
	// returns ErrSearchModeUnsupported (bare or wrapped) for a Mode it
	// does not accelerate; any other error is a genuine failure.
	Search(ctx context.Context, req SearchRequest) (SearchResult, error)
}

// ErrSearchModeUnsupported is what Search returns for a Mode the plugin
// does not accelerate (RFC 0016 §5): the caller falls back to walking.
var ErrSearchModeUnsupported = stderrors.New("cutting_garden_plugins: search mode unsupported")
