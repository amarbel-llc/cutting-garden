package mcp

import (
	"encoding/json"
	"fmt"
)

// listingByteCap bounds the node bytes of a filtered or queried list_nodes
// result when the caller gave no limit (cutting-garden#363). 60,000 B is about
// 15,000 tokens at ~4 B/token, safely under the ~25,000-token result limits
// of the MCP clients this serves. For scale: 230 caldav VTODOs enriched and
// indented measured 187,572 B (about 815 B each), so the cap keeps roughly 70
// of them and says how many more there are. The wrapper's own fields add a
// small constant on top. The unfiltered default path (#86) is not capped.
const listingByteCap = 60_000

// listingTruncation is the wrapper's truncation trailer; all omitted when the
// whole listing fit.
type listingTruncation struct {
	// Truncated is true when the cap cut the listing short; Shown is how many
	// nodes the page carries and Total the true match count (before offset).
	Truncated bool   `json:"truncated,omitempty"`
	Shown     int    `json:"shown,omitempty"`
	Total     int    `json:"total,omitempty"`
	Hint      string `json:"hint,omitempty"`
}

// nodeCost is the bytes a node adds to the indented output: its own indented
// JSON plus the separating comma and newline.
func nodeCost(v nodeView) int {
	b, err := json.MarshalIndent(v, "    ", "  ")
	if err != nil {
		return 0
	}
	return len(b) + 2
}

// capViews applies offset and limit to views, and — only when limit is 0, the
// caller having given no bound — keeps the longest prefix whose nodes cost at
// most budget bytes, always at least one node so a single large node is never
// an empty page. The trailer reports what was cut.
func capViews(views []nodeView, offset, limit, budget int) ([]nodeView, listingTruncation) {
	total := len(views)
	page := paginate(views, offset, limit)
	if limit > 0 {
		return page, listingTruncation{}
	}
	used, keep := 0, 0
	for _, v := range page {
		c := nodeCost(v)
		if keep > 0 && used+c > budget {
			break
		}
		used += c
		keep++
	}
	if keep == len(page) {
		return page, listingTruncation{}
	}
	return page[:keep], listingTruncation{
		Truncated: true,
		Shown:     keep,
		Total:     total,
		Hint: fmt.Sprintf(
			"%d more not shown (output capped at about %d bytes): pass limit and offset=%d to page, "+
				"narrow with a filter, or keep fewer keys with fields",
			len(page)-keep, budget, offset+keep),
	}
}
