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

// runGenerate selects the anchor's nodes, builds the organize document, stores
// its canonical form as an organize-base-v1 blob, and prints the emitted form
// (with the `- _base` pin) so a later --apply three-way-merges the edits against
// the exact pre-edit state.
func (cmd *Organize) runGenerate(ctx errors.Context, uriStr string) error {
	if cmd.GroupBy == "" {
		return errors.BadRequestf("organize <uri> requires --group-by <facet-key>")
	}

	u, lister, err := command_components.ResolveRootListerPlugin(uriStr)
	if err != nil {
		return err
	}

	nodes, err := selectNodes(ctx, lister, u, cmd.Query)
	if err != nil {
		return errors.Wrapf(err, "organize %s", uriStr)
	}

	doc := buildDocument(nodes, uriStr, cmd.Query, cmd.GroupBy, lister)

	// The canonical form (no `_base`) is the exact bytes hashed and stored; its
	// digest becomes the pin the emitted form carries.
	digest, err := cmd.storeBase(ctx, renderCanonical(doc))
	if err != nil {
		return err
	}
	doc.BaseDigest = digest

	if _, err := io.WriteString(cmd.output, render(doc)); err != nil {
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

	types := distinctTypes(nodes)
	switch len(types) {
	case 1:
		// Spelling 2: type in the envelope, object boxes bare, dimension at depth 1.
		doc.Type = types[0]
		declared := writableBuckets(lister, types[0], groupBy)
		ungrouped, buckets := groupNodes(nodes, groupBy, anchor, declared, false)
		doc.Ungrouped = ungrouped
		doc.Sections = dimensionSections(groupBy, buckets, 1)
	default:
		// Spelling 1: a `# !<type>` heading per type, each with its own dimension
		// ladder at depth 2; object boxes carry inline `!type`.
		for _, typ := range types {
			typeNodes := nodesOfType(nodes, typ)
			declared := writableBuckets(lister, typ, groupBy)
			ungrouped, buckets := groupNodes(typeNodes, groupBy, anchor, declared, true)
			doc.Sections = append(doc.Sections, section{Depth: 1, Term: "!" + typ, Lines: ungrouped})
			doc.Sections = append(doc.Sections, dimensionSections(groupBy, buckets, 2)...)
		}
	}
	return doc
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
