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

	// Resolve the group-by spelling ONCE, at generate time (cutting-garden#230):
	// a bare date dimension takes the `[organize] date_granularity` config
	// default, then day, and the resolved spelling is persisted in the
	// document's dimension heading — so a later --apply never consults config
	// (which may change in between). The config was already loaded and warned
	// about by Run's LoadAndInjectConfig; this re-read just fetches the value.
	cfg, err := command_components.LoadDefaultConfig(nil)
	if err != nil {
		return "", err
	}
	spec, err := parseGroupSpec(
		cmd.GroupBy, describedFacets(lister), describedTagDims(lister),
		cfg.Organize.DateGranularity,
	)
	if err != nil {
		return "", err
	}

	// A namespace grouping needs a tag interpreter that declares namespaces
	// (RFC 0019 tags slice 3): resolve it from the field default + the global
	// [tags] override, then reject the naive (exact-match) interpreter up front
	// with a clear message rather than surfacing the interpreter's raw "declares
	// no namespaces". A whole-dimension or field grouping needs no interpreter.
	var interp cgp.TagInterpreter
	if spec.Kind == groupKindTagNamespace {
		var interpName string
		interp, interpName, err = interpreterForDimension(lister, spec.Dim, cfg.Tags.Interpreter)
		if err != nil {
			return "", err
		}
		if err := requireNamespaceInterpreter(interp, interpName, cmd.GroupBy, spec); err != nil {
			return "", err
		}
	}

	// The effective query is the user's query with organize's default
	// `_terminal=no` exclusion composed in (cutting-garden#214) — echoed into the
	// document's `_query` below so the default is visible, editable, and re-applies
	// identically.
	effective := effectiveQuery(lister, cmd.Query, cmd.IncludeTerminal)
	nodes, err := selectNodes(ctx, lister, u, effective)
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

	doc, err := buildDocument(nodes, anchor, effective, spec, lister, interp)
	if err != nil {
		return "", err
	}
	// Provenance records what the user actually typed for the URI (e.g. the
	// short alias), even though _anchor is the canonical common prefix — but
	// echoes the RESOLVED group-by spelling, so a config-defaulted granularity
	// is visible.
	doc.Provenance = provenance(spec.String(), effective, uriStr)

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
// a multi-type set uses per-type `# !<type>` headings (spelling 1). A FIELD
// grouping renders the grouped dimension as a `<spec>=` heading (the full
// `dim:granularity` spelling for a date grouping, cutting-garden#230) with a
// `=<value>` bucket per declared / observed value. A TAG grouping (RFC 0019 tags
// slice 3 B3) is hoisted: no parent dimension heading, its spec recorded in the
// `_group-by` envelope directive, and its buckets bare `## <value>` headings.
// interp is the resolved tag interpreter — required for a namespace grouping
// (groupKindTagNamespace), nil otherwise.
func buildDocument(
	nodes []cgp.Node, anchor, query string, spec groupSpec, lister cgp.RootLister,
	interp cgp.TagInterpreter,
) (document, error) {
	doc := document{
		Anchor:     anchor,
		Query:      query,
		Provenance: provenance(spec.String(), query, anchor),
		GroupBy:    spec.groupByEncoding(),
	}

	present := boxAtomPresenter(lister)
	types := distinctTypes(nodes)
	switch len(types) {
	case 1:
		// Spelling 2: type in the envelope, object boxes bare, buckets at depth 1
		// (a field dimension heading at depth 1 with buckets at depth 2).
		doc.Type = types[0]
		declared := writableBuckets(lister, types[0], spec.Dim)
		ungrouped, buckets, err := groupForSpec(nodes, spec, anchor, declared, false, present, interp)
		if err != nil {
			return document{}, err
		}
		doc.Ungrouped = ungrouped
		doc.Sections = sectionsForSpec(spec, buckets, 1)
	default:
		// Spelling 1: a `# !<type>` heading per type, each with its own bucket
		// ladder one level deeper; object boxes carry inline `!type`.
		for _, typ := range types {
			typeNodes := nodesOfType(nodes, typ)
			declared := writableBuckets(lister, typ, spec.Dim)
			ungrouped, buckets, err := groupForSpec(typeNodes, spec, anchor, declared, true, present, interp)
			if err != nil {
				return document{}, err
			}
			doc.Sections = append(doc.Sections, section{Depth: 1, Term: "!" + typ, Lines: ungrouped})
			doc.Sections = append(doc.Sections, sectionsForSpec(spec, buckets, 2)...)
		}
	}
	return doc, nil
}

// groupForSpec buckets nodes for the grouping dialect the spec selects: a
// namespace grouping folds through the resolved interpreter's rollup
// (groupNodesByNamespace, B2); a whole-dimension tag grouping and a field
// grouping both bucket by raw facet value (groupNodes) — the difference is only
// in how the buckets RENDER (sectionsForSpec), not how nodes bucket.
func groupForSpec(
	nodes []cgp.Node, spec groupSpec, anchor string, declared []string,
	inlineType bool, present func(cgp.Node) []cgp.BoxAtom, interp cgp.TagInterpreter,
) (ungrouped []objectLine, buckets []bucket, err error) {
	if spec.Kind == groupKindTagNamespace {
		return groupNodesByNamespace(nodes, spec, anchor, interp, inlineType, present)
	}
	ungrouped, buckets = groupNodes(nodes, spec, anchor, declared, inlineType, present)
	return ungrouped, buckets, nil
}

// sectionsForSpec renders a grouping's buckets in the dialect the spec selects: a
// FIELD grouping keeps the `<spec>=` heading + `## =<value>` buckets
// (dimensionSections); a TAG grouping is hoisted to bare `## <value>` buckets
// with no parent heading (tagDimensionSections).
func sectionsForSpec(spec groupSpec, buckets []bucket, baseDepth int) []section {
	if spec.Kind == groupKindField {
		return dimensionSections(spec, buckets, baseDepth)
	}
	return tagDimensionSections(buckets, baseDepth)
}

// describedFacets probes the plugin's declared facet schema, nil for a plugin
// without the FacetDescriber capability — a granularity suffix then rejects,
// since no schema says the dimension is a date.
func describedFacets(lister cgp.RootLister) []cgp.NodeTypeFacets {
	if d, ok := lister.(cgp.FacetDescriber); ok {
		return d.DescribeFacets()
	}
	return nil
}

// requireNamespaceInterpreter rejects a namespace grouping whose resolved
// interpreter declares no namespaces — the naive (exact-match) interpreter —
// with a clear, actionable message naming the interpreter and pointing at the
// [tags] config, rather than surfacing the interpreter's raw "declares no
// namespaces". A capable interpreter (dodder-hyphen) probes clean and returns
// nil. The probe uses an empty tag set: naive rejects any non-empty namespace
// regardless of tags, dodder-hyphen returns an empty membership set. Removes the
// need for buildDocument to distinguish the naive error downstream (RFC 0019
// tags slice 3 B3, cutting-garden#231).
func requireNamespaceInterpreter(
	interp cgp.TagInterpreter, interpName, groupBy string, spec groupSpec,
) error {
	if _, err := interp.Buckets(nil, spec.Namespace); err != nil {
		return errors.BadRequestf(
			"organize: namespace grouping (--group-by %s) needs a tag interpreter "+
				"that declares namespaces, but dimension %q uses the %q interpreter; "+
				"set [tags] interpreter = dodder-hyphen",
			groupBy, spec.Dim, interpName,
		)
	}
	return nil
}

// describedTagDims collects the plugin's TAG-dimension keys — the
// UnifiedField.Kind == FieldTag fields declared via DescribeUnified (FDR 0025).
// The FacetDimension surface derives a tag field to FacetCategorical
// (facet_derive), so the unified declaration is the ONLY place a tag dimension
// is distinguishable from a plain categorical one; a plugin without the
// UnifiedDescriber capability has no tag dimensions. Deduplicated,
// first-declared order — parseGroupSpec resolves an unqualified namespace arg
// against the first.
func describedTagDims(lister cgp.RootLister) []string {
	d, ok := lister.(cgp.UnifiedDescriber)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var keys []string
	for _, set := range d.DescribeUnified() {
		for _, codec := range set.Codecs {
			for _, f := range codec.Fields() {
				if f.Kind == cgp.FieldTag && !seen[f.Key] {
					seen[f.Key] = true
					keys = append(keys, f.Key)
				}
			}
		}
	}
	return keys
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

// dimensionSections renders the grouped dimension as a `<spec>=` heading at
// baseDepth followed by a `=<value>` bucket heading (baseDepth+1) per bucket.
// The heading term carries the FULL spec spelling (`date_due:month=`) — the
// persisted granularity a later apply recovers via groupedSpec (#230).
func dimensionSections(spec groupSpec, buckets []bucket, baseDepth int) []section {
	secs := make([]section, 0, len(buckets)+1)
	secs = append(secs, section{Depth: baseDepth, Term: spec.String() + "="})
	for _, bk := range buckets {
		secs = append(secs, section{Depth: baseDepth + 1, Term: "=" + bk.Value, Lines: bk.Lines})
	}
	return secs
}

// tagDimensionSections renders a TAG grouping's buckets in the hoisted dialect
// (RFC 0019 tags slice 3 B3): a bare `## <value>` heading per bucket with NO
// parent dimension heading (the spec lives in the `_group-by` envelope directive)
// and NO `=` value prefix. The buckets sit at baseDepth+1 — the SAME depth a
// field grouping's `## =<value>` buckets occupy (dimensionSections) — so the
// hoisting only elides the parent heading, it does not shift the buckets up:
// spelling 2 renders `## work`, spelling 1 `### work` under its `# !<type>`. A
// value containing whitespace is quoted (`## "_ inbox"`); the parser unquotes.
// The bucket value already IS the tag (`work`) or the namespace-rollup segment
// (`-client`) from groupForSpec.
func tagDimensionSections(buckets []bucket, baseDepth int) []section {
	secs := make([]section, 0, len(buckets))
	for _, bk := range buckets {
		secs = append(secs, section{
			Depth: baseDepth + 1, Term: quoteHeadingValue(bk.Value), Lines: bk.Lines,
		})
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
// generated. The echoed command is wrapped in backticks so it reads as code and
// copy-pastes unambiguously (cutting-garden#243).
func provenance(groupBy, query, uri string) string {
	if query != "" {
		return fmt.Sprintf("generated: `cg organize -group-by %s -query %q %s`", groupBy, query, uri)
	}
	return fmt.Sprintf("generated: `cg organize -group-by %s %s`", groupBy, uri)
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
