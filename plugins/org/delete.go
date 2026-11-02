package org

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"strings"

	"code.linenisgreat.com/cutting-garden/plugins/org/orgtext"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Deleting a headline (FDR 0026 D7-G, cutting-garden#361). A delete with no
// body removes a headline that has no children; one with children is
// refused, naming how many descendants it would take. The delete op's body
// {"subtree": true} removes the headline with its whole subtree. The write
// is the edit path's: LOCK → fresh GET → splice → PUT with the token →
// UNLOCK, then a read-back now and after readBackDelay that reports a
// resurrected headline as lost_to_concurrent_sync.

const fieldDeleteSubtree = "subtree"

// DeleteNode is a delete with no options: a headline without children.
func (p Plugin) DeleteNode(ctx context.Context, node *url.URL) error {
	return p.DeleteNodeWith(ctx, node, nil)
}

// DeleteNodeWith is a delete whose op body carries options: {"subtree": bool}.
func (Plugin) DeleteNodeWith(ctx context.Context, node *url.URL, body io.Reader) error {
	subtree, err := parseDeleteOptions(body)
	if err != nil {
		return err
	}
	t, err := resolve(node)
	if err != nil {
		return err
	}
	if t.ref.kind != kindHeadline {
		return errors.BadRequestf(
			"org plugin: only a headline can be deleted, not %q (a file or folder is not deletable here)",
			node.String())
	}
	want, err := t.deleteLocked(ctx, subtree)
	if err != nil {
		return err
	}
	return readBackTwice(ctx, func() error { return t.verifyDeleted(ctx, want) })
}

func parseDeleteOptions(body io.Reader) (subtree bool, err error) {
	if body == nil {
		return false, nil
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		return false, errors.Wrap(err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return false, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var opts struct {
		Subtree *bool `json:"subtree"`
	}
	if err := dec.Decode(&opts); err != nil {
		return false, errors.BadRequestf(
			`org plugin: a delete body is JSON {"subtree": true} or nothing: %s`, err)
	}
	return opts.Subtree != nil && *opts.Subtree, nil
}

// deletedHeadline is what the read-back checks for after a delete.
type deletedHeadline struct {
	id      string   // :ID: of the deleted headline, "" when it had none
	outline []string // its outline path
	sameKey int      // headlines left with that id (or outline path)
}

func (t target) deleteLocked(ctx context.Context, subtree bool) (want deletedHeadline, err error) {
	token, err := t.lockWithRetry(ctx)
	if err != nil {
		return want, err
	}
	defer t.client.unlock(context.WithoutCancel(ctx), t.ref.path, token)

	// Fresh read under the lock; the cache may be stale.
	src, etag, err := t.client.get(ctx, t.ref.path)
	if err != nil {
		return want, err
	}
	pf := parseFile(t.acct, t.ref.path, etag, src)
	n, ok := pf.find(t.ref)
	if !ok {
		return want, errors.BadRequestf(
			"org plugin: no headline %s in %s (renamed or moved? try a title~= search)", t.refText(), t.ref.path)
	}
	if n.h.Synthetic {
		return want, errors.BadRequestf("org plugin: %s has no headlines; there is nothing to delete", t.ref.path)
	}
	if desc := countHeadlines(n.h.Children); desc > 0 && !subtree {
		noun := "headlines"
		if desc == 1 {
			noun = "headline"
		}
		return want, errors.BadRequestf(
			`org plugin: %q has %d descendant %s and deleting it would take them too; delete them first, `+
				`or send the delete op's body {"subtree": true} to delete the whole subtree`,
			n.h.Title, desc, noun)
	}

	out := append(append([]byte(nil), src[:n.h.SubtreeSpan.Start]...), src[n.h.SubtreeSpan.End:]...)
	removed := 1 + countHeadlines(n.h.Children)
	after := orgtext.Parse(out, orgtext.Options{Keywords: pf.file.Keywords})
	if got, wantCount := countHeadlines(after.Headlines), countHeadlines(pf.file.Headlines)-removed; got != wantCount {
		return want, errors.BadRequestf(
			"org plugin: deleting %q would change %d other headline(s) in %s; nothing was written",
			n.h.Title, wantCount-got, t.ref.path)
	}

	want = deletedHeadline{id: n.h.ID(), outline: n.h.OutlinePath}
	want.sameKey = sameKeyCount(after.Headlines, want)

	defer resetCacheEntry(ctx, t.acct.Name, t.ref.path)
	if err := t.putChecked(ctx, t.ref.path, out, token); err != nil {
		return want, err
	}
	return want, nil
}

// refText names the target in an error: its id, else its outline path.
func (t target) refText() string {
	if t.ref.id != "" {
		return "with id " + t.ref.id
	}
	return "with outline path " + strings.Join(t.ref.outline, " / ")
}

// sameKeyCount counts the headlines that still match the deleted one: the
// same :ID: when it had one, else the same outline path.
func sameKeyCount(hs []*orgtext.Headline, want deletedHeadline) int {
	n := 0
	for _, h := range hs {
		if want.id != "" && h.ID() == want.id ||
			want.id == "" && equalStrings(h.OutlinePath, want.outline) {
			n++
		}
		n += sameKeyCount(h.Children, want)
	}
	return n
}

func equalStrings(a, b []string) bool {
	return strings.Join(a, "\x00") == strings.Join(b, "\x00") && len(a) == len(b)
}

// verifyDeleted re-reads the file and checks the headline is still gone.
// Orgzly uploads without locking, so a sync that read the file before our
// write silently brings it back.
func (t target) verifyDeleted(ctx context.Context, want deletedHeadline) error {
	src, _, err := t.client.get(ctx, t.ref.path)
	if err != nil {
		return errors.Wrapf(err, "org plugin: the delete in %s was written, but reading it back failed", t.ref.path)
	}
	f := orgtext.Parse(src, orgtext.Options{Keywords: t.acct.keywords})
	if sameKeyCount(f.Headlines, want) <= want.sameKey {
		return nil
	}
	return errors.ErrorWithStackf(
		"org plugin: lost_to_concurrent_sync: the delete in %s was written, then overwritten by another "+
			"writer — most likely an Orgzly sync that read the file first; the headline is back: read the "+
			"file again and re-apply", t.ref.path)
}
