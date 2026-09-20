package organize

import (
	"fmt"
	"io"
	"strings"

	"code.linenisgreat.com/cutting-garden/internal/command_components"
	cgp "code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/plugin_blob_io"
	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// generateParams carries the resolved generate inputs shared by the FLAG path
// (buildAndStore — organize's -group-by/-query flags plus the `[organize]`
// config defaults) and the DOCUMENT path (fmt-organize, design G4 — an
// existing document's envelope, which is authoritative: its `_query` is
// already the composed effective query, its levers already resolved, and its
// provenance preserved verbatim).
type generateParams struct {
	// groupBy is the grouping's G10 spelling (`(tags)`, `project`, `status=`,
	// `date_due=(month)`).
	groupBy string
	// query is the selection query. Flag path: the raw -query value, composed
	// with the default `_terminal=no` exclusion (effectiveQuery). Document
	// path: the document's `_query` VERBATIM — it was composed at generate
	// time and echoed precisely so re-selection never re-injects the default
	// (the same verbatim rule apply follows).
	query string
	// includeTerminal is the flag path's -include-terminal; ignored when
	// fromDocument (the doc's `_query` already reflects the choice).
	includeTerminal bool
	// tagAtoms / tagStrip are the document's `_tag-atoms` / `_tag-strip`
	// fields (document path); empty on the flag path, where the `[organize]`
	// config defaults apply instead.
	tagAtoms, tagStrip string
	// provenance, when non-empty, is preserved verbatim (document path);
	// empty derives the `% generated:` note from the spelling + query + uri.
	provenance string
	// fromDocument marks the document path: the doc is authoritative, so the
	// `[organize]` config defaults (levers, date_granularity) are NOT
	// consulted — an absent envelope field means the built-in default,
	// exactly what generate's omit-at-default rule implies — and the query is
	// used verbatim. The `[tags]` interpreter override IS still honored, as
	// at generate (RFC 0019 §4).
	fromDocument bool
}

// buildAndStore selects the anchor's nodes, builds the organize document, stores
// its canonical form as an organize-base-v1 blob, and returns the emitted form
// (with the `- _base` pin) so a later apply three-way-merges the edits against
// the exact pre-edit state. Shared by the stdout (runGenerate) and interactive
// (runInteractive) paths; fmt-organize takes the buildAndStoreFrom core
// directly with the document's own envelope as the params.
func (cmd *Organize) buildAndStore(ctx errors.Context, uriStr string) (string, error) {
	if cmd.GroupBy == "" {
		return "", errors.BadRequestf(
			"organize <uri> requires --group-by: `(tags)`, a tag namespace (`project`), " +
				"a field (`status=`), or a date field at a granularity (`date_due=(month)`)",
		)
	}
	rendered, _, err := buildAndStoreFrom(ctx, uriStr, generateParams{
		groupBy:         cmd.GroupBy,
		query:           cmd.Query,
		includeTerminal: cmd.IncludeTerminal,
	})
	return rendered, err
}

// buildAndStoreFrom is the generate core: it resolves the anchor's lister,
// selects the nodes, builds + renders the document, stores the canonical base
// blob, and returns the emitted form plus the pinned digest. The params decide
// whether config defaults participate (flag path) or the document is
// authoritative (fmt-organize, design G4).
func buildAndStoreFrom(
	ctx errors.Context, uriStr string, p generateParams,
) (rendered, digest string, err error) {
	u, lister, err := command_components.ResolveRootListerPlugin(uriStr)
	if err != nil {
		return "", "", err
	}

	// Resolve the group-by spelling ONCE, at generate time (cutting-garden#230):
	// a bare `<dim>=` on a date dimension takes the `[organize] date_granularity`
	// config default, then day, and the resolved spelling is persisted in the
	// document's dimension heading (`# date_due=(month)`) — so a later --apply
	// never consults config (which may change in between). The config was
	// already loaded and warned about by Run's LoadAndInjectConfig; this re-read
	// just fetches the value. The document path drops the `[organize]` defaults
	// (a document's persisted spelling already carries any granularity).
	cfg, err := command_components.LoadDefaultConfig(nil)
	if err != nil {
		return "", "", err
	}
	dateDefault := cfg.Organize.DateGranularity
	configTagAtoms, configTagStrip := cfg.Organize.TagAtoms, cfg.Organize.TagStrip
	if p.fromDocument {
		dateDefault, configTagAtoms, configTagStrip = "", "", ""
	}
	// The unified declaration's cross-codec invariants (a second FieldTag field
	// per type, G6 v1) are checked ONCE here — generate's resolution point, the
	// first consumer that reads THE designated tag field — so a bad declaration
	// fails the command loudly instead of PresentUnifiedTags silently picking
	// the first.
	if err := validateUnifiedDeclaration(lister); err != nil {
		return "", "", err
	}
	dims := describedFacets(lister)
	tagDims := command_components.DescribedTagDims(lister)
	spec, err := parseGroupSpec(p.groupBy, dims, tagDims, dateDefault)
	if err != nil {
		return "", "", err
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
		interp, interpName, err = command_components.InterpreterForDimension(
			lister, tagDims[0], cfg.Tags.Interpreter,
		)
		if err != nil {
			return "", "", err
		}
		if spec.Kind == groupKindTagNamespace {
			if err := requireNamespaceInterpreter(interp, interpName, p.groupBy, spec); err != nil {
				return "", "", err
			}
		}
	}

	// The tag-atom levers (design G1/G2/G3): the document's fields win, then
	// config (flag path only), then the built-in defaults — and they are
	// persisted as envelope fields ONLY when non-default, so default documents
	// stay byte-identical.
	tagAtoms := effectiveTagAtoms(p.tagAtoms, configTagAtoms)
	tagStrip := effectiveTagStrip(p.tagStrip, configTagStrip)
	tags := tagRender{strip: tagStrip == tagStripPlacement}
	if tagAtoms != tagAtomsNone {
		tags.present = command_components.UnifiedTagPresenter(lister, interp)
	}

	// The effective query is the user's query with organize's default
	// `_terminal=no` exclusion composed in (cutting-garden#214) — echoed into the
	// document's `_query` below so the default is visible, editable, and re-applies
	// identically. The document path takes its `_query` verbatim (it IS that echo).
	effective := p.query
	if !p.fromDocument {
		effective = effectiveQuery(lister, p.query, p.includeTerminal)
	}
	nodes, err := selectNodes(ctx, lister, u, effective)
	if err != nil {
		return "", "", errors.Wrapf(err, "organize %s", uriStr)
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
		return "", "", err
	}
	if err := rejectEmptyNamespace(spec, doc, dims); err != nil {
		return "", "", err
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
	// is visible. The document path preserves the original note verbatim.
	doc.Provenance = p.provenance
	if doc.Provenance == "" {
		doc.Provenance = provenance(spec.String(), effective, uriStr)
	}

	// The canonical form (no `_base`) is the exact bytes hashed and stored; its
	// digest becomes the pin the emitted form carries.
	digest, err = storeBase(ctx, renderCanonical(doc))
	if err != nil {
		return "", "", err
	}
	doc.BaseDigest = digest

	return render(doc), digest, nil
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

// The tag-dimension/interpreter resolution helpers (describedTagDims,
// firstTagDim, interpreterForDimension, unifiedTagPresenter) moved to
// command_components (tag_view.go) in native tags slice 2 T4, so the
// `list -format json` and mcp node views share them with organize.

// boxAtomPresenter is command_components.BoxAtomPresenter — the FieldPresenter
// → box-atom projection (cutting-garden#47), moved there in native tags
// slice 4 so `list -format espalier` renders the same detail atoms
// (design G8/G13). nil for a plugin without the capability, and every atom
// value passes through CollapseToSingleLine (slice 1.5 F).
func boxAtomPresenter(lister cgp.RootLister) func(cgp.Node) []cgp.BoxAtom {
	return command_components.BoxAtomPresenter(lister)
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
// filed it there (bucketOfTag — the membership's Via reconstruction); every
// other tag stays, and ungrouped lines always keep their full set. A FIELD
// grouping strips nothing (no tag placement exists). Assigning .Tags is safe —
// each line is a value copy in its own slice, so one appearance's assignment
// never changes another's — but the assigned SLICES may share a backing array
// (appearances that strip nothing all receive byID's slice), so Tags must
// never be element-mutated after fill; every consumer only reads.
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

// withoutPlacementTags returns tags minus every tag whose realized bucket
// under spec IS bucketValue (bucketOfTag); the input slice is returned
// untouched when nothing strips, and nil when everything does. Single-pass:
// each tag's bucket is derived exactly once.
func withoutPlacementTags(
	tags []string, spec groupSpec, interp cgp.TagInterpreter, bucketValue string,
) []string {
	var out []string
	stripped := false
	for i, t := range tags {
		if bucketOfTag(t, spec, interp) == bucketValue {
			if !stripped {
				out = append(out, tags[:i]...)
				stripped = true
			}
			continue
		}
		if stripped {
			out = append(out, t)
		}
	}
	if !stripped {
		return tags
	}
	return out
}

// bucketOfTag returns the ONE bucket a single tag realizes under the spec's
// grouping, or "" when it realizes none — the strip rule's (and the apply
// gate's) shared derivation (design G2): under a whole-dimension grouping a
// tag's bucket is itself (Via == Bucket); under a namespace grouping the bare
// namespace tag realizes the ROOT bucket (G10a) and any tag the interpreter
// rolls up realizes its continuation bucket — so EVERY contributor to a
// bucket derives it, not just the interpreter's representative Membership.Via,
// matching the §6.2 write-back's whole-subtree removal. A field grouping has
// no tag placement, so no tag ever realizes a bucket there. A returned "" is
// never a valid bucket value, so comparing against a real bucket is safe.
func bucketOfTag(tag string, spec groupSpec, interp cgp.TagInterpreter) string {
	switch spec.Kind {
	case groupKindTagWhole:
		return tag
	case groupKindTagNamespace:
		if tag == spec.Namespace {
			return spec.Namespace
		}
		if interp == nil {
			return ""
		}
		ms, err := interp.Buckets([]string{tag}, spec.Namespace)
		if err != nil || len(ms) != 1 {
			return ""
		}
		return ms[0].Bucket
	default:
		return ""
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
func storeBase(ctx errors.Context, canonical string) (string, error) {
	store := command_components.MakeBlobStoreEnv(ctx).GetDefaultBlobStore()
	id, _, err := plugin_blob_io.WriteReaderBlob(ctx, store, strings.NewReader(canonical))
	if err != nil {
		return "", errors.Wrapf(err, "organize: store base blob")
	}
	return id.String(), nil
}
