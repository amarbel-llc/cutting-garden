package organize

import (
	"code.linenisgreat.com/cutting-garden/internal/cgconfig"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// groupByTree is the `(tree)` grouping spelling (cutting-garden#353). Nothing
// parses it as a grouping yet; `_nest` is the only part of it that exists.
const groupByTree = "(tree)"

// fieldNest is the `- _nest = full | 1 | none` envelope lever: how deep a
// `(tree)` grouping renders as nested continuation headings. It is the one
// PRESENTATION lever — renderCanonical leaves it out, so the bytes `_base`
// content-addresses do not depend on it and changing only `_nest` is never an
// unapplied edit (fmt-organize regenerates with the new spelling; apply sees no
// change). Every other lever stays in the canonical form because it changes what
// an edit MEANS.
const fieldNest = "_nest"

const (
	nestFull = cgconfig.NestFull
	nestOne  = cgconfig.NestOne
	nestNone = cgconfig.NestNone
)

// parseNestField validates a `- _nest = <value>` envelope value; empty (absent)
// stays empty. An out-of-domain value is a loud bad request.
func parseNestField(raw string) (string, error) {
	switch raw {
	case "", nestFull, nestOne, nestNone:
		return raw, nil
	}
	return "", errors.BadRequestf(
		"organize: `- %s = %s` is not one of %s, %s, %s",
		fieldNest, raw, nestFull, nestOne, nestNone,
	)
}

// effectiveNest resolves the lever like effectiveTagAtoms: the document's field,
// then the `[organize] nest` config default, then the built-in default (full).
func effectiveNest(docVal, configVal string) string {
	return firstNonEmpty(docVal, configVal, nestFull)
}

// nestEnvelopeValue is the value generate persists as `- _nest`: empty (omitted)
// at the built-in default, and for every grouping but `(tree)` — a config
// default for the tree lever is simply not consulted by another grouping — and
// the effective value otherwise.
func nestEnvelopeValue(groupBy, docVal, configVal string) string {
	if groupBy != groupByTree {
		return ""
	}
	if v := effectiveNest(docVal, configVal); v != nestFull {
		return v
	}
	return ""
}

// refuseNestOutsideTree is parseDocument's cross-check: `_nest` shapes only a
// `(tree)` grouping, so on any other document it would do nothing — which is a
// trap, not a default.
func refuseNestOutsideTree(doc document) error {
	if doc.Nest == "" || doc.GroupBy == groupByTree {
		return nil
	}
	return errors.BadRequestf(
		"organize: `- %s = %s` applies only to a `- _group-by = %s` document",
		fieldNest, doc.Nest, groupByTree,
	)
}
