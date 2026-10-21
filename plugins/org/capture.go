package org

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"slices"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/plugins/org/orgtext"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Capture (FDR 0026 D7-E): an agent creates a headline with
// ContainerCreator.CreateChild — a mutate_nodes create with a temp id and
// `in` set to a file (a level-1 headline at its end) or a headline (its
// last child). The body is a JSON object with a required "title" and any
// of the patch keys "state", "priority", "own_tags", "scheduled",
// "deadline" and "body". The plugin mints the :ID: (so the create returns
// a durable ?id= URI), stamps :CREATED: with the current time in the
// account's timezone, and adds the
// own tag zz-agent_captured, which, like zz-agent_quoted, never inherits.
// The write takes the edit path: LOCK → GET → insert → PUT → UNLOCK, then
// the read-back now and after readBackDelay (D8-C).

// agentCapturedTag marks a headline an agent created.
const agentCapturedTag = "zz-agent_captured"

var _ cutting_garden_plugins.ContainerCreator = Plugin{}

// CreateChild captures a headline under container.
func (Plugin) CreateChild(
	ctx context.Context, container *url.URL, body io.Reader, typ string,
) (*url.URL, error) {
	if typ != "" && typ != typeHeadline {
		return nil, errors.BadRequestf("org plugin: only %s can be created, not %q", typeHeadline, typ)
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	e, title, err := parseCapture(raw)
	if err != nil {
		return nil, err
	}
	t, err := resolve(container)
	if err != nil {
		return nil, err
	}
	if t.ref.kind != kindFile && t.ref.kind != kindHeadline {
		return nil, errors.BadRequestf(
			"org plugin: a headline is created in a file or under a headline, not in %q", container)
	}
	return t.capture(ctx, title, e, t.acct.now())
}

// parseCapture reads a create body: the edit keys plus a required title.
func parseCapture(raw []byte) (headlineEdit, string, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return headlineEdit{}, "", errors.BadRequestf("org plugin: create body is not a JSON object: %s", err)
	}
	for _, key := range []string{fieldQuote, fieldPatchParent, fieldPatchBefore, fieldPatchAfter} {
		if _, ok := keys[key]; ok {
			return headlineEdit{}, "", errors.BadRequestf(
				"org plugin: %q is not a create key (the container sets the place; a new headline is "+
					"tagged %s instead of quoted)", key, agentCapturedTag)
		}
	}
	if _, ok := keys[fieldPatchTitle]; !ok {
		return headlineEdit{}, "", errors.BadRequestf(`org plugin: a new headline needs a "title"`)
	}
	e, err := parseEdit(bytes.NewReader(raw))
	if err != nil {
		return headlineEdit{}, "", err
	}
	title := *e.title
	e.title = nil
	return e, title, nil
}

func (t target) capture(ctx context.Context, title string, e headlineEdit, now time.Time) (*url.URL, error) {
	file := t.withPath(t.ref.path)
	id, want, err := t.captureLocked(ctx, file, title, e, now)
	if err != nil {
		return nil, err
	}
	if err := file.verifyEdit(ctx, want); err != nil {
		return nil, err
	}
	if readBackDelay > 0 {
		select {
		case <-ctx.Done():
			return headlineURI(t.acct.Name, t.ref.path, id, nil, 1), nil
		case <-time.After(readBackDelay):
		}
		if err := file.verifyEdit(ctx, want); err != nil {
			return nil, err
		}
	}
	return headlineURI(t.acct.Name, t.ref.path, id, nil, 1), nil
}

func (t target) captureLocked(
	ctx context.Context, file target, title string, e headlineEdit, now time.Time,
) (id string, want editResult, err error) {
	token, err := file.lockWithRetry(ctx)
	if err != nil {
		return "", want, err
	}
	defer t.client.unlock(context.WithoutCancel(ctx), t.ref.path, token)

	src, _, err := t.client.get(ctx, t.ref.path)
	if err != nil {
		return "", want, err
	}
	at, level, _, err := insertionPoint(t.acct, t.ref.path, src, t,
		&headlineMove{key: fieldPatchParent, ref: t.ref.path})
	if err != nil {
		return "", want, err
	}

	kws := parseFile(t.acct, t.ref.path, "", src).file.Keywords
	tags := []string{}
	if e.ownTags != nil {
		tags = append(tags, *e.ownTags...)
	}
	if !slices.Contains(tags, agentCapturedTag) {
		tags = append(tags, agentCapturedTag)
	}
	e.ownTags = nil
	line := renderHeadlineLine("", level, "", "", title, tags)
	if err := checkHeadlineLine(line, kws, "", "", title, tags); err != nil {
		return "", want, err
	}

	nl := lineEnding(src)
	id = newUUID()
	block := line + nl +
		":PROPERTIES:" + nl +
		":ID:       " + id + nl +
		":CREATED:  [" + now.Format("2006-01-02 Mon 15:04") + "]" + nl +
		":END:" + nl
	if at == len(src) && len(src) > 0 && src[len(src)-1] != '\n' {
		block = nl + block
	}
	out := append(append(append([]byte(nil), src[:at]...), block...), src[at:]...)

	// The other keys go through the edit splice, on the new headline.
	pf := parseFile(t.acct, t.ref.path, "", out)
	n, ok := pf.find(nodeRef{account: t.acct.Name, kind: kindHeadline, path: t.ref.path, id: id, nth: 1})
	if !ok {
		return "", want, errors.ErrorWithStackf("org plugin: the new headline %s did not parse back", id)
	}
	if !e.empty() {
		if out, _, _, err = applyEdit(out, pf, n, e, now); err != nil {
			return "", want, err
		}
	}

	h := findHeadlineByID(orgtext.Parse(out, orgtext.Options{Keywords: kws}).Headlines, id)
	if h == nil || h.Level != level || h.Title != title {
		return "", want, errors.ErrorWithStackf("org plugin: the new headline %s did not read back", id)
	}
	want = editResult{
		id: id, keyword: h.Keyword, pri: h.Priority, title: h.Title, ownTags: h.OwnTags,
		sched: h.Planning.Scheduled, dead: h.Planning.Deadline, body: &h.Body,
	}

	defer resetCacheEntry(ctx, t.acct.Name, t.ref.path)
	if err := t.putChecked(ctx, t.ref.path, out, token); err != nil {
		return "", want, err
	}
	return id, want, nil
}
