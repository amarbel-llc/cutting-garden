package traversal_conformance

// The RFC 0013 facet_writes amendment's method-semantics points: a peer's
// declaration must pass the host's bring-up check, and its node.patch must
// accept the two HOST-built bodies — a write:one `{"<field>": "<bucket>"}`
// that moves the node into the bucket, and a write:many
// `{"<field>": [<set>]}` that REPLACES the node's membership wholesale —
// observable through the node's facets on a re-list. The bodies come from
// the same builders the host's WirePlugin uses, so a passing peer is one
// organize --apply can write through.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/traversal_serve"
)

const (
	nameFacetWriteDecl  = "initialize: facet_writes declaration is usable by the host"
	nameFacetWriteOne   = "node.patch: host-built write:one body moves the node into the bucket"
	nameFacetWriteMany  = "node.patch: host-built write:many body replaces the node's set, [] clears"
	nameFacetWriteClear = "node.patch: host-built clear body (null) empties a clearable write:one dimension"

	namePresentationDecl    = "initialize: node_types presentation members are usable by the host"
	namePresentationTrailer = "node.patch: host-built trailer_field body renames the node"
)

// caseFacetWrites runs the four facet_writes points. A peer declaring no
// facet_writes SKIPs all four (the block is OPTIONAL); a declaring peer
// always gets the declaration point, the manifest's [facet_write] table
// opts the two write probes in, and its [facet_clear] table the clear probe
// (forge organize F12).
func (r *runner) caseFacetWrites(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, perCaseDeadline)
	defer cancel()

	if len(r.init.FacetWrites) == 0 {
		for _, name := range []string{
			nameFacetWriteDecl, nameFacetWriteOne, nameFacetWriteMany,
			nameFacetWriteClear,
		} {
			r.tap.Skip(name, "peer declares no facet_writes")
		}

		return
	}

	if err := traversal_serve.ValidateFacetWriteDeclaration(r.init); err != nil {
		r.tap.NotOk(nameFacetWriteDecl, map[string]string{"facet_writes": err.Error()})
	} else {
		r.tap.Ok(nameFacetWriteDecl)
	}

	mutate := r.hasCapability(traversal_serve.CapMutate)

	switch spec := r.manifest.FacetWrite; {
	case spec == nil:
		r.tap.Skip(nameFacetWriteOne, "manifest declares no facet_write")
		r.tap.Skip(nameFacetWriteMany, "manifest declares no facet_write")
	case !mutate:
		r.tap.Skip(nameFacetWriteOne, "mutate not advertised")
		r.tap.Skip(nameFacetWriteMany, "mutate not advertised")
	default:
		if spec.OneDimension == "" {
			r.tap.Skip(nameFacetWriteOne, "manifest names no one_dimension")
		} else {
			r.facetWriteOne(ctx, spec)
		}

		if spec.ManyDimension == "" {
			r.tap.Skip(nameFacetWriteMany, "manifest names no many_dimension")
		} else {
			r.facetWriteMany(ctx, spec)
		}
	}

	switch spec := r.manifest.FacetClear; {
	case spec == nil:
		r.tap.Skip(nameFacetWriteClear, "manifest declares no facet_clear")
	case !mutate:
		r.tap.Skip(nameFacetWriteClear, "mutate not advertised")
	default:
		r.facetWriteClear(ctx, spec)
	}
}

// casePresentation runs the RFC 0013 presentation additions' points (forge
// organize F11): a peer carrying presentation members on its node_types
// entries must pass the host's bring-up check of them, and — opted in by the
// manifest's [trailer] table — accept the host-built trailer body and
// reflect it as the node's name. A peer declaring no members SKIPs both
// (every member is OPTIONAL).
func (r *runner) casePresentation(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, perCaseDeadline)
	defer cancel()

	if len(traversal_serve.PresentationsOf(r.init)) == 0 {
		r.tap.Skip(namePresentationDecl, "peer declares no presentation members")
		r.tap.Skip(namePresentationTrailer, "peer declares no presentation members")
		return
	}

	if err := traversal_serve.ValidatePresentationDeclaration(r.init); err != nil {
		r.tap.NotOk(namePresentationDecl, map[string]string{"node_types": err.Error()})
	} else {
		r.tap.Ok(namePresentationDecl)
	}

	switch spec := r.manifest.Trailer; {
	case spec == nil:
		r.tap.Skip(namePresentationTrailer, "manifest declares no trailer")
	case !r.hasCapability(traversal_serve.CapMutate):
		r.tap.Skip(namePresentationTrailer, "mutate not advertised")
	default:
		r.presentationTrailer(ctx, spec)
	}
}

// presentationTrailer sends `{"<trailer_field>": "<text>"}` for the node's
// declared trailer field, reads back that nodes.list names the node text,
// then restores the original name.
func (r *runner) presentationTrailer(ctx context.Context, trailer *TrailerSpec) {
	spec := &FacetWriteSpec{Container: trailer.Container, Node: trailer.Node}

	node, ok := r.findListedNode(ctx, spec)
	if !ok {
		r.tap.NotOk(namePresentationTrailer, map[string]string{
			"setup": fmt.Sprintf("%s is not among nodes.list %s", spec.Node, spec.Container),
		})
		return
	}

	field := ""
	for _, p := range traversal_serve.PresentationsOf(r.init) {
		if p.Tag == node.Type {
			field = p.TrailerField
		}
	}
	if field == "" {
		r.tap.NotOk(namePresentationTrailer, map[string]string{
			"setup": fmt.Sprintf("type %q declares no trailer_field", node.Type),
		})
		return
	}

	problems := map[string]string{}
	if _, _, err := r.patchRaw(ctx, spec.Node, trailerBody(field, trailer.Text)); err != nil {
		problems["node.patch"] = err.Error()
	} else if renamed, ok := r.findListedNode(ctx, spec); !ok {
		problems["read-back"] = spec.Node + " vanished from its container"
	} else if renamed.Name != trailer.Text {
		problems["read-back"] = fmt.Sprintf(
			"after %s, name = %q, want %q",
			trailerBody(field, trailer.Text), renamed.Name, trailer.Text,
		)
	}

	if _, _, err := r.patchRaw(ctx, spec.Node, trailerBody(field, node.Name)); err != nil {
		problems["restore"] = err.Error()
	}

	r.verdict(namePresentationTrailer, problems)
}

func trailerBody(field, text string) string {
	body, _ := json.Marshal(map[string]string{field: text})
	return string(body)
}

// facetWriteClear sends the host-built clear body `{"<field>": null}` for a
// declared CLEARABLE write:one dimension, reads back that the node carries no
// value in it, then restores the original bucket.
func (r *runner) facetWriteClear(ctx context.Context, clear *FacetClearSpec) {
	spec := &FacetWriteSpec{Container: clear.Container, Node: clear.Node}

	node, write, problem := r.facetWriteSubject(
		ctx, spec, clear.Dimension, cutting_garden_plugins.FacetWriteOne,
	)
	if problem == "" && !write.Clearable {
		problem = fmt.Sprintf(
			"type %q declares dimension %q but not clearable", node.Type, clear.Dimension,
		)
	}
	if problem != "" {
		r.tap.NotOk(nameFacetWriteClear, map[string]string{"setup": problem})
		return
	}

	original := facetKeys(node.Facets[clear.Dimension])

	body, err := traversal_serve.HostFacetWritePatch(write, "")
	if err != nil {
		r.tap.NotOk(nameFacetWriteClear, map[string]string{"build": err.Error()})
		return
	}

	problems := r.patchAndReadBack(ctx, spec, body, clear.Dimension, nil)

	if len(original) == 1 {
		restore, _ := traversal_serve.HostFacetWritePatch(write, original[0])
		if _, _, err := r.patchRaw(ctx, spec.Node, string(restore)); err != nil {
			problems["restore"] = err.Error()
		}
	}

	r.verdict(nameFacetWriteClear, problems)
}

// facetWriteOne moves the node into spec.OneBucket with the host-built
// write:one body, reads it back, then restores the original bucket.
func (r *runner) facetWriteOne(ctx context.Context, spec *FacetWriteSpec) {
	node, write, problem := r.facetWriteSubject(
		ctx, spec, spec.OneDimension, cutting_garden_plugins.FacetWriteOne,
	)
	if problem != "" {
		r.tap.NotOk(nameFacetWriteOne, map[string]string{"setup": problem})
		return
	}

	original := facetKeys(node.Facets[spec.OneDimension])

	body, err := traversal_serve.HostFacetWritePatch(write, spec.OneBucket)
	if err != nil {
		r.tap.NotOk(nameFacetWriteOne, map[string]string{"manifest": err.Error()})
		return
	}

	problems := r.patchAndReadBack(
		ctx, spec, body, spec.OneDimension, []string{spec.OneBucket},
	)

	if len(original) == 1 && original[0] != spec.OneBucket {
		restore, _ := traversal_serve.HostFacetWritePatch(write, original[0])
		if _, _, err := r.patchRaw(ctx, spec.Node, string(restore)); err != nil {
			problems["restore"] = err.Error()
		}
	}

	r.verdict(nameFacetWriteOne, problems)
}

// facetWriteMany replaces the node's set with spec.ManySet, then clears it
// with [], reading each back, then restores the original set.
func (r *runner) facetWriteMany(ctx context.Context, spec *FacetWriteSpec) {
	node, write, problem := r.facetWriteSubject(
		ctx, spec, spec.ManyDimension, cutting_garden_plugins.FacetWriteMany,
	)
	if problem != "" {
		r.tap.NotOk(nameFacetWriteMany, map[string]string{"setup": problem})
		return
	}

	original := facetKeys(node.Facets[spec.ManyDimension])
	problems := map[string]string{}

	for _, set := range [][]string{spec.ManySet, {}} {
		body, err := traversal_serve.HostMembershipWritePatch(write, set)
		if err != nil {
			problems["build"] = err.Error()
			break
		}
		for key, value := range r.patchAndReadBack(
			ctx, spec, body, spec.ManyDimension, set,
		) {
			problems[fmt.Sprintf("%s (set %v)", key, set)] = value
		}
	}

	restore, _ := traversal_serve.HostMembershipWritePatch(write, original)
	if _, _, err := r.patchRaw(ctx, spec.Node, string(restore)); err != nil {
		problems["restore"] = err.Error()
	}

	r.verdict(nameFacetWriteMany, problems)
}

// facetWriteSubject lists the node's container to find the node (its type
// and current facets) and resolves the peer's declared mapping for
// dimension on that type, which must have mode want. problem is non-empty
// when either lookup fails.
func (r *runner) facetWriteSubject(
	ctx context.Context,
	spec *FacetWriteSpec,
	dimension string,
	want cutting_garden_plugins.FacetWriteMode,
) (cutting_garden_plugins.Node, cutting_garden_plugins.FacetWrite, string) {
	node, ok := r.findListedNode(ctx, spec)
	if !ok {
		return node, cutting_garden_plugins.FacetWrite{}, fmt.Sprintf(
			"%s is not among nodes.list %s", spec.Node, spec.Container,
		)
	}

	for _, view := range r.init.FacetWrites {
		declared := view.ToNodeTypeFacetWrites()
		if declared.Tag != node.Type {
			continue
		}
		for _, write := range declared.Writes {
			if write.DimensionKey == dimension && write.Mode == want {
				return node, write, ""
			}
		}
	}

	return node, cutting_garden_plugins.FacetWrite{}, fmt.Sprintf(
		"type %q declares no mode %q write for dimension %q",
		node.Type, want, dimension,
	)
}

// patchAndReadBack sends body through node.patch and asserts the node's
// dimension now holds exactly want (order-insensitive).
func (r *runner) patchAndReadBack(
	ctx context.Context,
	spec *FacetWriteSpec,
	body []byte,
	dimension string,
	want []string,
) map[string]string {
	problems := map[string]string{}

	if _, _, err := r.patchRaw(ctx, spec.Node, string(body)); err != nil {
		problems["node.patch"] = fmt.Sprintf("%s: %s", body, err)
		return problems
	}

	node, ok := r.findListedNode(ctx, spec)
	if !ok {
		problems["read-back"] = spec.Node + " vanished from its container"
		return problems
	}

	got := facetKeys(node.Facets[dimension])
	if !sameStringSet(got, want) {
		problems["read-back"] = fmt.Sprintf(
			"after %s, %s = [%s], want [%s]",
			body, dimension, strings.Join(got, ","), strings.Join(want, ","),
		)
	}

	return problems
}

// findListedNode lists spec.Container and returns spec.Node's entry.
func (r *runner) findListedNode(
	ctx context.Context, spec *FacetWriteSpec,
) (cutting_garden_plugins.Node, bool) {
	nodes, ok := r.listNodesDecoded(ctx, spec.Container, nil)
	if !ok {
		return cutting_garden_plugins.Node{}, false
	}

	index := slices.IndexFunc(nodes, func(node cutting_garden_plugins.Node) bool {
		return node.URIString() == spec.Node
	})
	if index < 0 {
		return cutting_garden_plugins.Node{}, false
	}

	return nodes[index], true
}

func (r *runner) verdict(name string, problems map[string]string) {
	if len(problems) > 0 {
		r.tap.NotOk(name, problems)
		return
	}

	r.tap.Ok(name)
}

func facetKeys(values []cutting_garden_plugins.FacetValue) []string {
	keys := make([]string, len(values))
	for i, value := range values {
		keys[i] = value.Key
	}

	return keys
}
