// Package orgtext is the org plugin's hand-rolled, line-oriented org-mode
// parser (FDR 0026). It extracts the headline outline with byte spans so a
// later write slice can splice a file without reformatting it. It depends
// only on the standard library.
package orgtext

import (
	"fmt"
	"strings"
)

// Keywords is a TODO keyword sequence: Active states before the `|`, Done
// (terminal) states after it.
type Keywords struct {
	Active []string
	Done   []string
}

// DefaultKeywords is org's own default, `TODO | DONE`.
var DefaultKeywords = Keywords{Active: []string{"TODO"}, Done: []string{"DONE"}}

// ParseKeywords parses an org keyword spec such as
// "TODO NEXT | DONE CANCELLED". Without a `|`, the last word is the only
// done state, as in org. Fast-access suffixes like `TODO(t)` or `DONE(d!)`
// are dropped.
func ParseKeywords(spec string) (Keywords, error) {
	before, after, hasBar := strings.Cut(spec, "|")
	active := keywordWords(before)
	var done []string
	if hasBar {
		done = keywordWords(after)
	} else if len(active) > 0 {
		done = active[len(active)-1:]
		active = active[:len(active)-1]
	}
	if len(active)+len(done) == 0 {
		return Keywords{}, fmt.Errorf("orgtext: empty TODO keyword spec %q", spec)
	}
	return Keywords{Active: active, Done: done}, nil
}

func keywordWords(s string) []string {
	var out []string
	for _, w := range strings.Fields(s) {
		if i := strings.IndexByte(w, '('); i > 0 {
			w = w[:i]
		}
		out = append(out, w)
	}
	return out
}

// Contains reports whether word is any keyword in k.
func (k Keywords) Contains(word string) bool {
	return k.IsDone(word) || containsString(k.Active, word)
}

// IsDone reports whether word is a terminal (done) keyword in k.
func (k Keywords) IsDone(word string) bool { return containsString(k.Done, word) }

// merge returns the union of k and other, keeping first-seen order.
func (k Keywords) merge(other Keywords) Keywords {
	return Keywords{
		Active: appendUnique(k.Active, other.Active...),
		Done:   appendUnique(k.Done, other.Done...),
	}
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func appendUnique(list []string, items ...string) []string {
	out := append([]string(nil), list...)
	for _, it := range items {
		if !containsString(out, it) {
			out = append(out, it)
		}
	}
	return out
}
