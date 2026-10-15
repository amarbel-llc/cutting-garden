package org

import (
	"bytes"
	"context"
	"crypto/rand"
	stderrors "errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/org/orgtext"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The quote write (FDR 0026 D12, cutting-garden#309):
// `patch_node <headline> {"quote": true}` makes a headline's reference
// durable: the plugin mints its :ID: (the agent does not choose it) and
// adds the own tag zz-agent_quoted, which never inherits. It is one case
// of a headline edit (edit.go), sharing its write path: a WebDAV LOCK
// (Fastmail Files ignores conditional headers, #267), then LOCK → GET →
// splice → PUT with the token → UNLOCK. This file holds that path's
// shared pieces.

const fieldQuote = "quote"

// lockRetryDelays is the 423 backoff, as circus's pebble uses: 5 tries
// sleeping 1–5 s, about 15 s in all, well under the 60 s lock lifetime.
// A variable so tests can shorten it.
var lockRetryDelays = []time.Duration{
	1 * time.Second, 2 * time.Second, 3 * time.Second, 4 * time.Second, 5 * time.Second,
}

// idShadowed reports a copy-pasted headline whose :ID: an earlier headline
// already holds: its ID does not address it, so a quote replaces it.
func (n *hnode) idShadowed() bool {
	return n.h.ID() != "" && n.uri.Query().Get("id") == ""
}

func (t target) lockWithRetry(ctx context.Context) (string, error) {
	for attempt := 0; ; attempt++ {
		token, err := t.client.lock(ctx, t.ref.path)
		if !stderrors.Is(err, errLocked) {
			return token, err
		}
		if attempt == len(lockRetryDelays) {
			var total time.Duration
			for _, d := range lockRetryDelays {
				total += d
			}
			return "", errors.ErrorWithStackf(
				"org plugin: %s stayed locked by another writer (423); retried %d× over %s; nothing was written",
				t.ref.path, len(lockRetryDelays), total)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(lockRetryDelays[attempt]):
		}
	}
}

// tagSuffixRe finds a headline line's trailing tag block: the gap before
// it, then the block.
var tagSuffixRe = regexp.MustCompile(`([ \t]+)(:(?:[^\s:]+:)+)[ \t]*$`)

// idLineRe matches a drawer's ID property line, value or not.
var idLineRe = regexp.MustCompile(`(?i)^[ \t]*:ID:`)

// spliceDrawerID replaces the drawer's existing :ID: line (empty or
// shadowed) with idLine, or inserts idLine after `:PROPERTIES:` when there
// is none, so the drawer never holds two IDs.
func spliceDrawerID(src []byte, drawer orgtext.Span, idLine, nl string) []byte {
	start := drawer.Start
	for start < drawer.End {
		end := start + bytes.IndexByte(src[start:drawer.End], '\n') + 1
		if end <= start { // last line without a newline
			end = drawer.End
		}
		line := bytes.TrimRight(src[start:end], "\r\n")
		if idLineRe.Match(line) {
			out := append([]byte(nil), src[:start]...)
			out = append(out, idLine...)
			return append(out, src[start+len(line):]...)
		}
		start = end
	}
	afterOpen := drawer.Start + bytes.IndexByte(src[drawer.Start:], '\n') + 1
	return insertAt(src, afterOpen, []byte(idLine+nl))
}

func insertAt(b []byte, at int, ins []byte) []byte {
	out := make([]byte, 0, len(b)+len(ins))
	out = append(out, b[:at]...)
	out = append(out, ins...)
	return append(out, b[at:]...)
}

// newUUID returns a random (version 4) UUID, the form org-id uses.
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("org plugin: crypto/rand: %v", err))
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// The rest of NodeMutator: headlines are only patched (FDR 0026 slice 3a);
// creating, replacing and deleting are not supported.
func (Plugin) CreateNode(context.Context, *url.URL, io.Reader, string) error {
	return errOnlyPatch
}

func (Plugin) PutNode(context.Context, *url.URL, io.Reader) error { return errOnlyPatch }

func (Plugin) DeleteNode(context.Context, *url.URL) error { return errOnlyPatch }

var errOnlyPatch = errors.BadRequestf(
	`org plugin: the only supported write is a patch of a headline (FDR 0026)`)

// DescribeBodies declares the headline's patch for describe_node_types.
func (Plugin) DescribeBodies() []cutting_garden_plugins.NodeTypeBody {
	return []cutting_garden_plugins.NodeTypeBody{{
		Tag: typeHeadline,
		Accepts: []string{
			`application/json, patch ops only, any of: "title" (string), "state" (a TODO keyword of the ` +
				`file, or null), "priority" ("A".."Z" or null), "own_tags" (the headline's own tags, the ` +
				`whole set), "scheduled" / "deadline" ("YYYY-MM-DD" with an optional " HH:MM", or null), ` +
				`"quote" (true: also add the zz-agent_quoted tag). The body is never edited. Any edit ` +
				`mints the headline's :ID: when it has none, so read the node again for its ?id= URI; ` +
				`entering or leaving a done state adds or removes CLOSED. A lost_to_concurrent_sync ` +
				`error means another writer (an Orgzly sync) overwrote the edit: re-read and re-apply.`,
		},
		Example: map[string]any{fieldPatchState: "DONE", fieldPatchScheduled: "2026-10-20"},
	}}
}

var (
	_ cutting_garden_plugins.NodeMutator   = (*Plugin)(nil)
	_ cutting_garden_plugins.BodyDescriber = (*Plugin)(nil)
)
