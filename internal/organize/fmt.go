package organize

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"code.linenisgreat.com/cutting-garden/internal/command"
	"code.linenisgreat.com/cutting-garden/internal/command_components"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// FmtOrganize is the value registered for the `fmt-organize` subcommand
// (native tags design G4, v1): a formatter that regenerates an organize
// document FROM ITS ENVELOPE — `_anchor` re-resolved to the plugin,
// `_query` re-selected verbatim, the grouping re-run from `_group-by` (or
// the field dimension heading) with the document's own `_tag-atoms` /
// `_tag-strip` levers — then re-renders against the live data, stores the
// new base blob, and rewrites the file in place (atomically) with the new
// `- _base` pin. The document is authoritative: `[organize]` config
// defaults are not consulted (an absent lever field means the built-in
// default), though interpreter resolution still honors the `[tags]` config
// override exactly as generate does.
//
// v1 REFUSES when the body differs from its pinned base — "doc has
// unapplied edits; apply or discard first" (exit 64) — so a pending edit is
// never silently regenerated over. The clean-body predicate is
// renderCanonical(parse(file)) == the stored base blob's bytes: the same
// data-plane projection `_base` content-addresses, so equality IMPLIES the
// state in which apply would find zero edits — conservatively: fmt also
// refuses provenance/`%`-comment or envelope churn apply would ignore —
// while benign formatting noise (extra blank lines, a deeper heading root)
// re-renders canonically and passes. The edit-preserving v2 (carry pending
// edits forward,
// re-place object lines) is cutting-garden#252. Like generate,
// fmt-organize never emits empty (reset) headings.
type FmtOrganize struct {
	output io.Writer
}

var (
	_ command.Cmd                    = (*FmtOrganize)(nil)
	_ command.CommandWithDescription = (*FmtOrganize)(nil)
	_ command.CommandWithArgs        = (*FmtOrganize)(nil)
	_ command.CommandWithExamples    = (*FmtOrganize)(nil)
	_ command.CommandWithSeeAlso     = (*FmtOrganize)(nil)
)

// NewFmt constructs a FmtOrganize with output routed to os.Stdout.
func NewFmt() *FmtOrganize {
	return &FmtOrganize{output: os.Stdout}
}

func (*FmtOrganize) GetDescription() command.Description {
	return command.Description{
		Short: "regenerate an organize document in place from its envelope",
		Long: "Re-runs an organize document's own generate spec — its " +
			"`- _anchor`, `- _query`, grouping (`- _group-by` or the " +
			"dimension heading), and `_tag-atoms`/`_tag-strip` levers — " +
			"against the live data, then rewrites the file in place " +
			"(atomically) with a fresh `- _base` pin, printing a one-line " +
			"rewritten/unchanged summary. The document is authoritative: " +
			"config defaults are not re-consulted. If the body no longer " +
			"matches its pinned base — the document has unapplied edits — " +
			"fmt-organize refuses (exit 64) rather than regenerate over " +
			"them; apply or discard the edits first. An edit-preserving v2 " +
			"is tracked at cutting-garden#252. See the native tags design " +
			"(G4), RFC 0015.",
	}
}

func (*FmtOrganize) GetArgs() []command.ArgGroup {
	return []command.ArgGroup{{
		Args: []command.Arg{
			{
				Name: "path",
				Description: "path of the organize document to refresh in " +
					"place. Must carry a `- _base = @<digest>` pin whose " +
					"blob is in the local store, and a body that still " +
					"matches it (no unapplied edits).",
				Required: true,
			},
		},
	}}
}

func (*FmtOrganize) GetExamples() []command.Example {
	return []command.Example{
		{
			Description: "Refresh a previously generated document against " +
				"the live data, rewriting its `- _base` pin.",
			Command: "cutting-garden fmt-organize triage.txt",
		},
	}
}

func (*FmtOrganize) GetSeeAlso() []string {
	return []string{
		"cutting-garden(1)",
		"cutting-garden-organize(1)",
	}
}

func (cmd *FmtOrganize) Run(req command.Request) {
	ctx := req.Context.(errors.Context)

	// Config load precedes the regenerate: the plugin (and any wire plugin) is
	// resolvable through the scheme registry only after registration, exactly
	// as in organize's Run (RFC 0013 §Host integration).
	if _, err := command_components.LoadAndInjectConfig(os.Stderr); err != nil {
		errors.ContextCancelWithError(ctx, err)
		return
	}

	args := req.PeekArgs()
	if len(args) != 1 {
		errors.ContextCancelWithBadRequestf(ctx,
			"fmt-organize takes exactly one positional argument, the document "+
				"<path>; got %d", len(args))
		return
	}

	if err := cmd.run(ctx, args[0]); err != nil {
		errors.ContextCancelWithError(ctx, err)
	}
}

func (cmd *FmtOrganize) run(ctx errors.Context, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return errors.Wrapf(err, "fmt-organize: read %s", path)
	}
	original := string(data)

	doc, err := parseDocument(original)
	if err != nil {
		return err
	}
	spec, err := doc.groupedSpec()
	if err != nil {
		return err
	}
	if doc.Anchor == "" {
		return errors.BadRequestf(
			"fmt-organize: %s: document is missing its `- _anchor` field, so "+
				"there is no plugin URI to regenerate from", path,
		)
	}
	if !spec.grouped() {
		return errors.BadRequestf(
			"fmt-organize: %s: document has no grouping — neither a `# <dim>=` "+
				"dimension heading nor a `- _group-by` directive", path,
		)
	}
	if doc.BaseDigest == "" {
		return errors.BadRequestf(
			"fmt-organize: %s: document has no `- _base = @<digest>` pin to "+
				"compare against", path,
		)
	}

	// The G4 v1 clean-body gate: the document's data-plane projection
	// (renderCanonical — the exact bytes `_base` content-addresses) must equal
	// the pinned base blob byte for byte — equality implies apply would see
	// zero edits (the gate is stricter: comment/envelope churn refuses too).
	// Checked BEFORE any network touch, so refusing is cheap and works offline.
	store := command_components.MakeBlobStoreEnv(ctx).GetDefaultBlobStore()
	baseBody, err := readBase(store, doc.BaseDigest)
	if err != nil {
		return errors.Wrapf(err, "fmt-organize: %s", path)
	}
	if renderCanonical(doc) != baseBody {
		return errors.BadRequestf(
			"fmt-organize: %s: doc has unapplied edits; apply or discard first "+
				"(the body no longer matches its pinned `- _base` — write the edits "+
				"with `cg organize -apply %s`, or regenerate to discard them)",
			path, path,
		)
	}

	// Clean: regenerate from the envelope. The document is authoritative
	// (fromDocument): its `_query` is the composed effective query, verbatim;
	// its levers and provenance carry forward; config defaults stay out.
	rendered, digest, err := buildAndStoreFrom(ctx, doc.Anchor, generateParams{
		groupBy:      spec.String(),
		query:        doc.Query,
		tagAtoms:     doc.TagAtoms,
		tagStrip:     doc.TagStrip,
		provenance:   doc.Provenance,
		fromDocument: true,
	})
	if err != nil {
		return err
	}

	if rendered == original {
		fmt.Fprintf(cmd.output, "fmt-organize: %s unchanged\n", path)
		return nil
	}
	if err := writeFileAtomic(path, rendered); err != nil {
		return err
	}
	fmt.Fprintf(cmd.output,
		"fmt-organize: %s rewritten — _base @%s → @%s\n",
		path, doc.BaseDigest, digest)
	return nil
}

// writeFileAtomic replaces path's content via a temp file + rename in the same
// directory, preserving the original file's mode — so a crash mid-write never
// leaves a half-written document, and the rename is atomic on POSIX.
func writeFileAtomic(path, content string) (err error) {
	info, err := os.Stat(path)
	if err != nil {
		return errors.Wrapf(err, "fmt-organize: stat %s", path)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cg-fmt-organize-*")
	if err != nil {
		return errors.Wrapf(err, "fmt-organize: create temp for %s", path)
	}
	tmpPath := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err = io.WriteString(tmp, content); err != nil {
		_ = tmp.Close()
		return errors.Wrapf(err, "fmt-organize: write %s", tmpPath)
	}
	if err = tmp.Close(); err != nil {
		return errors.Wrapf(err, "fmt-organize: close %s", tmpPath)
	}
	if err = os.Chmod(tmpPath, info.Mode().Perm()); err != nil {
		return errors.Wrapf(err, "fmt-organize: chmod %s", tmpPath)
	}
	if err = os.Rename(tmpPath, path); err != nil {
		return errors.Wrapf(err, "fmt-organize: replace %s", path)
	}
	return nil
}
