package organize

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"code.linenisgreat.com/cutting-garden/internal/command_components"
	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/plugin_blob_io"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// buildAndStore selects the anchor's nodes, builds the organize document, stores
// its canonical form as an organize-base-v1 blob, and returns the emitted form
// (with the `- _base` pin) so a later apply three-way-merges the edits against
// the exact pre-edit state. Shared by the stdout (runGenerate) and interactive
// (runInteractive) paths.
func (cmd *Organize) buildAndStore(ctx errors.Context, uriStr string) (string, error) {
	if cmd.GroupBy == "" {
		return "", errors.BadRequestf(
			"organize <uri> requires --group-by: `(tags)`, a tag namespace (`project`), " +
				"a field (`status=`), or a date field at a granularity (`date_due=(month)`)",
		)
	}

	u, lister, err := command_components.ResolveRootListerPlugin(uriStr)
	if err != nil {
		return "", err
	}

	// Resolve the group-by spelling ONCE, at generate time (cutting-garden#230):
	// a bare `<dim>=` on a date dimension takes the `[organize] date_granularity`
	// config default, then day, and the resolved spelling is persisted in the
	// document's dimension heading (`# date_due=(month)`) — so a later --apply
	// never consults config (which may change in between). The config was
	// already loaded and warned about by Run's LoadAndInjectConfig; this re-read
	// just fetches the value.
	cfg, err := command_components.LoadDefaultConfig(nil)
	if err != nil {
		return "", err
	}
	// The unified declaration's cross-codec invariants (a second FieldTag field
	// per type, G6 v1) are checked ONCE here — generate's resolution point, the
	// first consumer that reads THE designated tag field — so a bad declaration
	// fails the command loudly instead of PresentUnifiedTags silently picking
	// the first.
	if err := validateUnifiedDeclaration(lister); err != nil {
		return "", err
	}
	dims := describedFacets(lister)
	tagDims := describedTagDims(lister)
	spec, err := parseGroupSpec(cmd.GroupBy, dims, tagDims, cfg.Organize.DateGranularity)
	if err != nil {
		return "", err
	}

	// The tag interpreter serves two jobs here: a NAMESPACE grouping's rollup
	// (RFC 0019 tags slice 3 — reject the naive interpreter up front with a
	// clear message rather than its raw "declares no namespaces"), and the
	// SortKey ordering of every rendered tag set (design G1, slice 2). So it is
	// resolved whenever the plugin declares a tag dimension at all, from the
	// field's declared default + the global [tags] override.
	var interp cgp.TagInterpreter
	if len(tagDims) > 0 {
		var interpName string
		interp, interpName, err = interpreterForDimension(lister, tagDims[0], cfg.Tags.Interpreter)
		if err != nil {
			return "", err
		}
		if spec.Kind == groupKindTagNamespace {
			if err := requireNamespaceInterpreter(interp, interpName, cmd.GroupBy, spec); err != nil {
				return "", err
			}
		}
	}

	// The tag-atom levers (design G1/G2/G3): resolved from config at generate
	// (there is no document yet, so the doc-wins half of effectiveTagAtoms is
	// apply's) and persisted as envelope fields ONLY when non-default, so
	// default documents stay byte-identical.
	tagAtoms := effectiveTagAtoms("", cfg.Organize.TagAtoms)
	tagStrip := effectiveTagStrip("", cfg.Organize.TagStrip)
	tags := tagRender{strip: tagStrip == tagStripPlacement}
	if tagAtoms != tagAtomsNone {
		tags.present = unifiedTagPresenter(lister, interp)
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

	doc, err := buildDocument(nodes, anchor, effective, spec, lister, interp, tags)
	if err != nil {
		return "", err
	}
	if err := rejectEmptyNamespace(spec, doc, dims); err != nil {
		return "", err
	}
	// Non-default levers are DATA-plane envelope fields (design G3): they reach
	// the canonical base below, so `_base` content-addresses them.
	if tagAtoms != tagAtomsLeading {
		doc.TagAtoms = tagAtoms
	}
	if tagStrip != tagStripPlacement {
		doc.TagStrip = tagStrip
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
// grouping renders the grouped dimension as its spelling heading (`# status=`,
// or `# date_due=(month)` for a date grouping, cutting-garden#230) with a
// `=<value>` bucket per declared / observed value. A TAG grouping (design G10)
// is hoisted: no parent dimension heading, its spelling recorded in the
// `_group-by` envelope directive, and its buckets bare `# <value>` headings at
// minimal depth.
// interp is the resolved tag interpreter — required for a namespace grouping
// (groupKindTagNamespace), nil otherwise. tags is the tag-atom render view
// (design G1/G2, slice 2); the zero value renders no tag atoms.
func buildDocument(
	nodes []cgp.Node, anchor, query string, spec groupSpec, lister cgp.RootLister,
	interp cgp.TagInterpreter, tags tagRender,
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
		tags.fill(nodes, anchor, spec, interp, ungrouped, buckets)
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
			tags.fill(typeNodes, anchor, spec, interp, ungrouped, buckets)
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
// (dimensionSections); a whole-dimension TAG grouping is hoisted to bare
// `# <value>` buckets with no parent heading (tagDimensionSections); a namespace
// grouping renders the G10a root-heading ladder (namespaceSections).
func sectionsForSpec(spec groupSpec, buckets []bucket, baseDepth int) []section {
	switch spec.Kind {
	case groupKindField:
		return dimensionSections(spec, buckets, baseDepth)
	case groupKindTagNamespace:
		return namespaceSections(buckets, baseDepth)
	default:
		return tagDimensionSections(buckets, baseDepth)
	}
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
// capability). Atom values pass through collapseToSingleLine: a stored TEXT
// value (e.g. a caldav LOCATION) may carry real newlines, but an atom lives
// inside one document line — same presentation-only rule as the description
// trailer (native tags slice 1.5 F; see collapseToSingleLine).
func boxAtomPresenter(lister cgp.RootLister) func(cgp.Node) []cgp.BoxAtom {
	p, ok := lister.(cgp.FieldPresenter)
	if !ok {
		return nil
	}
	return func(n cgp.Node) []cgp.BoxAtom {
		// Copy before collapsing: the plugin may hand back a cached slice, and
		// mutating it in place would corrupt the plugin's own state.
		atoms := slices.Clone(p.PresentBoxAtoms(n))
		for i := range atoms {
			atoms[i].Value = collapseToSingleLine(atoms[i].Value)
		}
		return atoms
	}
}

// tagRender is generate's tag-atom view (native tags design G1/G2, slice 2):
// present resolves a node's rendered tag set (SortKey-ordered; nil renders no
// tag atoms — a plugin without a tag dimension, or `_tag-atoms = none`), and
// strip applies the `_tag-strip = placement` rule to tag-grouped buckets.
type tagRender struct {
	present func(cgp.Node) []string
	strip   bool
}

// fill populates each object line's Tags from the presented tag sets, keyed by
// the same relativeID the lines were built with. Under a TAG grouping with
// strip on, each bucket appearance drops exactly the placement tag(s) that
// filed it there (placementVia — the membership's Via reconstruction); every
// other tag stays, and ungrouped lines always keep their full set. A FIELD
// grouping strips nothing (no tag placement exists). Mutation is safe: each
// line is a value copy in its own slice (a multi-membership object's line was
// COPIED into every bucket), so per-appearance Tags never alias.
func (tr tagRender) fill(
	nodes []cgp.Node, anchor string, spec groupSpec, interp cgp.TagInterpreter,
	ungrouped []objectLine, buckets []bucket,
) {
	if tr.present == nil {
		return
	}
	byID := make(map[string][]string, len(nodes))
	for _, n := range nodes {
		if ts := tr.present(n); len(ts) > 0 {
			byID[relativeID(n.URIString(), anchor)] = ts
		}
	}
	for i := range ungrouped {
		ungrouped[i].Tags = byID[ungrouped[i].ID]
	}
	for bi := range buckets {
		for li := range buckets[bi].Lines {
			ln := &buckets[bi].Lines[li]
			ts := byID[ln.ID]
			if tr.strip {
				ts = withoutPlacementTags(ts, spec, interp, buckets[bi].Value)
			}
			ln.Tags = ts
		}
	}
}

// withoutPlacementTags returns tags minus every tag whose placement under spec
// IS bucketValue (placementVia); the input slice is returned untouched when
// nothing strips, and nil when everything does.
func withoutPlacementTags(
	tags []string, spec groupSpec, interp cgp.TagInterpreter, bucketValue string,
) []string {
	strip := false
	for _, t := range tags {
		if placementVia(t, bucketValue, spec, interp) {
			strip = true
			break
		}
	}
	if !strip {
		return tags
	}
	var out []string
	for _, t := range tags {
		if !placementVia(t, bucketValue, spec, interp) {
			out = append(out, t)
		}
	}
	return out
}

// placementVia reports whether tag PRODUCES the bucketValue placement under
// the spec's grouping — the strip rule's (and the apply gate's) one derivation
// (design G2): under a whole-dimension grouping a tag's bucket is itself
// (Via == Bucket); under a namespace grouping the ROOT bucket (== the
// namespace) is produced by the bare namespace tag (G10a) and a continuation
// bucket by any tag the interpreter rolls up to it (all contributors — the
// interpreter's Membership.Via names one representative, but every tag rolling
// to the bucket produced the placement, and the §6.2 write-back removes the
// whole subtree symmetrically). A field grouping has no tag placement, so
// nothing is ever a Via there.
func placementVia(tag, bucketValue string, spec groupSpec, interp cgp.TagInterpreter) bool {
	switch spec.Kind {
	case groupKindTagWhole:
		return tag == bucketValue
	case groupKindTagNamespace:
		if bucketValue == spec.Namespace {
			return tag == spec.Namespace
		}
		if interp == nil {
			return false
		}
		ms, err := interp.Buckets([]string{tag}, spec.Namespace)
		return err == nil && len(ms) == 1 && ms[0].Bucket == bucketValue
	default:
		return false
	}
}

// unifiedTagPresenter returns the node → rendered-tag-set function (design G1):
// the type's designated FieldTag field's values (PresentUnifiedTags, stored
// order), ordered by the resolved interpreter's SortKey. nil for a plugin
// without the UnifiedDescriber capability — no tag dimension, no tag atoms.
// The presented slice is cloned before sorting: the codec's Format output must
// never be reordered in place (it may alias plugin state).
func unifiedTagPresenter(
	lister cgp.RootLister, interp cgp.TagInterpreter,
) func(cgp.Node) []string {
	d, ok := lister.(cgp.UnifiedDescriber)
	if !ok {
		return nil
	}
	byType := map[string][]cgp.Codec{}
	for _, set := range d.DescribeUnified() {
		byType[set.Tag] = set.Codecs
	}
	return func(n cgp.Node) []string {
		tags := slices.Clone(cgp.PresentUnifiedTags(byType[n.Type], n))
		if interp != nil {
			sort.SliceStable(tags, func(i, j int) bool {
				return interp.SortKey(tags[i]) < interp.SortKey(tags[j])
			})
		}
		return tags
	}
}

// validateUnifiedDeclaration checks a plugin's unified field declaration's
// cross-codec invariants (ValidateUnifiedFieldSets: at most one FieldTag field
// per type, G6 v1) — wired at generate's resolution point so a bad declaration
// fails the command loudly. A plugin without the capability has nothing to
// validate.
func validateUnifiedDeclaration(lister cgp.RootLister) error {
	d, ok := lister.(cgp.UnifiedDescriber)
	if !ok {
		return nil
	}
	return cgp.ValidateUnifiedFieldSets(d.DescribeUnified())
}

// dimensionSections renders the grouped dimension as its spelling heading at
// baseDepth followed by a `=<value>` bucket heading (baseDepth+1) per bucket.
// The heading term IS the spec spelling (`status=`, `date_due=(month)`) — the
// persisted granularity a later apply recovers via groupedSpec (#230).
func dimensionSections(spec groupSpec, buckets []bucket, baseDepth int) []section {
	secs := make([]section, 0, len(buckets)+1)
	secs = append(secs, section{Depth: baseDepth, Term: spec.String()})
	for _, bk := range buckets {
		secs = append(secs, section{
			Depth: baseDepth + 1, Term: "=" + trellis.QuoteIfNeeded(bk.Value), Lines: bk.Lines,
		})
	}
	return secs
}

// tagDimensionSections renders a TAG grouping's buckets in the hoisted dialect
// (design G10): a bare `# <value>` heading per bucket with NO parent dimension
// heading (the spec lives in the `_group-by` envelope directive) and NO `=`
// value prefix. Heading depth is MINIMAL: with no dimension heading to nest
// under, the buckets sit AT baseDepth — spelling 2 renders `# work` / `# -client`,
// spelling 1 `## work` under its `# !<type>` — while a field grouping keeps its
// `# <dim>=` / `## =<value>` ladder (dimensionSections). The parser normalizes
// depth (parseBody), so a document at either depth reads the same. A value
// containing whitespace or a reserved rune is quoted as a trellis String
// (`# "_ inbox"`, design G9); the parser unquotes. The bucket value already IS
// the tag (`work`) or the namespace-rollup segment (`-client`) from groupForSpec.
func tagDimensionSections(buckets []bucket, baseDepth int) []section {
	secs := make([]section, 0, len(buckets))
	for _, bk := range buckets {
		secs = append(secs, section{
			Depth: baseDepth, Term: trellis.QuoteIfNeeded(bk.Value), Lines: bk.Lines,
		})
	}
	return secs
}

// namespaceSections renders a namespace grouping's G10a ladder: the namespace
// ROOT as a real top-level tag heading with the rollup continuations nested one
// deeper — `# project` / `## -client` / `## -cutting_garden` — the ladder IS the
// tag hierarchy. buckets[0] is the root bucket groupNodesByNamespace always
// synthesizes (Value == the namespace, Lines the objects carrying the BARE
// namespace tag, which render directly under the root heading); the
// continuations follow at baseDepth+1. Like the whole-dimension dialect the
// grouping's spelling lives in the `_group-by` envelope directive, values quote
// as trellis Strings when needed, and depth stays minimal for spelling 2
// (baseDepth 1) while spelling 1 nests the whole ladder one deeper under its
// `# !<type>`.
func namespaceSections(buckets []bucket, baseDepth int) []section {
	secs := make([]section, 0, len(buckets))
	for i, bk := range buckets {
		depth := baseDepth
		if i > 0 {
			depth++
		}
		secs = append(secs, section{
			Depth: depth, Term: trellis.QuoteIfNeeded(bk.Value), Lines: bk.Lines,
		})
	}
	return secs
}

// writableBuckets returns the plugin's declared target buckets for the grouped
// dimension on node type tag (FacetWrite.Values) — the values organize
// pre-renders as empty buckets. nil for a plugin without the write capability or
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
