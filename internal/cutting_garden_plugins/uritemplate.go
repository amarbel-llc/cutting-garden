package cutting_garden_plugins

import (
	"net/url"
	"regexp"
	"strings"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// URITemplate is a parsed, validated RFC 6570 Level 1 URI template
// (RFC 0018 §2) supporting bidirectional resolution: Expand fills the
// template's variables to mint a URI, and Match reverses a URI back to
// its variable bindings. A node type MAY declare one (NodeType.URITemplate)
// so the host can answer URI→type locally, without a round trip, and so a
// plugin can mint its own URIs consistently.
//
// The Level 1 subset — bare {name} expressions, no operators, prefixes, or
// explode — is the largest RFC 6570 subset that reverses unambiguously
// (RFC 0018 §2) and re-exports verbatim as a Model Context Protocol
// resource template (RFC 0018 §8). A {name} captures exactly one path
// segment or sub-segment: it never spans '/'. The value it captures is
// the maximal such run that still lets the remainder match — greedy, so a
// following literal delimiter binds at its LAST occurrence.
//
// The zero value is not usable; obtain a URITemplate only from
// ParseURITemplate.
type URITemplate struct {
	raw          string
	varNames     []string
	literalCount int
	prefixLen    int
	re           *regexp.Regexp
}

// uriTemplateVarName is the Level 1 variable-name grammar (RFC 0018 §2):
// it deliberately rejects every RFC 6570 operator, the ':' prefix
// modifier, and the '*' explode modifier by admitting only bare
// identifier characters.
var uriTemplateVarName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ParseURITemplate validates s against the Level 1 subset (RFC 0018 §2)
// and returns the parsed, match-ready template. It rejects an operator or
// modifier inside an expression, two variables with no intervening
// literal, a duplicate variable name, and an unclosed '{'. An empty string
// parses to a variable-free template that matches only itself (a fixed
// URI, e.g. a singleton).
func ParseURITemplate(s string) (URITemplate, error) {
	var (
		names        []string
		seen         = map[string]bool{}
		literalCount int
		prefixLen    int
		sawVar       bool
		lastWasVar   bool
		re           strings.Builder
	)

	re.WriteString(`^`)

	for i := 0; i < len(s); {
		if s[i] == '{' {
			rel := strings.IndexByte(s[i:], '}')
			if rel < 0 {
				return URITemplate{}, errors.Errorf(
					"uri template %q: unclosed '{'", s,
				)
			}

			name := s[i+1 : i+rel]
			if !uriTemplateVarName.MatchString(name) {
				return URITemplate{}, errors.Errorf(
					"uri template %q: %q is not a bare Level 1 variable "+
						"(no operators, prefixes, or explode)", s, name,
				)
			}
			if seen[name] {
				return URITemplate{}, errors.Errorf(
					"uri template %q: duplicate variable %q", s, name,
				)
			}
			if lastWasVar {
				return URITemplate{}, errors.Errorf(
					"uri template %q: adjacent variables need an "+
						"intervening literal", s,
				)
			}

			seen[name] = true
			names = append(names, name)
			re.WriteString(`([^/]+)`)
			sawVar = true
			lastWasVar = true
			i += rel + 1

			continue
		}

		rel := strings.IndexByte(s[i:], '{')

		var lit string
		if rel < 0 {
			lit = s[i:]
			i = len(s)
		} else {
			lit = s[i : i+rel]
			i += rel
		}

		literalCount += len(lit)
		if !sawVar {
			prefixLen += len(lit)
		}
		re.WriteString(regexp.QuoteMeta(lit))
		lastWasVar = false
	}

	re.WriteString(`$`)

	compiled, err := regexp.Compile(re.String())
	if err != nil {
		return URITemplate{}, errors.Errorf(
			"uri template %q: compile: %s", s, err,
		)
	}

	return URITemplate{
		raw:          s,
		varNames:     names,
		literalCount: literalCount,
		prefixLen:    prefixLen,
		re:           compiled,
	}, nil
}

// Expand fills every variable from bindings and returns the minted URI
// (RFC 0018 §3). Each value is percent-encoded as a single path segment
// (url.PathEscape, so an embedded '/' becomes %2F per the §3 single-segment
// rule). A variable with no binding is an error, never an empty
// substitution.
func (t URITemplate) Expand(bindings map[string]string) (string, error) {
	var out strings.Builder

	for i := 0; i < len(t.raw); {
		if t.raw[i] == '{' {
			rel := strings.IndexByte(t.raw[i:], '}')
			name := t.raw[i+1 : i+rel]

			val, ok := bindings[name]
			if !ok {
				return "", errors.Errorf(
					"uri template %q: variable %q is unbound", t.raw, name,
				)
			}

			out.WriteString(url.PathEscape(val))
			i += rel + 1

			continue
		}

		rel := strings.IndexByte(t.raw[i:], '{')
		if rel < 0 {
			out.WriteString(t.raw[i:])

			break
		}

		out.WriteString(t.raw[i : i+rel])
		i += rel
	}

	return out.String(), nil
}

// Match reverses uri against the template (RFC 0018 §3). On a full,
// anchored match it returns the variables' percent-decoded bindings and
// true; a URI that does not match end to end returns nil, false — never a
// partial result. A capture that is not valid percent-encoding (so it
// could not have come from Expand) is treated as no match rather than a
// surfaced decode error.
func (t URITemplate) Match(uri string) (map[string]string, bool) {
	if t.re == nil {
		return nil, false
	}

	m := t.re.FindStringSubmatch(uri)
	if m == nil {
		return nil, false
	}

	bindings := make(map[string]string, len(t.varNames))
	for i, name := range t.varNames {
		val, err := url.PathUnescape(m[i+1])
		if err != nil {
			return nil, false
		}
		bindings[name] = val
	}

	return bindings, true
}

// Raw returns the template's source string.
func (t URITemplate) Raw() string { return t.raw }

// LiteralCount is the number of literal (non-variable) characters — the
// primary key of the resolver's most-specific-match rule (RFC 0018 §4).
func (t URITemplate) LiteralCount() int { return t.literalCount }

// VarCount is the number of variables — the resolver's first tie-break
// (fewer variables is more specific, RFC 0018 §4).
func (t URITemplate) VarCount() int { return len(t.varNames) }

// PrefixLen is the count of literal characters before the first variable —
// the resolver's final tie-break (RFC 0018 §4).
func (t URITemplate) PrefixLen() int { return t.prefixLen }
