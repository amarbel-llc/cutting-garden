package orgtext

import (
	"bytes"
	"regexp"
	"slices"
	"strings"
)

// Span is a half-open byte range [Start, End) into the parsed source. An
// empty span (Start == End) marks where an absent element would be inserted.
type Span struct {
	Start, End int
}

// Empty reports whether the span covers no bytes.
func (s Span) Empty() bool { return s.Start == s.End }

// Planning holds a headline's planning-line dates as ISO dates
// (2006-01-02), empty when absent.
type Planning struct {
	Scheduled string
	Deadline  string
	Closed    string
}

// Property is one `:KEY: value` line of a `:PROPERTIES:` drawer, in file
// order. Org property keys are case-insensitive; Key keeps the spelling.
type Property struct {
	Key   string
	Value string
}

// Headline is one org headline and its subtree.
type Headline struct {
	Level    int
	Keyword  string // TODO keyword, "" when none
	Priority string // priority cookie letter, "" when none
	Title    string

	// OwnTags are the tags on the headline line. Tags is the effective set:
	// OwnTags, then ancestors' and #+FILETAGS tags, minus the tags exempt
	// from inheritance (FDR 0026 D4).
	OwnTags []string
	Tags    []string

	Planning   Planning
	Properties []Property

	// Body is the headline's own text before its first child, with drawers
	// removed, dedented and trimmed of surrounding blank lines.
	Body string

	// OutlinePath is the titles from the top-level ancestor down to this
	// headline.
	OutlinePath []string

	Children []*Headline

	// Synthetic marks the single headline synthesized for a file that has
	// none (File.SynthesizeHeadline).
	Synthetic bool

	// Byte spans. Line excludes the newline. PlanningSpan and DrawerSpan
	// are empty insertion points when absent. BodySpan runs from after the
	// drawer to the first child or next headline. SubtreeSpan covers the
	// headline line through its last descendant.
	LineSpan     Span
	PlanningSpan Span
	DrawerSpan   Span
	BodySpan     Span
	SubtreeSpan  Span
}

// Property returns the value of the named property, matched
// case-insensitively, and whether it is present.
func (h *Headline) Property(key string) (string, bool) {
	for _, p := range h.Properties {
		if strings.EqualFold(p.Key, key) {
			return p.Value, true
		}
	}
	return "", false
}

// ID returns the headline's `:ID:` property, "" when absent.
func (h *Headline) ID() string {
	id, _ := h.Property("ID")
	return id
}

// Warning is a tolerated parse problem, reported rather than dropped.
// Line is 1-based.
type Warning struct {
	Line    int
	Message string
}

// File is a parsed org file.
type File struct {
	// Keywords is the effective TODO sequence: the file's #+TODO lines when
	// present, otherwise Options.Keywords (FDR 0026 D3).
	Keywords Keywords
	FileTags []string

	// PreambleSpan covers everything before the first headline.
	PreambleSpan Span
	Headlines    []*Headline // top-level headlines
	Warnings     []Warning

	src []byte
}

// Options configures Parse.
type Options struct {
	// Keywords is the default TODO sequence, used when the file has no
	// #+TODO line. Zero value means DefaultKeywords.
	Keywords Keywords
	// NoInherit lists tags that never pass from a parent (or #+FILETAGS)
	// to its descendants, like org's org-tags-exclude-from-inheritance.
	NoInherit []string
}

type line struct {
	index            int // 0-based line number
	start, end, next int // end excludes "\n" and a trailing "\r"
	text             string
}

func splitLines(src []byte) []line {
	var out []line
	for start := 0; start < len(src); {
		next := len(src)
		end := len(src)
		if i := bytes.IndexByte(src[start:], '\n'); i >= 0 {
			end, next = start+i, start+i+1
		}
		if end > start && src[end-1] == '\r' {
			end--
		}
		out = append(out, line{index: len(out), start: start, end: end, next: next, text: string(src[start:end])})
		start = next
	}
	return out
}

var (
	headlineRe  = regexp.MustCompile(`^(\*+)[ \t]+(.*)$`)
	tagSuffixRe = regexp.MustCompile(`(?:^|[ \t]+)(:(?:[^\s:]+:)+)[ \t]*$`)
	priorityRe  = regexp.MustCompile(`^\[#([A-Z]|[0-9]{1,2})\](?:[ \t]+|$)`)
	blockRe     = regexp.MustCompile(`(?i)^[ \t]*#\+(BEGIN|END)_`)
	planningRe  = regexp.MustCompile(`(SCHEDULED|DEADLINE|CLOSED):[ \t]*[<\[](\d{4}-\d{2}-\d{2})[^>\]]*[>\]]`)
	propertyRe  = regexp.MustCompile(`^[ \t]*:([^\s:]+):(?:[ \t]+(.*?))?[ \t]*$`)
	drawerRe    = regexp.MustCompile(`^[ \t]*:([A-Za-z0-9_-]+):[ \t]*$`)
	settingRe   = regexp.MustCompile(`^#\+([A-Za-z_]+):[ \t]*(.*)$`)
)

// Parse parses src as an org file. It never fails: problems it can
// tolerate are reported in File.Warnings.
func Parse(src []byte, opts Options) *File {
	lines := splitLines(src)
	f := &File{src: src}
	defaults := opts.Keywords
	if len(defaults.Active)+len(defaults.Done) == 0 {
		defaults = DefaultKeywords
	}
	f.Keywords = defaults
	inBlock := blockLines(lines)
	f.readSettings(lines, inBlock)

	var heads []int // indexes into lines of headline lines
	for i, l := range lines {
		if !inBlock[i] && headlineRe.MatchString(l.text) {
			heads = append(heads, i)
		}
	}

	f.PreambleSpan = Span{0, len(src)}
	if len(heads) > 0 {
		f.PreambleSpan.End = lines[heads[0]].start
	}

	var stack []*Headline
	for hi, li := range heads {
		bodyEndLine := len(lines)
		if hi+1 < len(heads) {
			bodyEndLine = heads[hi+1]
		}
		h := f.parseHeadline(lines, li, bodyEndLine)
		// Every headline still open at a level >= h's ends where h starts;
		// those left open at EOF keep End = len(src).
		h.SubtreeSpan = Span{lines[li].start, len(src)}
		for len(stack) > 0 && stack[len(stack)-1].Level >= h.Level {
			stack[len(stack)-1].SubtreeSpan.End = h.SubtreeSpan.Start
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			h.OutlinePath = []string{h.Title}
			f.Headlines = append(f.Headlines, h)
		} else {
			parent := stack[len(stack)-1]
			h.OutlinePath = append(append([]string(nil), parent.OutlinePath...), h.Title)
			parent.Children = append(parent.Children, h)
		}
		stack = append(stack, h)
	}

	inherited := withoutTags(f.FileTags, opts.NoInherit)
	for _, h := range f.Headlines {
		resolveTags(h, inherited, opts.NoInherit)
	}
	return f
}

// blockLines marks the lines inside `#+BEGIN_…` / `#+END_…` blocks (the
// delimiters included), where org sees neither headlines nor settings. An
// unclosed block runs to the end of the file, as in org.
func blockLines(lines []line) []bool {
	in := make([]bool, len(lines))
	open := false
	for i, l := range lines {
		m := blockRe.FindStringSubmatch(l.text)
		switch {
		case m != nil && strings.EqualFold(m[1], "BEGIN"):
			open = true
			in[i] = true
		case m != nil && open:
			in[i] = true
			open = false
		default:
			in[i] = open
		}
	}
	return in
}

// readSettings scans every `#+KEY:` line outside blocks for TODO sequences
// and FILETAGS. Any #+TODO line replaces the default sequence; several
// accumulate.
func (f *File) readSettings(lines []line, inBlock []bool) {
	var fileKeywords Keywords
	for i, l := range lines {
		if inBlock[i] {
			continue
		}
		m := settingRe.FindStringSubmatch(l.text)
		if m == nil {
			continue
		}
		switch strings.ToUpper(m[1]) {
		case "TODO", "SEQ_TODO", "TYP_TODO":
			kw, err := ParseKeywords(m[2])
			if err != nil {
				f.warn(i, err.Error())
				continue
			}
			fileKeywords = fileKeywords.merge(kw)
		case "FILETAGS":
			f.FileTags = appendUnique(f.FileTags, splitTags(strings.TrimSpace(m[2]))...)
		}
	}
	if len(fileKeywords.Active)+len(fileKeywords.Done) > 0 {
		f.Keywords = fileKeywords
	}
}

func (f *File) warn(lineIndex int, msg string) {
	f.Warnings = append(f.Warnings, Warning{Line: lineIndex + 1, Message: msg})
}

// parseHeadline parses the headline at lines[li], whose own region (planning,
// drawer, body) ends before lines[bodyEnd].
func (f *File) parseHeadline(lines []line, li, bodyEnd int) *Headline {
	l := lines[li]
	m := headlineRe.FindStringSubmatch(l.text)
	h := &Headline{Level: len(m[1]), LineSpan: Span{l.start, l.end}}
	rest := m[2]

	if tm := tagSuffixRe.FindStringSubmatchIndex(rest); tm != nil {
		h.OwnTags = splitTags(rest[tm[2]:tm[3]])
		rest = rest[:tm[0]]
	}
	rest = strings.TrimSpace(rest)
	word, after := rest, ""
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		word, after = rest[:i], rest[i:]
	}
	if f.Keywords.Contains(word) {
		h.Keyword = word
		rest = strings.TrimSpace(after)
	}
	if pm := priorityRe.FindStringSubmatch(rest); pm != nil {
		h.Priority = pm[1]
		rest = strings.TrimSpace(rest[len(pm[0]):])
	}
	h.Title = rest

	cur := li + 1
	insertAt := l.next
	h.PlanningSpan = Span{insertAt, insertAt}
	// A line is planning only if it starts with a planning keyword AND
	// carries a timestamp; otherwise it stays body text.
	var planning [][]string
	if cur < bodyEnd && isPlanningLine(lines[cur].text) {
		planning = planningRe.FindAllStringSubmatch(lines[cur].text, -1)
	}
	if len(planning) > 0 {
		for _, pm := range planning {
			switch pm[1] {
			case "SCHEDULED":
				h.Planning.Scheduled = pm[2]
			case "DEADLINE":
				h.Planning.Deadline = pm[2]
			case "CLOSED":
				h.Planning.Closed = pm[2]
			}
		}
		h.PlanningSpan = Span{lines[cur].start, lines[cur].next}
		insertAt = lines[cur].next
		cur++
	}

	h.DrawerSpan = Span{insertAt, insertAt}
	if cur < bodyEnd && strings.EqualFold(strings.TrimSpace(lines[cur].text), ":PROPERTIES:") {
		// An unclosed drawer is left in the body, where bodyText warns once.
		if end := closingEnd(lines[:bodyEnd], cur+1); end >= 0 {
			for j := cur + 1; j < end; j++ {
				pm := propertyRe.FindStringSubmatch(lines[j].text)
				if pm == nil {
					f.warn(j, "malformed property line ignored")
					continue
				}
				h.Properties = append(h.Properties, Property{Key: pm[1], Value: pm[2]})
			}
			h.DrawerSpan = Span{lines[cur].start, lines[end].next}
			cur = end + 1
		}
	}

	bodyStart := h.DrawerSpan.End // == insertAt when the drawer is absent
	bodyStop := len(f.src)
	if bodyEnd < len(lines) {
		bodyStop = lines[bodyEnd].start
	}
	h.BodySpan = Span{bodyStart, bodyStop}
	h.Body = f.bodyText(lines[cur:bodyEnd])
	return h
}

// closingEnd returns the index of the first `:END:` line at or after from,
// or -1.
func closingEnd(lines []line, from int) int {
	for j := from; j < len(lines); j++ {
		if strings.EqualFold(strings.TrimSpace(lines[j].text), ":END:") {
			return j
		}
	}
	return -1
}

func isPlanningLine(text string) bool {
	t := strings.TrimSpace(text)
	for _, kw := range []string{"SCHEDULED:", "DEADLINE:", "CLOSED:"} {
		if strings.HasPrefix(t, kw) {
			return true
		}
	}
	return false
}

// bodyText renders body lines without drawers (LOGBOOK and the like stay
// only in the raw subtree), dedented and trimmed of blank edges.
func (f *File) bodyText(body []line) string {
	var kept []string
	for i := 0; i < len(body); i++ {
		t := body[i].text
		if dm := drawerRe.FindStringSubmatch(t); dm != nil && !strings.EqualFold(dm[1], "END") {
			if end := closingEnd(body, i+1); end >= 0 {
				i = end
				continue
			}
			f.warn(body[i].index, "drawer :"+dm[1]+": has no :END:; kept as body text")
		}
		kept = append(kept, t)
	}
	for len(kept) > 0 && strings.TrimSpace(kept[0]) == "" {
		kept = kept[1:]
	}
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}
	indent := -1
	for _, t := range kept {
		if strings.TrimSpace(t) == "" {
			continue
		}
		n := len(t) - len(strings.TrimLeft(t, " \t"))
		if indent < 0 || n < indent {
			indent = n
		}
	}
	for i, t := range kept {
		if len(t) >= indent && indent > 0 {
			kept[i] = t[indent:]
		} else if strings.TrimSpace(t) == "" {
			kept[i] = ""
		}
	}
	return strings.Join(kept, "\n")
}

func splitTags(s string) []string {
	var out []string
	for _, t := range strings.Split(strings.Trim(s, ":"), ":") {
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func withoutTags(tags, drop []string) []string {
	var out []string
	for _, t := range tags {
		if !slices.Contains(drop, t) {
			out = append(out, t)
		}
	}
	return out
}

// resolveTags sets h.Tags (own first, then inherited) and recurses with the
// set h passes down.
func resolveTags(h *Headline, inherited, noInherit []string) {
	h.Tags = appendUnique(h.OwnTags, inherited...)
	down := appendUnique(withoutTags(h.OwnTags, noInherit), inherited...)
	for _, c := range h.Children {
		resolveTags(c, down, noInherit)
	}
}

// Source returns the bytes of span s.
func (f *File) Source(s Span) []byte { return f.src[s.Start:s.End] }

// SynthesizeHeadline returns the single headline standing in for a file
// with no headlines: titled by the first non-empty, non-setting line, else
// stem, with the whole file as its body. It returns nil when the file has
// headlines.
func (f *File) SynthesizeHeadline(stem string) *Headline {
	if len(f.Headlines) > 0 {
		return nil
	}
	var content []line // the file minus its #+ setting lines
	for _, l := range splitLines(f.src) {
		if !settingRe.MatchString(l.text) {
			content = append(content, l)
		}
	}
	title := stem
	for _, l := range content {
		if t := strings.TrimSpace(l.text); t != "" {
			title = t
			break
		}
	}
	all := Span{0, len(f.src)}
	return &Headline{
		Level:        1,
		Title:        title,
		Tags:         append([]string(nil), f.FileTags...),
		Body:         (&File{}).bodyText(content), // scratch File: repeat calls must not re-append warnings
		OutlinePath:  []string{title},
		Synthetic:    true,
		LineSpan:     Span{0, 0},
		PlanningSpan: Span{0, 0},
		DrawerSpan:   Span{0, 0},
		BodySpan:     all,
		SubtreeSpan:  all,
	}
}
