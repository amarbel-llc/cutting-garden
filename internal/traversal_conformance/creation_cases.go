package traversal_conformance

// The RFC 0013 §Creation points (forge organize F10): a peer's `creatable`
// node_types members must pass the host's bring-up check, and its
// node.create_child must accept the HOST-built fields body — the trailer under
// trailer_field, a `one` dimension's value under its facet write's field as a
// string, a `many` dimension's set as an array — creating a node that lists
// with that name and those facet values. The body comes from the same builder
// the host's WirePlugin uses, so a passing peer is one organize can create in.

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/traversal_serve"
)

const (
	nameCreationDecl        = "initialize: node_types creatable declarations are usable by the host"
	nameCreationChild       = "node.create_child: host-built create body creates the node with its fields"
	nameCreationIdempotency = "node.create_child: a repeated idempotency_key returns the same node, existed"
)

// conformanceIdempotencyKey is the key the idempotency point sends (shaped
// like the host's `cgk1-` keys).
const conformanceIdempotencyKey = "cgk1-c0f0c0f0c0f0c0f0c0f0c0f0c0f0c0f0"

// caseCreation runs the two creation points. A peer declaring no creatable
// type SKIPs both (the member is OPTIONAL); a declaring peer always gets the
// declaration point, and the manifest's [creation] table opts the create
// probe in.
func (r *runner) caseCreation(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, perCaseDeadline)
	defer cancel()

	if len(traversal_serve.CreationsOf(r.init)) == 0 {
		for _, name := range []string{nameCreationDecl, nameCreationChild, nameCreationIdempotency} {
			r.tap.Skip(name, "peer declares no creatable node types")
		}
		return
	}

	declErr := traversal_serve.ValidateCreationDeclaration(r.init)
	if declErr != nil {
		r.tap.NotOk(nameCreationDecl, map[string]string{"node_types": declErr.Error()})
	} else {
		r.tap.Ok(nameCreationDecl)
	}

	switch spec := r.manifest.Creation; {
	case spec == nil:
		r.tap.Skip(nameCreationChild, "manifest declares no creation")
		r.tap.Skip(nameCreationIdempotency, "manifest declares no creation")
	case declErr != nil:
		r.tap.Skip(nameCreationChild, "the creatable declaration is unusable")
		r.tap.Skip(nameCreationIdempotency, "the creatable declaration is unusable")
	default:
		r.creationChild(ctx, spec)
		r.creationIdempotency(ctx, spec)
	}
}

// creationIdempotency sends the same host-built create twice with one
// `idempotency_key`: a peer honoring the key MUST answer the repeat with the
// SAME `created` URI and `"existed": true`, leaving one node. A peer that
// ignores the key (it MAY — the host's receipt covers it) creates twice; the
// point SKIPs then. Every created node is deleted afterwards.
func (r *runner) creationIdempotency(ctx context.Context, spec *CreationSpec) {
	body, err := r.creationBody(spec)
	if err != nil {
		r.tap.NotOk(nameCreationIdempotency, map[string]string{"build": err.Error()})
		return
	}

	call := func() (traversal_serve.NodeCreateChildResult, error) {
		var result traversal_serve.NodeCreateChildResult
		err := r.session.Call(ctx, traversal_serve.MethodNodeCreateChild,
			traversal_serve.NodeCreateChildParams{
				Container:      spec.Container,
				Type:           spec.Type,
				BodyBase64:     encodeBody(string(body)),
				IdempotencyKey: conformanceIdempotencyKey,
			}, &result)
		return result, err
	}

	first, err := call()
	if err != nil {
		r.tap.NotOk(nameCreationIdempotency, map[string]string{"first node.create_child": err.Error()})
		return
	}
	second, err := call()
	cleanup := func(uris ...string) map[string]string {
		problems := map[string]string{}
		if !r.hasCapability(traversal_serve.CapMutate) {
			return problems
		}
		seen := map[string]bool{}
		for _, uri := range uris {
			if uri == "" || seen[uri] {
				continue
			}
			seen[uri] = true
			if err := r.session.Call(ctx, traversal_serve.MethodNodeDelete,
				traversal_serve.NodeDeleteParams{URI: uri}, nil); err != nil {
				problems["cleanup "+uri] = err.Error()
			}
		}
		return problems
	}
	if err != nil {
		problems := cleanup(first.Created)
		problems["second node.create_child"] = err.Error()
		r.tap.NotOk(nameCreationIdempotency, problems)
		return
	}
	if second.Created != first.Created && !second.Existed {
		problems := cleanup(first.Created, second.Created)
		if len(problems) > 0 {
			r.tap.NotOk(nameCreationIdempotency, problems)
			return
		}
		r.tap.Skip(nameCreationIdempotency, "peer does not honor idempotency_key (it MAY ignore it)")
		return
	}

	problems := map[string]string{}
	switch {
	case first.Existed:
		problems["first"] = "a first create reported existed"
	case second.Created != first.Created:
		problems["second"] = fmt.Sprintf("existed, but created %q != %q", second.Created, first.Created)
	case !second.Existed:
		problems["second"] = "the repeat returned the same URI without existed: true"
	}
	nodes, ok := r.listNodesDecoded(ctx, spec.Container, nil)
	if !ok {
		problems["read-back"] = "nodes.list " + spec.Container + " failed"
	} else if n := countURI(nodes, first.Created); n != 1 {
		problems["read-back"] = fmt.Sprintf("%s listed %d times, want 1", first.Created, n)
	}
	for k, v := range cleanup(first.Created) {
		problems[k] = v
	}
	r.verdict(nameCreationIdempotency, problems)
}

func countURI(nodes []cutting_garden_plugins.Node, uri string) int {
	n := 0
	for _, node := range nodes {
		if node.URIString() == uri {
			n++
		}
	}
	return n
}

// creationBody builds the host create body for the manifest's fields.
func (r *runner) creationBody(spec *CreationSpec) ([]byte, error) {
	presentation := traversal_serve.NewPresentation(
		traversal_serve.PresentationsOf(r.init), r.declaredFacetWrites(),
	)
	trailerField := ""
	for _, p := range traversal_serve.PresentationsOf(r.init) {
		if p.Tag == spec.Type {
			trailerField = p.TrailerField
		}
	}
	fields := map[string][]string{trailerField: {spec.Trailer}}
	if spec.OneDimension != "" {
		fields[spec.OneDimension] = []string{spec.OneValue}
	}
	if spec.ManyDimension != "" {
		fields[spec.ManyDimension] = spec.ManySet
	}
	return presentation.BuildCreateBody(spec.Type, fields)
}

// creationChild builds the host body for spec's fields, creates the node,
// reads it back through nodes.list of the container, and deletes it.
func (r *runner) creationChild(ctx context.Context, spec *CreationSpec) {
	body, err := r.creationBody(spec)
	if err != nil {
		r.tap.NotOk(nameCreationChild, map[string]string{"build": err.Error()})
		return
	}

	created, err := r.createChildProbe(ctx, spec.Container, spec.Type, string(body))
	if err != nil {
		r.tap.NotOk(nameCreationChild, map[string]string{
			"node.create_child": fmt.Sprintf("%s: %s", body, err),
		})
		return
	}

	problems := map[string]string{}
	node, ok := r.findListedNode(ctx, &FacetWriteSpec{Container: spec.Container, Node: created})
	switch {
	case !ok:
		problems["read-back"] = fmt.Sprintf("%s is not among nodes.list %s", created, spec.Container)
	case node.Type != spec.Type:
		problems["type"] = fmt.Sprintf("%q, want %q", node.Type, spec.Type)
	case node.Name != spec.Trailer:
		problems["name"] = fmt.Sprintf("%q, want the trailer %q", node.Name, spec.Trailer)
	default:
		if spec.OneDimension != "" {
			if got := facetKeys(node.Facets[spec.OneDimension]); !slices.Equal(got, []string{spec.OneValue}) {
				problems[spec.OneDimension] = fmt.Sprintf("[%s], want [%s]", strings.Join(got, ","), spec.OneValue)
			}
		}
		if spec.ManyDimension != "" {
			if got := facetKeys(node.Facets[spec.ManyDimension]); !sameStringSet(got, spec.ManySet) {
				problems[spec.ManyDimension] = fmt.Sprintf(
					"[%s], want [%s]", strings.Join(got, ","), strings.Join(spec.ManySet, ","),
				)
			}
		}
	}

	if r.hasCapability(traversal_serve.CapMutate) {
		if err := r.session.Call(
			ctx, traversal_serve.MethodNodeDelete,
			traversal_serve.NodeDeleteParams{URI: created}, nil,
		); err != nil {
			problems["cleanup"] = err.Error()
		}
	}

	r.verdict(nameCreationChild, problems)
}

// declaredFacetWrites decodes the peer's facet_writes block.
func (r *runner) declaredFacetWrites() []cutting_garden_plugins.NodeTypeFacetWrites {
	writes := make([]cutting_garden_plugins.NodeTypeFacetWrites, len(r.init.FacetWrites))
	for i, view := range r.init.FacetWrites {
		writes[i] = view.ToNodeTypeFacetWrites()
	}
	return writes
}
