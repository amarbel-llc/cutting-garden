package org

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/org/orgtext"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// The quote write (FDR 0026 D12, cutting-garden#309): the plugin's only
// write. `patch_node <headline> {"quote": true}` makes a headline's
// reference durable: the plugin mints its :ID: (the agent does not choose
// it) and adds the own tag zz-agent_quoted, which never inherits. The
// write takes a WebDAV LOCK (Fastmail Files ignores conditional headers,
// #267): LOCK → GET → splice → PUT with the token → UNLOCK.

const fieldQuote = "quote"

// lockRetryDelays is the 423 backoff, as circus's pebble uses: 5 tries
// sleeping 1–5 s, about 15 s in all, well under the 60 s lock lifetime.
// A variable so tests can shorten it.
var lockRetryDelays = []time.Duration{
	1 * time.Second, 2 * time.Second, 3 * time.Second, 4 * time.Second, 5 * time.Second,
}

// PatchNode applies {"quote": true} to a headline. Other keys are
// tolerated and not applied. A headline already quoted changes nothing,
// so applied is empty.
func (Plugin) PatchNode(ctx context.Context, uri *url.URL, body io.Reader) ([]string, error) {
	var patch map[string]json.RawMessage
	if err := json.NewDecoder(body).Decode(&patch); err != nil {
		return nil, errors.BadRequestf("org plugin: patch body is not a JSON object: %s", err)
	}
	if len(patch) == 0 {
		return nil, errors.BadRequestf("org plugin: empty patch body")
	}
	raw, ok := patch[fieldQuote]
	if !ok {
		return []string{}, nil
	}
	var quote bool
	if err := json.Unmarshal(raw, &quote); err != nil {
		return nil, errors.BadRequestf("org plugin: quote must be true, got %s", raw)
	}
	if !quote {
		return nil, errors.BadRequestf("org plugin: quote must be true (unquoting is not supported)")
	}

	t, err := resolve(uri)
	if err != nil {
		return nil, err
	}
	if t.ref.kind != kindHeadline {
		return nil, errors.BadRequestf("org plugin: only a headline can be quoted, not %q", uri.String())
	}
	changed, err := t.quote(ctx)
	if err != nil {
		return nil, err
	}
	if !changed {
		return []string{}, nil
	}
	return []string{fieldQuote}, nil
}

func (t target) quote(ctx context.Context) (changed bool, err error) {
	token, err := t.lockWithRetry(ctx)
	if err != nil {
		return false, err
	}
	defer t.client.unlock(context.WithoutCancel(ctx), t.ref.path, token)

	// Fresh read under the lock; the cache may be stale.
	body, etag, err := t.client.get(ctx, t.ref.path)
	if err != nil {
		return false, err
	}
	pf := parseFile(t.acct, t.ref.path, etag, body)
	n, ok := pf.find(t.ref)
	if !ok {
		return false, errors.BadRequestf(
			"org plugin: no such headline in %s (renamed or moved?)", t.ref.path)
	}
	if n.h.Synthetic {
		return false, errors.BadRequestf(
			"org plugin: %s has no headlines; there is nothing to quote", t.ref.path)
	}
	updated, changed := spliceQuote(body, n.h, n.idShadowed())
	if !changed {
		return false, nil
	}
	defer resetCacheEntry(t.acct.Name, t.ref.path)
	err = t.client.put(ctx, t.ref.path, updated, token)
	var unknown outcomeUnknown
	if stderrors.As(err, &unknown) {
		// Still holding the lock, so a re-read settles it: if our bytes are
		// there, the write landed.
		if now, _, getErr := t.client.get(ctx, t.ref.path); getErr == nil && bytes.Equal(now, updated) {
			return true, nil
		}
	}
	if err != nil {
		return false, err
	}
	return true, nil
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

// tagSuffixRe finds a headline line's trailing tag block.
var tagSuffixRe = regexp.MustCompile(`([ \t]+)(:(?:[^\s:]+:)+)[ \t]*$`)

// idLineRe matches a drawer's ID property line, value or not.
var idLineRe = regexp.MustCompile(`(?i)^[ \t]*:ID:`)

// spliceQuote gives h a fresh :ID: (when it has none, an empty one, or a
// shadowed copy's) and the zz-agent_quoted own tag (when absent), touching
// nothing else. New lines use the headline's own line ending. changed is
// false when h already has both.
func spliceQuote(src []byte, h *orgtext.Headline, idShadowed bool) (out []byte, changed bool) {
	nl := "\n"
	if h.LineSpan.End < len(src) && src[h.LineSpan.End] == '\r' {
		nl = "\r\n"
	}
	out = append([]byte(nil), src...)

	// The drawer edit sits after the headline line, so splice it first and
	// the line's offsets stay valid.
	if h.ID() == "" || idShadowed {
		idLine := ":ID:       " + newUUID()
		if h.DrawerSpan.Empty() {
			ins := ":PROPERTIES:" + nl + idLine + nl + ":END:" + nl
			at := h.DrawerSpan.Start
			if at > 0 && src[at-1] != '\n' {
				ins = nl + ins // the file ended without a newline
			}
			out = insertAt(out, at, []byte(ins))
		} else {
			out = spliceDrawerID(out, h.DrawerSpan, idLine, nl)
		}
		changed = true
	}

	if !slices.Contains(h.OwnTags, agentQuotedTag) {
		line := string(src[h.LineSpan.Start:h.LineSpan.End])
		var newLine string
		if m := tagSuffixRe.FindStringSubmatchIndex(line); m != nil {
			newLine = line[:m[5]] + agentQuotedTag + ":" + line[m[5]:]
		} else {
			newLine = line + " :" + agentQuotedTag + ":"
		}
		out = append(out[:h.LineSpan.Start:h.LineSpan.Start], append([]byte(newLine), out[h.LineSpan.End:]...)...)
		changed = true
	}
	return out, changed
}

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

// The rest of NodeMutator: quote is the only write (FDR 0026 D7/D8 defer
// everything else).
func (Plugin) CreateNode(context.Context, *url.URL, io.Reader, string) error {
	return errOnlyQuote
}

func (Plugin) PutNode(context.Context, *url.URL, io.Reader) error { return errOnlyQuote }

func (Plugin) DeleteNode(context.Context, *url.URL) error { return errOnlyQuote }

var errOnlyQuote = errors.BadRequestf(
	`org plugin: the only supported write is patch_node {"quote": true} on a headline (FDR 0026 D12)`)

// DescribeBodies declares the headline's one write for describe_node_types.
func (Plugin) DescribeBodies() []cutting_garden_plugins.NodeTypeBody {
	return []cutting_garden_plugins.NodeTypeBody{{
		Tag: typeHeadline,
		Accepts: []string{
			`application/json, patch_node only: {"quote": true} mints the headline's :ID: and adds the ` +
				`zz-agent_quoted tag, making its ?id= URI durable; read the node again for the id`,
		},
		Example: map[string]any{fieldQuote: true},
	}}
}

var (
	_ cutting_garden_plugins.NodeMutator   = (*Plugin)(nil)
	_ cutting_garden_plugins.BodyDescriber = (*Plugin)(nil)
)
