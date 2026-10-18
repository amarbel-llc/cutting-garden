package org

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"code.linenisgreat.com/cutting-garden/plugins/org/orgtext"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Moves (FDR 0026 D7): one of the patch keys `parent`, `before` or `after`
// moves a headline with its whole subtree.
//
//   - parent: a headline makes it that headline's last child; a file makes
//     it a level-1 headline at the file's end.
//   - before / after: next to a sibling headline, at that sibling's level.
//
// The reference is a full org:// URI, a path from the account root
// (`notes.org`, `sub/notes.org?id=…`), or a bare query (`id=…`, `?h=…`)
// naming a headline in the moved headline's own file. A move may cross
// files: both files are locked, the destination is written first and the
// source second, so a failure in between leaves the headline in both files
// (one :ID:, reported as such), never in neither. The headline keeps its
// :ID: (one is minted if it has none); its levels shift to fit, its own
// tags travel with it, and its inherited tags become the new place's.
// Metadata and body keys in the same patch apply first. A cross-file move
// changes the headline's URI (the file part), so read it again afterwards.

const (
	fieldPatchParent = "parent"
	fieldPatchBefore = "before"
	fieldPatchAfter  = "after"
)

type headlineMove struct {
	key string // fieldPatchParent, fieldPatchBefore or fieldPatchAfter
	ref string
}

func parseMove(patch map[string]json.RawMessage) (*headlineMove, error) {
	var move *headlineMove
	for _, key := range []string{fieldPatchParent, fieldPatchBefore, fieldPatchAfter} {
		raw, ok := patch[key]
		if !ok {
			continue
		}
		if move != nil {
			return nil, errors.BadRequestf("org plugin: give one of parent, before or after, not %s and %s",
				move.key, key)
		}
		var ref string
		if err := json.Unmarshal(raw, &ref); err != nil || strings.TrimSpace(ref) == "" {
			return nil, errors.BadRequestf("org plugin: %s must be a headline or file reference, got %s", key, raw)
		}
		move = &headlineMove{key: key, ref: strings.TrimSpace(ref)}
	}
	return move, nil
}

// resolveMoveRef turns a move reference into a target in t's account.
func (t target) resolveMoveRef(m *headlineMove) (target, error) {
	u, err := url.Parse(m.ref)
	switch {
	case err == nil && u.Scheme == schemeOrg:
	default:
		file, query, hasQuery := strings.Cut(m.ref, "?")
		switch {
		case hasQuery && file != "":
		case strings.HasSuffix(m.ref, ".org"):
			file, query = m.ref, ""
		default:
			// A bare query: a headline in the moved headline's own file.
			file, query = t.ref.path, strings.TrimPrefix(m.ref, "?")
		}
		u = fileURI(t.acct.Name, strings.TrimPrefix(file, "/"))
		u.RawQuery = query
	}
	dest, err := resolve(u)
	if err != nil {
		return target{}, err
	}
	if dest.acct.Name != t.acct.Name {
		return target{}, errors.BadRequestf("org plugin: a headline moves only within its account (%s), not to %s",
			t.acct.Name, u)
	}
	switch {
	case dest.ref.kind == kindHeadline:
	case dest.ref.kind == kindFile && m.key == fieldPatchParent:
	default:
		return target{}, errors.BadRequestf("org plugin: %s must name a headline%s, not %s", m.key,
			map[bool]string{true: " or a file", false: ""}[m.key == fieldPatchParent], u)
	}
	return dest, nil
}

// withPath is t addressing another file of its account.
func (t target) withPath(path string) target {
	t.ref = nodeRef{account: t.acct.Name, kind: kindFile, path: path, nth: 1}
	return t
}

// movedHeadline is what the read-back checks for after a move.
type movedHeadline struct {
	path       string
	level      int
	parentPath []string // outline path of the new parent; nil at top level
	fields     editResult
	subtree    string // the moved text, quoted back if a cross-file move is lost
}

func (t target) editAndMove(ctx context.Context, e headlineEdit, now time.Time) ([]string, error) {
	dest, err := t.resolveMoveRef(e.move)
	if err != nil {
		return nil, err
	}
	applied, want, err := t.moveLocked(ctx, e, dest, now)
	if err != nil || len(applied) == 0 {
		return applied, err
	}
	if err := t.verifyMove(ctx, want); err != nil {
		return nil, err
	}
	if readBackDelay > 0 {
		select {
		case <-ctx.Done():
			return applied, nil
		case <-time.After(readBackDelay):
		}
		if err := t.verifyMove(ctx, want); err != nil {
			return nil, err
		}
	}
	return applied, nil
}

func (t target) moveLocked(
	ctx context.Context, e headlineEdit, dest target, now time.Time,
) (applied []string, want movedHeadline, err error) {
	srcPath, dstPath := t.ref.path, dest.ref.path
	crossFile := srcPath != dstPath

	// Lock in path order, so two of this plugin's moves cannot deadlock.
	paths := []string{srcPath}
	if crossFile {
		paths = append(paths, dstPath)
		sort.Strings(paths)
	}
	tokens := map[string]string{}
	defer func() {
		for path, token := range tokens {
			t.client.unlock(context.WithoutCancel(ctx), path, token)
		}
	}()
	for _, path := range paths {
		token, err := t.withPath(path).lockWithRetry(ctx)
		if err != nil {
			return nil, want, err
		}
		tokens[path] = token
	}

	srcBytes, etag, err := t.client.get(ctx, srcPath)
	if err != nil {
		return nil, want, err
	}
	pf := parseFile(t.acct, srcPath, etag, srcBytes)
	n, ok := pf.find(t.ref)
	if !ok {
		return nil, want, errors.BadRequestf("org plugin: no such headline in %s (renamed or moved?)", srcPath)
	}
	if n.h.Synthetic {
		return nil, want, errors.BadRequestf("org plugin: %s has no headlines; there is nothing to move", srcPath)
	}

	// Metadata, body and the :ID: first, in the source file.
	edited, applied, editWant, err := applyEdit(srcBytes, pf, n, e, now)
	if err != nil {
		return nil, want, err
	}
	id := editWant.id
	kws := pf.file.Keywords
	srcFile := orgtext.Parse(edited, orgtext.Options{Keywords: kws})
	moved := findHeadlineByID(srcFile.Headlines, id)
	if moved == nil {
		return nil, want, errors.ErrorWithStackf("org plugin: headline %s vanished while moving it", id)
	}
	nl := lineEnding(edited)
	subtree := string(edited[moved.SubtreeSpan.Start:moved.SubtreeSpan.End])
	if !strings.HasSuffix(subtree, "\n") {
		subtree += nl
	}
	srcWithout := append(append([]byte(nil), edited[:moved.SubtreeSpan.Start]...),
		edited[moved.SubtreeSpan.End:]...)

	// The destination, as it will be once the subtree is lifted out of it.
	// Within one file the target is resolved in the text as it is (a ?h=
	// ordinal would shift once the subtree is gone), and the offset is then
	// mapped past the removed span.
	dstBase := srcWithout
	var at, level int
	var parentPath []string
	if crossFile {
		if dstBase, _, err = t.client.get(ctx, dstPath); err != nil {
			return nil, want, err
		}
		if at, level, parentPath, err = insertionPoint(t.acct, dstPath, dstBase, dest, e.move); err != nil {
			return nil, want, err
		}
	} else {
		if dest.ref.kind == kindHeadline {
			destNode, ok := parseFile(t.acct, srcPath, "", edited).find(dest.ref)
			if !ok {
				return nil, want, errors.BadRequestf("org plugin: no such headline %s", e.move.ref)
			}
			if destNode.h.SubtreeSpan.Start >= moved.SubtreeSpan.Start &&
				destNode.h.SubtreeSpan.Start < moved.SubtreeSpan.End {
				return nil, want, errors.BadRequestf(
					"org plugin: %s %s is the moved headline or inside its subtree", e.move.key, e.move.ref)
			}
		}
		if at, level, parentPath, err = insertionPoint(t.acct, srcPath, edited, dest, e.move); err != nil {
			return nil, want, err
		}
		// A target outside the moved subtree never starts or ends strictly
		// inside it, so an offset at or past its end shifts back by its length.
		if at >= moved.SubtreeSpan.End {
			at -= moved.SubtreeSpan.End - moved.SubtreeSpan.Start
		}
	}
	block := relevel(subtree, moved, level)
	if at == len(dstBase) && len(dstBase) > 0 && dstBase[len(dstBase)-1] != '\n' {
		block = lineEnding(dstBase) + block
	}
	dstOut := append(append(append([]byte(nil), dstBase[:at]...), block...), dstBase[at:]...)

	// It must read back as the same headline, at the new place.
	dstParsed := parseFile(t.acct, dstPath, "", dstOut)
	got := findHeadlineByID(dstParsed.file.Headlines, id)
	if got == nil || got.Level != level || got.Keyword != moved.Keyword || got.Title != moved.Title {
		return nil, want, errors.BadRequestf(
			"org plugin: the moved headline would not read back as written in %s; a TODO keyword it "+
				"carries (%q) may not be one of that file's", dstPath, moved.Keyword)
	}
	want = movedHeadline{
		path: dstPath, level: level, parentPath: parentPath,
		fields: editResult{
			id: id, keyword: got.Keyword, pri: got.Priority, title: got.Title, ownTags: got.OwnTags,
			sched: got.Planning.Scheduled, dead: got.Planning.Deadline, body: &got.Body,
		},
		subtree: block,
	}

	if !crossFile && bytes.Equal(dstOut, edited) {
		// Already there: only the edit (if any) is written.
		if len(applied) > 0 {
			if err := t.putChecked(ctx, srcPath, edited, tokens[srcPath]); err != nil {
				return nil, want, err
			}
		}
		return applied, want, nil
	}
	applied = append(applied, e.move.key)

	defer resetCacheEntry(t.acct.Name, srcPath)
	defer resetCacheEntry(t.acct.Name, dstPath)
	if err := t.putChecked(ctx, dstPath, dstOut, tokens[dstPath]); err != nil {
		return nil, want, err
	}
	if crossFile {
		if err := t.putChecked(ctx, srcPath, srcWithout, tokens[srcPath]); err != nil {
			return nil, want, errors.Wrapf(err,
				"org plugin: the headline (:ID: %s) was written to %s, but removing it from %s failed; "+
					"it is now in both files — delete the copy in %s (retrying the move would add a second copy "+
					"to %s)", id, dstPath, srcPath, srcPath, dstPath)
		}
	}
	return applied, want, nil
}

// putChecked writes body under the lock; when the outcome is unknown, the
// lock still held, a re-read settles whether it landed.
func (t target) putChecked(ctx context.Context, path string, body []byte, token string) error {
	err := t.client.put(ctx, path, body, token)
	var unknown outcomeUnknown
	if stderrors.As(err, &unknown) {
		if cur, _, getErr := t.client.get(ctx, path); getErr == nil && bytes.Equal(cur, body) {
			return nil
		}
	}
	return err
}

// insertionPoint finds where the moved subtree goes in dst (the
// destination file's text without the subtree), the level it takes there,
// and its new parent's outline path.
func insertionPoint(
	a account, dstPath string, dst []byte, dest target, m *headlineMove,
) (at, level int, parentPath []string, err error) {
	if dest.ref.kind == kindFile {
		return len(dst), 1, nil, nil
	}
	n, ok := parseFile(a, dstPath, "", dst).find(dest.ref)
	if !ok || n.h.Synthetic {
		return 0, 0, nil, errors.BadRequestf("org plugin: no such headline %s", m.ref)
	}
	h := n.h
	switch m.key {
	case fieldPatchParent:
		return h.SubtreeSpan.End, h.Level + 1, h.OutlinePath, nil
	case fieldPatchBefore:
		return h.SubtreeSpan.Start, h.Level, h.OutlinePath[:len(h.OutlinePath)-1], nil
	default:
		return h.SubtreeSpan.End, h.Level, h.OutlinePath[:len(h.OutlinePath)-1], nil
	}
}

// relevel rewrites the stars of every headline in subtree (h's, with
// subtree starting at h's line) so h sits at level.
func relevel(subtree string, h *orgtext.Headline, level int) string {
	delta := level - h.Level
	if delta == 0 {
		return subtree
	}
	var starts []int // line starts, relative to the subtree
	var walk func(*orgtext.Headline)
	walk = func(x *orgtext.Headline) {
		starts = append(starts, x.LineSpan.Start-h.SubtreeSpan.Start)
		for _, c := range x.Children {
			walk(c)
		}
	}
	walk(h)
	var b strings.Builder
	prev := 0
	for _, s := range starts {
		stars := s + strings.IndexFunc(subtree[s:], func(r rune) bool { return r != '*' })
		b.WriteString(subtree[prev:s])
		b.WriteString(strings.Repeat("*", stars-s+delta))
		prev = stars
	}
	b.WriteString(subtree[prev:])
	return b.String()
}

// lineEnding is the file's line ending: "\r\n" when it has any, else "\n".
func lineEnding(src []byte) string {
	if bytes.Contains(src, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

// verifyMove re-reads the files after unlocking: the headline must be at
// its new place with the edit, and (across files) gone from the old one.
func (t target) verifyMove(ctx context.Context, want movedHeadline) error {
	lost := errors.ErrorWithStackf(
		"org plugin: lost_to_concurrent_sync: the move of :ID: %s to %s was written, then overwritten by "+
			"another writer — most likely an Orgzly sync that read the file first; read the headline "+
			"again and re-apply", want.fields.id, want.path)
	if t.ref.path != want.path {
		// The source no longer holds it either: hand the text back, so a
		// lost destination write cannot lose the headline.
		lost = errors.ErrorWithStackf(
			"org plugin: lost_to_concurrent_sync: the move of :ID: %s from %s to %s was overwritten in %s "+
				"by another writer (most likely an Orgzly sync); if %s no longer holds it, re-create it "+
				"from this text:\n%s", want.fields.id, t.ref.path, want.path, want.path, t.ref.path, want.subtree)
	}
	body, etag, err := t.client.get(ctx, want.path)
	if err != nil {
		return errors.Wrapf(err, "org plugin: the move to %s was written, but reading it back failed", want.path)
	}
	pf := parseFile(t.acct, want.path, etag, body)
	h := findHeadlineByID(pf.file.Headlines, want.fields.id)
	w := want.fields
	if h == nil || h.Level != want.level ||
		!slices.Equal(h.OutlinePath[:len(h.OutlinePath)-1], want.parentPath) ||
		h.Keyword != w.keyword || h.Priority != w.pri || h.Title != w.title ||
		!slices.Equal(h.OwnTags, w.ownTags) || h.Planning.Scheduled != w.sched ||
		h.Planning.Deadline != w.dead || h.Body != *w.body {
		return lost
	}
	if t.ref.path != want.path {
		src, _, err := t.client.get(ctx, t.ref.path)
		if err != nil {
			return errors.Wrapf(err, "org plugin: the move from %s was written, but reading it back failed",
				t.ref.path)
		}
		if findHeadlineByID(orgtext.Parse(src, orgtext.Options{Keywords: t.acct.keywords}).Headlines,
			w.id) != nil {
			return errors.ErrorWithStackf(
				"org plugin: lost_to_concurrent_sync: the headline (:ID: %s) was moved to %s, but another "+
					"writer put it back in %s — it is now in both files; delete one copy", w.id, want.path, t.ref.path)
		}
	}
	return nil
}
