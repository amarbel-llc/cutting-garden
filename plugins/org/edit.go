package org

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"code.linenisgreat.com/cutting-garden/plugins/org/orgtext"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Headline edits (FDR 0026 slice 3a, D7): a patch on a headline sets its
// title, state, priority, own tags, scheduled and deadline, and its own
// text (body.go). Every edit goes through the quote's write path (LOCK → fresh GET →
// splice → PUT with the token → UNLOCK, #267), mints an :ID: when the
// headline has none so its reference survives a retitle, and adds or
// removes CLOSED when the state enters or leaves a done keyword. Orgzly
// uploads without locking, so after unlocking the plugin reads the file
// back, now and again after readBackDelay, and reports a lost update
// instead of claiming success (D8-C).

// Patch keys a headline accepts.
const (
	fieldPatchTitle     = "title"
	fieldPatchState     = "state"
	fieldPatchPriority  = "priority"
	fieldPatchOwnTags   = "own_tags"
	fieldPatchScheduled = "scheduled"
	fieldPatchDeadline  = "deadline"
)

// readBackDelay is how long after a write the plugin checks a second time
// that the edit is still there. A variable so tests can zero it.
var readBackDelay = 2 * time.Second

// headlineEdit is one validated patch. A nil pointer leaves that part
// alone; a pointer to "" clears it.
type headlineEdit struct {
	title     *string
	state     *string
	priority  *string
	ownTags   *[]string
	scheduled *string // "" or "YYYY-MM-DD" with an optional " HH:MM"
	deadline  *string
	body      *string       // the own text, as read_node returns it (body.go)
	move      *headlineMove // move.go
	quote     bool
}

func (e headlineEdit) empty() bool {
	return e.title == nil && e.state == nil && e.priority == nil && e.ownTags == nil &&
		e.scheduled == nil && e.deadline == nil && e.body == nil && e.move == nil && !e.quote
}

var (
	priorityValueRe = regexp.MustCompile(`^(?:[A-Z]|[0-9]{1,2})$`)
	tagValueRe      = regexp.MustCompile(`^[^\s:]+$`)
	dateValueRe     = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})(?:[ T](\d{2}:\d{2}))?$`)
)

// parseEdit decodes and shape-checks a patch body. Unknown keys are
// tolerated and not applied, as before.
func parseEdit(body io.Reader) (headlineEdit, error) {
	var patch map[string]json.RawMessage
	if err := json.NewDecoder(body).Decode(&patch); err != nil {
		return headlineEdit{}, errors.BadRequestf("org plugin: patch body is not a JSON object: %s", err)
	}
	if len(patch) == 0 {
		return headlineEdit{}, errors.BadRequestf("org plugin: empty patch body")
	}
	var e headlineEdit
	if raw, ok := patch[fieldQuote]; ok {
		var quote bool
		if err := json.Unmarshal(raw, &quote); err != nil || !quote {
			return e, errors.BadRequestf("org plugin: quote must be true (unquoting is not supported), got %s", raw)
		}
		e.quote = true
	}
	if raw, ok := patch[fieldPatchTitle]; ok {
		var title string
		if err := json.Unmarshal(raw, &title); err != nil || strings.TrimSpace(title) == "" ||
			strings.ContainsAny(title, "\r\n") {
			return e, errors.BadRequestf("org plugin: title must be a non-empty single-line string, got %s", raw)
		}
		title = strings.TrimSpace(title)
		e.title = &title
	}
	var err error
	if e.state, err = clearableString(patch, fieldPatchState, nil); err != nil {
		return e, err
	}
	if e.priority, err = clearableString(patch, fieldPatchPriority, priorityValueRe); err != nil {
		return e, err
	}
	if e.scheduled, err = clearableString(patch, fieldPatchScheduled, dateValueRe); err != nil {
		return e, err
	}
	if e.deadline, err = clearableString(patch, fieldPatchDeadline, dateValueRe); err != nil {
		return e, err
	}
	if raw, ok := patch[fieldPatchBody]; ok {
		body := ""
		if string(raw) != "null" {
			if err := json.Unmarshal(raw, &body); err != nil {
				return e, errors.BadRequestf("org plugin: body must be a string or null, got %s", raw)
			}
		}
		e.body = &body
	}
	if e.move, err = parseMove(patch); err != nil {
		return e, err
	}
	if raw, ok := patch[fieldPatchOwnTags]; ok {
		var tags []string
		if err := json.Unmarshal(raw, &tags); err != nil {
			return e, errors.BadRequestf("org plugin: own_tags must be an array of tags, got %s", raw)
		}
		for _, tag := range tags {
			if !tagValueRe.MatchString(tag) {
				return e, errors.BadRequestf("org plugin: %q is not a tag (no spaces or colons)", tag)
			}
		}
		tags = uniqueInOrder(tags)
		e.ownTags = &tags
	}
	for _, d := range []*string{e.scheduled, e.deadline} {
		if d != nil && *d != "" {
			if _, err := time.Parse("2006-01-02", dateValueRe.FindStringSubmatch(*d)[1]); err != nil {
				return e, errors.BadRequestf("org plugin: %q is not a calendar date", *d)
			}
		}
	}
	return e, nil
}

// clearableString reads key as a string, where null or "none" clears it
// (""); shape, when set, constrains any other value.
func clearableString(patch map[string]json.RawMessage, key string, shape *regexp.Regexp) (*string, error) {
	raw, ok := patch[key]
	if !ok {
		return nil, nil
	}
	cleared := ""
	if string(raw) == "null" {
		return &cleared, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, errors.BadRequestf("org plugin: %s must be a string or null, got %s", key, raw)
	}
	s = strings.TrimSpace(s)
	if s == "" || s == valueNone {
		return &cleared, nil
	}
	if shape != nil && !shape.MatchString(s) {
		return nil, errors.BadRequestf("org plugin: %s %q is malformed", key, s)
	}
	return &s, nil
}

func uniqueInOrder(xs []string) []string {
	out := []string{}
	for _, x := range xs {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// PatchNode applies an edit to a headline.
func (Plugin) PatchNode(ctx context.Context, uri *url.URL, body io.Reader) ([]string, error) {
	e, err := parseEdit(body)
	if err != nil {
		return nil, err
	}
	if e.empty() {
		return []string{}, nil
	}
	t, err := resolve(uri)
	if err != nil {
		return nil, err
	}
	if t.ref.kind != kindHeadline {
		return nil, errors.BadRequestf("org plugin: only a headline can be edited, not %q", uri.String())
	}
	if e.move != nil {
		return t.editAndMove(ctx, e, t.acct.now())
	}
	return t.edit(ctx, e, t.acct.now())
}

// editResult is what the read-back checks for.
type editResult struct {
	id      string
	keyword string
	pri     string
	title   string
	ownTags []string
	sched   string // day, "" when none
	dead    string
	body    *string // checked only when the edit set it
}

func (t target) edit(ctx context.Context, e headlineEdit, now time.Time) ([]string, error) {
	applied, want, err := t.editLocked(ctx, e, now)
	if err != nil || len(applied) == 0 {
		return applied, err
	}
	if err := t.verifyEdit(ctx, want); err != nil {
		return nil, err
	}
	if readBackDelay > 0 {
		select {
		case <-ctx.Done():
			return applied, nil // written and checked once; the caller gave up waiting
		case <-time.After(readBackDelay):
		}
		if err := t.verifyEdit(ctx, want); err != nil {
			return nil, err
		}
	}
	return applied, nil
}

func (t target) editLocked(
	ctx context.Context, e headlineEdit, now time.Time,
) (applied []string, want editResult, err error) {
	token, err := t.lockWithRetry(ctx)
	if err != nil {
		return nil, want, err
	}
	defer t.client.unlock(context.WithoutCancel(ctx), t.ref.path, token)

	// Fresh read under the lock; the cache may be stale.
	body, etag, err := t.client.get(ctx, t.ref.path)
	if err != nil {
		return nil, want, err
	}
	pf := parseFile(t.acct, t.ref.path, etag, body)
	n, ok := pf.find(t.ref)
	if !ok {
		return nil, want, errors.BadRequestf(
			"org plugin: no such headline in %s (renamed or moved?)", t.ref.path)
	}
	if n.h.Synthetic {
		return nil, want, errors.BadRequestf(
			"org plugin: %s has no headlines; there is nothing to edit", t.ref.path)
	}
	updated, applied, want, err := applyEdit(body, pf, n, e, now)
	if err != nil || len(applied) == 0 {
		return applied, want, err
	}
	defer resetCacheEntry(t.acct.Name, t.ref.path)
	err = t.client.put(ctx, t.ref.path, updated, token)
	var unknown outcomeUnknown
	if stderrors.As(err, &unknown) {
		// Still holding the lock, so a re-read settles it.
		if cur, _, getErr := t.client.get(ctx, t.ref.path); getErr == nil && bytes.Equal(cur, updated) {
			return applied, want, nil
		}
	}
	if err != nil {
		return nil, want, err
	}
	return applied, want, nil
}

// verifyEdit re-reads the file and checks the edited headline still holds
// the edit. Orgzly uploads without locking, so a sync that read the file
// before our write silently replaces it.
func (t target) verifyEdit(ctx context.Context, want editResult) error {
	body, etag, err := t.client.get(ctx, t.ref.path)
	if err != nil {
		return errors.Wrapf(err, "org plugin: the edit to %s was written, but reading it back failed", t.ref.path)
	}
	pf := parseFile(t.acct, t.ref.path, etag, body)
	n, ok := pf.find(nodeRef{account: t.ref.account, kind: kindHeadline, path: t.ref.path, id: want.id})
	if ok && n.h.Keyword == want.keyword && n.h.Priority == want.pri && n.h.Title == want.title &&
		slices.Equal(n.h.OwnTags, want.ownTags) &&
		n.h.Planning.Scheduled == want.sched && n.h.Planning.Deadline == want.dead &&
		(want.body == nil || n.h.Body == *want.body) {
		return nil
	}
	return errors.ErrorWithStackf(
		"org plugin: lost_to_concurrent_sync: the edit to %s (:ID: %s) was written, then overwritten by "+
			"another writer — most likely an Orgzly sync that read the file first; read the headline "+
			"again and re-apply", t.ref.path, want.id)
}

// applyEdit splices e into src for headline n. It touches only the
// headline line, its planning line and its property drawer; applied lists
// the patch keys that changed.
func applyEdit(
	src []byte, pf *parsedFile, n *hnode, e headlineEdit, now time.Time,
) (out []byte, applied []string, want editResult, err error) {
	h := n.h
	kws := pf.file.Keywords

	keyword, pri, title := h.Keyword, h.Priority, h.Title
	ownTags := slices.Clone(h.OwnTags)
	if e.title != nil && *e.title != title {
		title = *e.title
		applied = append(applied, fieldPatchTitle)
	}
	if e.state != nil && *e.state != keyword {
		if *e.state != "" && !kws.Contains(*e.state) {
			return nil, nil, want, errors.BadRequestf(
				"org plugin: state %q is not a TODO keyword of %s (have: %s)",
				*e.state, pf.rel, strings.Join(append(slices.Clone(kws.Active), kws.Done...), ", "))
		}
		keyword = *e.state
		applied = append(applied, fieldPatchState)
	}
	if e.priority != nil && *e.priority != pri {
		pri = *e.priority
		applied = append(applied, fieldPatchPriority)
	}
	if e.ownTags != nil && !slices.Equal(*e.ownTags, ownTags) {
		ownTags = slices.Clone(*e.ownTags)
		applied = append(applied, fieldPatchOwnTags)
	}
	if e.quote && !slices.Contains(ownTags, agentQuotedTag) {
		ownTags = append(ownTags, agentQuotedTag)
	}
	tagsChanged := !slices.Equal(ownTags, h.OwnTags)

	planning := parsePlanningLine(string(src[h.PlanningSpan.Start:h.PlanningSpan.End]))
	planningBefore := planning.render()
	// A rendered stamp equal to the current one is no change; the time a
	// date-only value keeps makes that common for an unchanged day.
	if e.scheduled != nil && planning.set("SCHEDULED", activeStamp(*e.scheduled, planning.get("SCHEDULED"))) {
		applied = append(applied, fieldPatchScheduled)
	}
	if e.deadline != nil && planning.set("DEADLINE", activeStamp(*e.deadline, planning.get("DEADLINE"))) {
		applied = append(applied, fieldPatchDeadline)
	}
	if wasDone, isDone := kws.IsDone(h.Keyword), kws.IsDone(keyword); wasDone != isDone {
		closed := ""
		if isDone {
			closed = "[" + now.Format("2006-01-02 Mon 15:04") + "]"
		}
		planning.set("CLOSED", closed)
	}

	var newBody *string
	if e.body != nil {
		if b := normalizeBody(*e.body); b != h.Body {
			newBody = &b
			applied = append(applied, fieldPatchBody)
		}
	}

	needID := h.ID() == "" || n.idShadowed()
	quoted := e.quote && (needID || !slices.Contains(h.OwnTags, agentQuotedTag))
	if quoted {
		applied = append(applied, fieldQuote)
	}
	// A move finds the headline again by its :ID:, so it mints one even
	// when nothing else changes.
	if len(applied) == 0 && !(e.move != nil && needID) {
		return src, applied, editResult{id: h.ID()}, nil
	}

	// Check the new headline line parses back to exactly what was asked
	// for: a title starting with a keyword or ending in :tags: would not.
	line := string(src[h.LineSpan.Start:h.LineSpan.End])
	if keyword != h.Keyword || pri != h.Priority || title != h.Title {
		line = renderHeadlineLine(line, h.Level, keyword, pri, title, ownTags)
	} else if tagsChanged {
		line = replaceTagBlock(line, ownTags)
	}
	if err := checkHeadlineLine(line, kws, keyword, pri, title, ownTags); err != nil {
		return nil, nil, want, err
	}

	nl := "\n"
	if h.LineSpan.End < len(src) && src[h.LineSpan.End] == '\r' {
		nl = "\r\n"
	}
	planningChanged := planning.render() != planningBefore
	out = append([]byte(nil), src...)
	planningAt, drawerAt := h.PlanningSpan, h.DrawerSpan
	if (planningChanged || needID) && drawerAt.Start == len(src) &&
		len(src) > 0 && src[len(src)-1] != '\n' {
		// This headline's last line ends the file without a newline: add
		// one, so a line inserted after it starts on its own line, and let
		// a planning line ending there own the added newline.
		out = append(out, nl...)
		if planningAt.End == len(src) {
			planningAt.End = len(out)
			if planningAt.Start == len(src) {
				planningAt.Start = len(out)
			}
		}
		drawerAt = orgtext.Span{Start: len(out), End: len(out)}
	}

	id := h.ID()
	if needID {
		id = newUUID()
		idLine := ":ID:       " + id
		if drawerAt.Empty() {
			out = insertAt(out, drawerAt.Start, []byte(":PROPERTIES:"+nl+idLine+nl+":END:"+nl))
		} else {
			out = spliceDrawerID(out, drawerAt, idLine, nl)
		}
	}
	if planningChanged {
		newPlanning := ""
		if after := planning.render(); after != "" {
			newPlanning = after + nl
		}
		out = append(out[:planningAt.Start:planningAt.Start],
			append([]byte(newPlanning), out[planningAt.End:]...)...)
	}
	out = append(out[:h.LineSpan.Start:h.LineSpan.Start],
		append([]byte(line), out[h.LineSpan.End:]...)...)

	// The body goes last, found again by the :ID: the splices above settled.
	if newBody != nil {
		if out, err = spliceBody(out, kws, id, *newBody, nl); err != nil {
			return nil, nil, want, err
		}
	}

	want = editResult{
		id: id, keyword: keyword, pri: pri, title: title, ownTags: ownTags,
		sched: planning.day("SCHEDULED"), dead: planning.day("DEADLINE"), body: newBody,
	}
	return out, applied, want, nil
}

// renderHeadlineLine rebuilds a headline line, keeping the gap before an
// existing tag block (tags are often aligned to a column).
func renderHeadlineLine(old string, level int, keyword, pri, title string, tags []string) string {
	var b strings.Builder
	b.WriteString(strings.Repeat("*", level))
	b.WriteString(" ")
	if keyword != "" {
		b.WriteString(keyword + " ")
	}
	if pri != "" {
		b.WriteString("[#" + pri + "] ")
	}
	b.WriteString(title)
	if len(tags) > 0 {
		gap := " "
		if m := tagSuffixRe.FindStringSubmatch(old); m != nil {
			gap = m[1]
		}
		b.WriteString(gap + ":" + strings.Join(tags, ":") + ":")
	}
	return b.String()
}

// replaceTagBlock swaps only the line's trailing tag block, leaving the
// rest of the line byte for byte.
func replaceTagBlock(line string, tags []string) string {
	block := ""
	if len(tags) > 0 {
		block = ":" + strings.Join(tags, ":") + ":"
	}
	if m := tagSuffixRe.FindStringSubmatchIndex(line); m != nil {
		if block == "" {
			return line[:m[0]]
		}
		return line[:m[4]] + block
	}
	if block == "" {
		return line
	}
	return line + " " + block
}

func checkHeadlineLine(line string, kws orgtext.Keywords, keyword, pri, title string, tags []string) error {
	f := orgtext.Parse([]byte(line+"\n"), orgtext.Options{Keywords: kws})
	if len(f.Headlines) == 1 {
		h := f.Headlines[0]
		if h.Keyword == keyword && h.Priority == pri && h.Title == title && slices.Equal(h.OwnTags, tags) {
			return nil
		}
	}
	return errors.BadRequestf(
		"org plugin: the edited headline would not read back as written (%q); a title cannot begin "+
			"with a TODO keyword or a priority cookie, or end in :tags:", line)
}

// planningLine is a headline's planning line as an ordered list of
// KEYWORD: timestamp entries, so an edit changes one entry and keeps the
// others (and their repeaters) byte for byte.
type planningLine struct {
	indent  string
	entries []planningEntry
}

type planningEntry struct{ key, stamp string }

var planningEntryRe = regexp.MustCompile(`(SCHEDULED|DEADLINE|CLOSED):[ \t]*([<\[][^>\]]*[>\]])`)

func parsePlanningLine(text string) planningLine {
	text = strings.TrimRight(text, "\r\n")
	p := planningLine{indent: text[:len(text)-len(strings.TrimLeft(text, " \t"))]}
	for _, m := range planningEntryRe.FindAllStringSubmatch(text, -1) {
		p.entries = append(p.entries, planningEntry{key: m[1], stamp: m[2]})
	}
	return p
}

func (p planningLine) get(key string) string {
	for _, e := range p.entries {
		if e.key == key {
			return e.stamp
		}
	}
	return ""
}

// set replaces key's timestamp, adds it (CLOSED first, others last, as org
// does), or removes it when stamp is "". It reports whether anything changed.
func (p *planningLine) set(key, stamp string) bool {
	for i, e := range p.entries {
		if e.key != key {
			continue
		}
		if stamp == e.stamp {
			return false
		}
		if stamp == "" {
			p.entries = slices.Delete(p.entries, i, i+1)
		} else {
			p.entries[i].stamp = stamp
		}
		return true
	}
	if stamp == "" {
		return false
	}
	if key == "CLOSED" {
		p.entries = slices.Insert(p.entries, 0, planningEntry{key, stamp})
	} else {
		p.entries = append(p.entries, planningEntry{key, stamp})
	}
	return true
}

var stampDayRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

func (p planningLine) day(key string) string { return stampDayRe.FindString(p.get(key)) }

func (p planningLine) render() string {
	if len(p.entries) == 0 {
		return ""
	}
	parts := make([]string, len(p.entries))
	for i, e := range p.entries {
		parts[i] = e.key + ": " + e.stamp
	}
	return p.indent + strings.Join(parts, " ")
}

// stampCookieRe finds a timestamp's repeater (+1w, .+1d, ++2m) and warning
// (-3d, --1w) cookies.
var stampCookieRe = regexp.MustCompile(`(?:[.+]?\+|--?)\d+[hdwmy]`)

// stampTimeRe finds a timestamp's time of day, or time range.
var stampTimeRe = regexp.MustCompile(`\b\d{1,2}:\d{2}(?:-\d{1,2}:\d{2})?\b`)

// activeStamp renders value ("YYYY-MM-DD" with an optional " HH:MM") as an
// active org timestamp, carrying over old's repeater and warning cookies,
// and old's time of day when value gives none (so rescheduling to another
// day keeps the time); "" stays "" (clear).
func activeStamp(value, old string) string {
	if value == "" {
		return ""
	}
	m := dateValueRe.FindStringSubmatch(value)
	d, _ := time.Parse("2006-01-02", m[1])
	parts := []string{m[1], d.Format("Mon")}
	if m[2] != "" {
		parts = append(parts, m[2])
	} else if t := stampTimeRe.FindString(old); t != "" {
		parts = append(parts, t)
	}
	parts = append(parts, stampCookieRe.FindAllString(old, -1)...)
	return "<" + strings.Join(parts, " ") + ">"
}
