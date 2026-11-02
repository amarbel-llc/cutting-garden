package org

import (
	"bytes"
	"context"
	stderrors "errors"
	"slices"
	"strings"

	"code.linenisgreat.com/cutting-garden/plugins/org/orgtext"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Preamble edits (cutting-garden#357): the file node's `body` patch key
// replaces the text before the first headline — what read_node returns as
// the file's `body` — verbatim. The file's own settings (#+TODO, #+SEQ_TODO,
// #+TYP_TODO, #+FILETAGS) are not part of that text: they are kept and
// written back above it, so a patch can never drop or change them, and a body
// that holds such a line is refused. Everything below the first headline is
// untouched. The write goes through the headline edit's path: LOCK → fresh GET
// → splice → PUT with the token → UNLOCK, then a read-back now and after
// readBackDelay that reports lost_to_concurrent_sync.

// patchPreamble applies a file-node patch. Only `body` is meaningful on a
// file; any other edit is refused rather than silently ignored.
func (t target) patchPreamble(ctx context.Context, e headlineEdit) ([]string, error) {
	other := e
	other.body = nil
	if !other.empty() || e.body == nil {
		return nil, errors.BadRequestf(
			"org plugin: a file accepts only body (its text before the first headline), not %q", t.ref.path)
	}
	want := strings.ReplaceAll(*e.body, "\r\n", "\n")
	applied, err := t.preambleLocked(ctx, want)
	if err != nil || len(applied) == 0 {
		return applied, err
	}
	err = readBackTwice(ctx, func() error { return t.verifyPreamble(ctx, want) })
	if err != nil {
		return nil, err
	}
	return applied, nil
}

func (t target) preambleLocked(ctx context.Context, want string) ([]string, error) {
	token, err := t.lockWithRetry(ctx)
	if err != nil {
		return nil, err
	}
	defer t.client.unlock(context.WithoutCancel(ctx), t.ref.path, token)

	// Fresh read under the lock; the cache may be stale.
	src, _, err := t.client.get(ctx, t.ref.path)
	if err != nil {
		return nil, err
	}
	updated, changed, err := splicePreamble(src, t.acct.keywords, want)
	if err != nil || !changed {
		return nil, err
	}
	defer resetCacheEntry(ctx, t.acct.Name, t.ref.path)
	err = t.client.put(ctx, t.ref.path, updated, token)
	var unknown outcomeUnknown
	if stderrors.As(err, &unknown) {
		// Still holding the lock, so a re-read settles it.
		if cur, _, getErr := t.client.get(ctx, t.ref.path); getErr == nil && bytes.Equal(cur, updated) {
			return []string{fieldPatchBody}, nil
		}
	}
	if err != nil {
		return nil, err
	}
	return []string{fieldPatchBody}, nil
}

// splicePreamble replaces src's preamble text with body, keeping the file's
// settings and everything from the first headline on byte for byte. changed
// is false when body already is the preamble text. The result must read back
// with the same headlines, settings and the body as sent.
func splicePreamble(src []byte, defaults orgtext.Keywords, body string) (out []byte, changed bool, err error) {
	before := orgtext.Parse(src, orgtext.Options{Keywords: defaults})
	cur := before.Preamble()
	if cur.Body == body {
		return src, false, nil
	}
	next := orgtext.Preamble{Settings: cur.Settings, Body: body}
	end := before.PreambleSpan.End
	out = append([]byte(next.Render(orgtext.LineEnding(src))), src[end:]...)

	after := orgtext.Parse(out, orgtext.Options{Keywords: defaults})
	if countHeadlines(after.Headlines) != countHeadlines(before.Headlines) {
		return nil, false, errors.BadRequestf(
			"org plugin: the body would create or swallow a headline: a line beginning with `*` and a " +
				"space starts one in org, and an unclosed #+BEGIN_ block hides the headlines below it")
	}
	if !slices.Equal(after.FileTags, before.FileTags) ||
		!slices.Equal(after.Keywords.Active, before.Keywords.Active) ||
		!slices.Equal(after.Keywords.Done, before.Keywords.Done) ||
		!slices.Equal(after.Preamble().Settings, cur.Settings) {
		return nil, false, errors.BadRequestf(
			"org plugin: the body would change the file's settings (#+TODO, #+SEQ_TODO, #+TYP_TODO or " +
				"#+FILETAGS); a body cannot hold those lines, and they are kept as they are")
	}
	if got := after.Preamble().Body; got != body {
		return nil, false, errors.BadRequestf(
			"org plugin: the body would not read back as written (%q)", got)
	}
	return out, true, nil
}

// verifyPreamble re-reads the file and checks it still holds the new
// preamble text. Orgzly uploads without locking, so a sync that read the
// file before our write silently replaces it.
func (t target) verifyPreamble(ctx context.Context, want string) error {
	src, _, err := t.client.get(ctx, t.ref.path)
	if err != nil {
		return errors.Wrapf(err, "org plugin: the edit to %s was written, but reading it back failed", t.ref.path)
	}
	f := orgtext.Parse(src, orgtext.Options{Keywords: t.acct.keywords})
	if f.Preamble().Body == want {
		return nil
	}
	return errors.ErrorWithStackf(
		"org plugin: lost_to_concurrent_sync: the edit to the preamble of %s was written, then "+
			"overwritten by another writer — most likely an Orgzly sync that read the file first; "+
			"read the file again and re-apply", t.ref.path)
}
