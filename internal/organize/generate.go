package organize

import (
	"fmt"
	"io"
	"strings"

	"code.linenisgreat.com/cutting-garden/internal/command_components"
	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/plugin_blob_io"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// buildAndStore selects the anchor's nodes, builds the organize document, stores
// its canonical form as an organize-base-v1 blob, and returns the emitted form
// (with the `- _base` pin) so a later apply three-way-merges the edits against
// the exact pre-edit state. Shared by the stdout (runGenerate) and interactive
// (runInteractive) paths.
func (cmd *Organize) buildAndStore(ctx errors.Context, uriStr string) (string, error) {
	if cmd.GroupBy == "" {
		return "", errors.BadRequestf("organize <uri> requires --group-by <facet-key>")
	}

	u, lister, err := command_components.ResolveRootListerPlugin(uriStr)
	if err != nil {
		return "", err
	}

	nodes, err := selectNodes(ctx, lister, u, cmd.Query)
	if err != nil {
		return "", errors.Wrapf(err, "organize %s", uriStr)
	}

	// Anchor the document at the selected nodes' common URI prefix rather than
	// the raw CLI arg, so box ids shorten regardless of the arg form (a
	// `caldav:<name>` alias, or the `caldav:https://` vs `caldav://` spelling) —
	// the prefix is itself a valid re-query anchor (the calendar for a
	// single-calendar query, the home for a multi). Fall back to the arg when
	// there is no common prefix (e.g. zero nodes).
	anchor := commonURIPrefix(nodes)
	if anchor == "" {
		anchor = uriStr
	}

	doc := buildDocument(nodes, anchor, cmd.Query, cmd.GroupBy, lister)
	// Provenance records what the user actually typed (e.g. the short alias),
	// even though _anchor is the canonical common prefix.
	doc.Provenance = provenance(cmd.GroupBy, cmd.Query, uriStr)

	// The canonical form (no `_base`) is the exact bytes hashed and stored; its
	// digest becomes the pin the emitted form carries.
	digest, err := cmd.storeBase(ctx, renderCanonical(doc))
	if err != nil {
		return "", err
	}
	doc.BaseDigest = digest

	return render(doc), nil
}

// runGenerate builds the document and prints the emitted form to stdout — the
// non-interactive path (a pipe/redirect, or an MCP/scripting consumer).
func (cmd *Organize) runGenerate(ctx errors.Context, uriStr string) error {
	rendered, err := cmd.buildAndStore(ctx, uriStr)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(cmd.output, rendered); err != nil {
		return errors.Wrap(err)
	}
	return nil
}

// buildDocument assembles the organize document from the selected nodes. A
// single-type node set uses the flatter envelope-`_type` spelling (spelling 2);
// a multi-type set uses per-type `# !<type>` headings (spelling 1). The grouped
// dimension renders as a `<dim>=` heading with a `=<value>` bucket per declared /
// observed value.
func buildDocument(
	nodes []cgp.Node, anchor, query, groupBy string, lister cgp.RootLister,
) document {
	doc := document{
		Anchor:     anchor,
		Query:      query,
		Provenance: provenance(groupBy, query, anchor),
	}

	present := boxAtomPresenter(lister)
	types := distinctTypes(nodes)
	switch len(types) {
	case 1:
		// Spelling 2: type in the envelope, object boxes bare, dimension at depth 1.
		doc.Type = types[0]
		declared := writableBuckets(lister, types[0], groupBy)
		ungrouped, buckets := groupNodes(nodes, groupBy, anchor, declared, false, present)
		doc.Ungrouped = ungrouped
		doc.Sections = dimensionSections(groupBy, buckets, 1)
	default:
		// Spelling 1: a `# !<type>` heading per type, each with its own dimension
		// ladder at depth 2; object boxes carry inline `!type`.
		for _, typ := range types {
			typeNodes := nodesOfType(nodes, typ)
			declared := writableBuckets(lister, typ, groupBy)
			ungrouped, buckets := groupNodes(typeNodes, groupBy, anchor, declared, true, present)
			doc.Sections = append(doc.Sections, section{Depth: 1, Term: "!" + typ, Lines: ungrouped})
			doc.Sections = append(doc.Sections, dimensionSections(groupBy, buckets, 2)...)
		}
	}
	return doc
}

// boxAtomPresenter returns the plugin's box-atom presentation function when it
// implements FieldPresenter (cutting-garden#47), or nil — in which case object
// boxes carry no detail atoms (today's behavior for a plugin without the
// capability).
func boxAtomPresenter(lister cgp.RootLister) func(cgp.Node) []cgp.BoxAtom {
	if p, ok := lister.(cgp.FieldPresenter); ok {
		return p.PresentBoxAtoms
	}
	return nil
}

// dimensionSections renders the grouped dimension as a `<dim>=` heading at
// baseDepth followed by a `=<value>` bucket heading (baseDepth+1) per bucket.
func dimensionSections(groupBy string, buckets []bucket, baseDepth int) []section {
	secs := make([]section, 0, len(buckets)+1)
	secs = append(secs, section{Depth: baseDepth, Term: groupBy + "="})
	for _, bk := range buckets {
		secs = append(secs, section{Depth: baseDepth + 1, Term: "=" + bk.Value, Lines: bk.Lines})
	}
	return secs
}

// writableBuckets returns the plugin's declared target buckets for the grouped
// dimension on node type tag (FacetWrite.Values) — the values organize
// pre-renders as empty headings. nil for a plugin without the write capability or
// one declaring no values for the dimension.
func writableBuckets(lister cgp.RootLister, tag, dim string) []string {
	describer, ok := lister.(cgp.FacetWriteDescriber)
	if !ok {
		return nil
	}
	for _, nt := range describer.DescribeFacetWrites() {
		if nt.Tag != tag {
			continue
		}
		for _, w := range nt.Writes {
			if w.DimensionKey == dim {
				return w.Values
			}
		}
	}
	return nil
}

// commonURIPrefix returns the longest common prefix of the nodes' URIs, trimmed
// back to the last '/' so it ends at a path boundary — the calendar URL for a
// single-calendar node set, the home for a multi-calendar one. Empty for zero
// nodes or nodes with no shared path boundary.
func commonURIPrefix(nodes []cgp.Node) string {
	if len(nodes) == 0 {
		return ""
	}
	prefix := nodes[0].URIString()
	for _, n := range nodes[1:] {
		prefix = commonStringPrefix(prefix, n.URIString())
		if prefix == "" {
			return ""
		}
	}
	if i := strings.LastIndexByte(prefix, '/'); i >= 0 {
		return prefix[:i+1]
	}
	return ""
}

// commonStringPrefix returns the longest byte-prefix shared by a and b.
func commonStringPrefix(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return a[:i]
}

// provenance renders the inert `%` provenance note recording how the document was
// generated.
func provenance(groupBy, query, uri string) string {
	if query != "" {
		return fmt.Sprintf("generated: cg organize -group-by %s -query %q %s", groupBy, query, uri)
	}
	return fmt.Sprintf("generated: cg organize -group-by %s %s", groupBy, uri)
}

// storeBase writes the canonical document as a content-addressed blob and returns
// the bare digest to pin. Content addressing makes the base tamper-evident: a
// later --apply reads back exactly what was generated.
func (cmd *Organize) storeBase(ctx errors.Context, canonical string) (string, error) {
	store := command_components.MakeBlobStoreEnv(ctx).GetDefaultBlobStore()
	id, _, err := plugin_blob_io.WriteReaderBlob(ctx, store, strings.NewReader(canonical))
	if err != nil {
		return "", errors.Wrapf(err, "organize: store base blob")
	}
	return id.String(), nil
}
