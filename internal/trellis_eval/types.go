package trellis_eval

import (
	"slices"
	"strings"

	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// checkDeclaredTypes refuses a `!type` term naming a type the plugin does not
// declare. Matching is exact, so such a term can only ever match nothing, and
// an empty result is indistinguishable from "no node matched" — the false
// empty of cutting-garden#266, where `!caldav-object-v1` silently matched no
// `caldav-object-vevent-v1` node. A plugin declaring no types is not checked.
func checkDeclaredTypes(lister cgp.RootLister, steps []trellis.Step) error {
	declared := lister.Types()
	if len(declared) == 0 {
		return nil
	}
	known := make([]string, 0, len(declared))
	for _, nt := range declared {
		known = append(known, nt.Tag)
	}
	for _, name := range typeTermNames(steps) {
		if !slices.Contains(known, name) {
			slices.Sort(known)
			return errors.BadRequestf(
				"trellis: unknown node type `!%s`; this plugin's types are: %s",
				name, strings.Join(known, ", "),
			)
		}
	}
	return nil
}

// typeTermNames collects every `!type` name in steps, including those inside
// OR alternatives and existential subpaths.
func typeTermNames(steps []trellis.Step) []string {
	var names []string
	var visit func(terms []trellis.Term)
	visit = func(terms []trellis.Term) {
		for _, term := range terms {
			switch b := term.Basic.(type) {
			case trellis.TypeBasicTerm:
				names = append(names, b.Type.Name)
			case trellis.GroupBasicTerm:
				switch body := b.Group.Body.(type) {
				case trellis.SubPath:
					if body.Path != nil {
						for _, step := range body.Path.Steps {
							visit(step.Terms)
						}
					}
				case trellis.Alternatives:
					for _, alt := range body.Alts {
						visit(alt.Terms)
					}
				}
			}
		}
	}
	for _, step := range steps {
		visit(step.Terms)
	}
	return names
}
