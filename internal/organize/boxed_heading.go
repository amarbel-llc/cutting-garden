package organize

import (
	"fmt"
	"strings"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// A boxed heading (`# ["id=…"] trips`, cutting-garden#377) is a node standing as
// a heading. Parsing and rendering carry it; what EDITING one means (rename,
// reparent, unbox) is not specified yet, so apply refuses every edit and
// every creation beneath one, and applies an untouched one as a no-op — a
// heading's box is not an object line, so no planner sees it.

// boxedHeading is one boxed heading with the heading terms above it, outermost
// first — enough to tell whether it moved.
type boxedHeading struct {
	ID     string
	Term   string
	Parent string // the enclosing headings' terms joined by NUL
}

// boxedHeadings lists doc's boxed headings in document order.
func (doc document) boxedHeadings() []boxedHeading {
	var out []boxedHeading
	var stack []int
	for i, s := range doc.Sections {
		stack = popTo(stack, doc.Sections, s.Depth)
		if s.Box != nil {
			terms := make([]string, len(stack))
			for k, idx := range stack {
				terms[k] = doc.Sections[idx].Term
			}
			out = append(out, boxedHeading{ID: s.Box.ID, Term: s.Term, Parent: strings.Join(terms, "\x00")})
		}
		stack = append(stack, i)
	}
	return out
}

// refuseBoxedHeadingEdits is apply's guard for boxed headings: editing the text,
// moving or deleting one, conjuring one from a plain heading, or creating an
// object beneath one is a bad request naming the heading and pointing at #377
// for the edit semantics. A document whose boxed headings are as pinned passes.
func refuseBoxedHeadingEdits(base, edited document) error {
	baseHeadings := map[string]boxedHeading{}
	for _, h := range base.boxedHeadings() {
		baseHeadings[h.ID] = h
	}
	editedHeadings := map[string]boxedHeading{}
	for _, h := range edited.boxedHeadings() {
		editedHeadings[h.ID] = h
	}

	refuse := func(h boxedHeading, what string) error {
		return errors.BadRequestf(
			"organize --apply: boxed heading %s %q was %s; editing a boxed heading "+
				"is not supported yet (see cutting-garden#377 for the edit semantics)",
			h.ID, h.Term, what)
	}
	for _, h := range base.boxedHeadings() {
		now, ok := editedHeadings[h.ID]
		switch {
		case !ok:
			return refuse(h, "deleted")
		case now.Term != h.Term:
			return refuse(h, fmt.Sprintf("edited (now %q)", now.Term))
		case now.Parent != h.Parent:
			return refuse(h, "moved")
		}
	}
	for _, h := range edited.boxedHeadings() {
		if _, ok := baseHeadings[h.ID]; !ok {
			return refuse(h, "added (a heading cannot be given a box by hand)")
		}
	}

	var stack []int
	for i, s := range edited.Sections {
		stack = popTo(stack, edited.Sections, s.Depth)
		stack = append(stack, i)
		for _, ln := range s.Lines {
			if !ln.New {
				continue
			}
			for k := len(stack) - 1; k >= 0; k-- {
				if b := edited.Sections[stack[k]]; b.Box != nil {
					return errors.BadRequestf(
						"organize --apply: body line %d creates an object beneath boxed heading %s %q; "+
							"creating beneath a boxed heading is not supported yet "+
							"(see cutting-garden#377 for the edit semantics)",
						ln.Line, b.Box.ID, b.Term)
				}
			}
		}
	}
	return nil
}
