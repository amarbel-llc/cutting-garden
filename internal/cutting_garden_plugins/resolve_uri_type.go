package cutting_garden_plugins

import "sync"

// ResolvedNodeType is the outcome of URI→type resolution (RFC 0018 §4):
// the node type whose declared URITemplate matched a URI, together with
// the variable bindings that match captured.
type ResolvedNodeType struct {
	Type     NodeType
	Bindings map[string]string
}

// uriTemplateCache memoizes parsed templates by their source string so a
// hot read path (the #168 gate resolves a URI on every read_node) does not
// recompile the same regexp per call. Templates are stable for a session,
// so the cache never needs invalidation; a parse error is cached too, so a
// malformed template is skipped cheaply on every subsequent resolve.
var uriTemplateCache sync.Map // string -> cachedTemplate

type cachedTemplate struct {
	template URITemplate
	err      error
}

func parseURITemplateCached(s string) (URITemplate, error) {
	if v, ok := uriTemplateCache.Load(s); ok {
		c := v.(cachedTemplate)

		return c.template, c.err
	}

	template, err := ParseURITemplate(s)
	uriTemplateCache.Store(s, cachedTemplate{template: template, err: err})

	return template, err
}

// ResolveNodeTypeByURI answers RFC 0018's URI→type question locally, with
// no plugin round trip: it matches uri against the URITemplate of each type
// lister declares, and returns the winning type and its captured bindings.
//
// When several templates match, RFC 0018 §4's most-specific rule selects
// one — greater literal-character count, then fewer variables, then longer
// literal prefix. A TRUE tie (no strict winner) returns ok == false: the
// host MUST fall back (RFC 0018 §6; the #168 gate probes) and MUST NOT
// guess. A type declaring no template, an unparseable template, and a URI
// no template matches all likewise return ok == false — the template is an
// optimization, never a precondition. Roots are outside this scheme
// (RFC 0018 §5) and are deliberately not resolved through this path.
func ResolveNodeTypeByURI(
	lister RootLister, uri string,
) (ResolvedNodeType, bool) {
	type candidate struct {
		resolved ResolvedNodeType
		template URITemplate
	}

	var candidates []candidate

	for _, nodeType := range lister.Types() {
		if nodeType.URITemplate == "" {
			continue
		}

		template, err := parseURITemplateCached(nodeType.URITemplate)
		if err != nil {
			// A malformed template resolves nothing rather than failing the
			// read; RFC 0018 §4 leaves rejecting it to an OPTIONAL init check.
			continue
		}

		bindings, ok := template.Match(uri)
		if !ok {
			continue
		}

		candidates = append(candidates, candidate{
			resolved: ResolvedNodeType{Type: nodeType, Bindings: bindings},
			template: template,
		})
	}

	switch len(candidates) {
	case 0:
		return ResolvedNodeType{}, false
	case 1:
		return candidates[0].resolved, true
	}

	best, tied := candidates[0], false
	for _, c := range candidates[1:] {
		switch compareSpecificity(c.template, best.template) {
		case 1:
			best, tied = c, false
		case 0:
			tied = true
		}
	}

	if tied {
		// A true tie is a modeling problem (RFC 0018 §4): resolve to ⊥ so
		// the caller falls back rather than picking a type arbitrarily.
		return ResolvedNodeType{}, false
	}

	return best.resolved, true
}

// compareSpecificity orders two templates by RFC 0018 §4's rule, returning
// 1 when a is strictly more specific than b, -1 when strictly less, and 0
// when they tie on every key (an ambiguous pair).
func compareSpecificity(a, b URITemplate) int {
	if a.LiteralCount() != b.LiteralCount() {
		if a.LiteralCount() > b.LiteralCount() {
			return 1
		}

		return -1
	}

	if a.VarCount() != b.VarCount() {
		// Fewer variables is more specific.
		if a.VarCount() < b.VarCount() {
			return 1
		}

		return -1
	}

	if a.PrefixLen() != b.PrefixLen() {
		if a.PrefixLen() > b.PrefixLen() {
			return 1
		}

		return -1
	}

	return 0
}
