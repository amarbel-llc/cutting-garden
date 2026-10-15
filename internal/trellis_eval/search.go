package trellis_eval

import (
	"context"
	stderrors "errors"
	"fmt"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Search acceleration for `_body` predicates (RFC 0016). When the plugin is
// a Searcher, the first `_body*=` (substring) or `_body~=` (regex) value
// asks it once for every matching node under the anchor — the whole tree
// the evaluator can reach — and later nodes are answered from that set
// instead of a leaf fetch each. A complete result decides both ways; an
// incomplete one only confirms a match, and a node outside it falls back to
// the leaf read, so the answer never depends on the index being whole. A
// mode the plugin does not accelerate falls back to the walk; any other
// search error fails the query.

// searchHits is one cached search: the matching URIs and whether the set
// covers every match. unsupported marks a mode the plugin declined.
type searchHits struct {
	uris        map[string]struct{}
	complete    bool
	unsupported bool
	reported    bool // an incomplete words answer was reported partial
}

type searchKey struct {
	mode  cgp.SearchMode
	query string
}

func searchModeFor(op trellis.FieldOp) (cgp.SearchMode, bool) {
	switch op {
	case trellis.FieldOpContains:
		return cgp.SearchSubstring, true
	case trellis.FieldOpRegex:
		return cgp.SearchRegex, true
	default:
		return "", false
	}
}

// searchBody answers fp for n from the plugin's search when it can.
// decided is false when the caller must read the leaf instead.
func (ev *evaluator) searchBody(
	ctx context.Context, n cgp.Node, fp trellis.FieldPred,
) (matched, decided bool, err error) {
	if ev.searcher == nil || n.URI == nil || ev.anchor == nil {
		return false, false, nil
	}
	mode, ok := searchModeFor(fp.Op)
	if !ok {
		return false, false, nil
	}
	uri := n.URI.String()
	allComplete := true
	for _, v := range fp.Values {
		hits, err := ev.searchFor(ctx, mode, valueString(v))
		if err != nil {
			return false, false, err
		}
		if hits.unsupported {
			return false, false, nil
		}
		if _, ok := hits.uris[uri]; ok {
			return true, true, nil
		}
		allComplete = allComplete && hits.complete
	}
	return false, allComplete, nil
}

// wordsField is the reserved `_words` field (RFC 0016 §words): whole-word,
// case-insensitive matching by the plugin's own tokenizer, answered only by
// a plugin's words search. There is no walk to fall back to, so a plugin
// without it makes the query a bad request, and an incomplete answer is
// returned as it is with the query reported partial.
const wordsField = "_words"

func (ev *evaluator) matchWords(ctx context.Context, n cgp.Node, fp trellis.FieldPred) (bool, error) {
	if fp.Op != trellis.FieldOpEq {
		return false, errors.BadRequestf(
			"trellis: `_words` takes `=` (a word, or a [list] of words, any of which matches), not %s",
			fp.Op)
	}
	if ev.searcher == nil || ev.anchor == nil {
		return false, errors.BadRequestf(
			"trellis: `_words` needs a plugin that searches words; this one does not (use `_body*=`)")
	}
	uri := n.URIString()
	for _, v := range fp.Values {
		word := valueString(v)
		hits, err := ev.searchFor(ctx, cgp.SearchWords, word)
		if err != nil {
			return false, err
		}
		if hits.unsupported {
			return false, errors.BadRequestf(
				"trellis: `_words` needs a plugin that searches words; this one does not (use `_body*=`)")
		}
		if !hits.complete && !hits.reported {
			cgp.ReportPartialListing(ctx, ev.anchor, fmt.Sprintf(
				"the plugin's word search for %q reported its result incomplete", word))
			hits.reported = true
			ev.searches[searchKey{cgp.SearchWords, word}] = hits
		}
		if _, ok := hits.uris[uri]; ok {
			return true, nil
		}
	}
	return false, nil
}

func (ev *evaluator) searchFor(ctx context.Context, mode cgp.SearchMode, query string) (searchHits, error) {
	key := searchKey{mode, query}
	if hits, ok := ev.searches[key]; ok {
		return hits, nil
	}
	res, err := ev.searcher.Search(ctx, cgp.SearchRequest{Root: ev.anchor, Query: query, Mode: mode})
	var hits searchHits
	switch {
	case stderrors.Is(err, cgp.ErrSearchModeUnsupported):
		hits.unsupported = true
	case err != nil:
		return hits, errors.Wrapf(err, "trellis: search %s %q", mode, query)
	default:
		hits.complete = res.Complete
		hits.uris = make(map[string]struct{}, len(res.Nodes))
		for _, n := range res.Nodes {
			hits.uris[n.URIString()] = struct{}{}
		}
	}
	if ev.searches == nil {
		ev.searches = map[searchKey]searchHits{}
	}
	ev.searches[key] = hits
	return hits, nil
}
