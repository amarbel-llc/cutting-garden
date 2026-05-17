package capture

import (
	"net/url"
	"os"
	"path/filepath"

	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/madder/go/pkgs/arg_resolver"
	"github.com/amarbel-llc/madder/go/pkgs/blob_store_id"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/errors"
)

// captureRoot is one directory plus the plugin that walks it. captureRoots
// belonging to the same captureGroup share a destination store and fold
// into a single receipt.
type captureRoot struct {
	// path is the original CLI argument verbatim. Used for sink labels,
	// shadow-detection input, audit-log entries, and root-collision
	// diagnostics. NEVER cleaned — see sourceURL for the cleaned form.
	path string

	plugin cutting_garden_plugins.CapturePlugin

	// sourceURL is the parsed URL the plugin walks. For schemeless args
	// (the file plugin's common case), sourceURL.Path is cleaned via
	// filepath.Clean during classifyArg so trailing slashes don't end
	// up in receipt entry.Root values.
	sourceURL *url.URL

	shadowNotice string
}

// captureGroup is the unit of one receipt: a target store plus the set of
// directories captured into it. An empty storeID selects the default
// store. switchNotice is non-empty when this group started with an
// explicit store-switch arg; the planner emits it before the group's first
// root is walked.
type captureGroup struct {
	storeID      blob_store_id.Id
	switchNotice string
	roots        []captureRoot
}

type classifyFailure struct {
	arg string
	err error
}

type argKind int

const (
	argKindError argKind = iota
	argKindCapture
	argKindStoreId
)

type classifiedArg struct {
	kind      argKind
	storeID   blob_store_id.Id
	plugin    cutting_garden_plugins.CapturePlugin
	sourceURL *url.URL
	err       error
}

// planCapture splits args into store groups and validates that each group
// has at least one capture-root. Returns either valid groups (and the
// per-arg classification failures, if any) or a planning error describing
// an empty store group. PWD is the implicit root in two cases: zero args,
// and a single arg that classifies as a store-id.
func planCapture(
	args []string,
	shadowCandidates []blob_store_id.Id,
) (groups []captureGroup, classifyFails []classifyFailure, err error) {
	if len(args) == 0 {
		plugin, _ := cutting_garden_plugins.ResolveCapture("")
		return []captureGroup{{
			roots: []captureRoot{{
				path:      ".",
				plugin:    plugin,
				sourceURL: &url.URL{Path: "."},
			}},
		}}, nil, nil
	}

	if len(args) == 1 {
		k := classifyArg(args[0])
		switch k.kind {
		case argKindStoreId:
			plugin, _ := cutting_garden_plugins.ResolveCapture("")
			return []captureGroup{{
				storeID:      k.storeID,
				switchNotice: arg_resolver.FormatStoreSwitchNotice(k.storeID),
				roots: []captureRoot{{
					path:      ".",
					plugin:    plugin,
					sourceURL: &url.URL{Path: "."},
				}},
			}}, nil, nil
		case argKindCapture:
			if scopeErr := k.plugin.ValidateSource(k.sourceURL, args[0]); scopeErr != nil {
				// Match the multi-arg loop: validation failures route to
				// classifyFails and surface via the sink. The
				// "failCount > 0" path in Run produces the cancel message;
				// no synthetic planErr is needed.
				return nil, []classifyFailure{{arg: args[0], err: scopeErr}}, nil
			}
			return []captureGroup{{
				roots: []captureRoot{{
					path:         args[0],
					plugin:       k.plugin,
					sourceURL:    k.sourceURL,
					shadowNotice: shadowNoticeFor(args[0], shadowCandidates),
				}},
			}}, nil, nil
		case argKindError:
			return nil, []classifyFailure{{arg: args[0], err: k.err}},
				errors.ErrorWithStackf("no usable directories or store-ids in arguments")
		}
	}

	current := captureGroup{}
	flush := func() error {
		if collisionErr := checkRootCollisions(current.roots); collisionErr != nil {
			return collisionErr
		}
		groups = append(groups, current)
		return nil
	}

	for _, arg := range args {
		k := classifyArg(arg)

		switch k.kind {
		case argKindError:
			classifyFails = append(classifyFails, classifyFailure{
				arg: arg,
				err: k.err,
			})

		case argKindStoreId:
			if len(current.roots) == 0 && !current.storeID.IsEmpty() {
				err = errors.ErrorWithStackf(
					"blob-store-id %q has no following directories",
					current.storeID,
				)
				return
			}
			if len(current.roots) > 0 {
				if err = flush(); err != nil {
					return
				}
			}

			current = captureGroup{
				storeID:      k.storeID,
				switchNotice: arg_resolver.FormatStoreSwitchNotice(k.storeID),
			}

		case argKindCapture:
			if scopeErr := k.plugin.ValidateSource(k.sourceURL, arg); scopeErr != nil {
				classifyFails = append(classifyFails, classifyFailure{
					arg: arg,
					err: scopeErr,
				})
				continue
			}
			current.roots = append(current.roots, captureRoot{
				path:         arg,
				plugin:       k.plugin,
				sourceURL:    k.sourceURL,
				shadowNotice: shadowNoticeFor(arg, shadowCandidates),
			})
		}
	}

	if len(current.roots) > 0 {
		if err = flush(); err != nil {
			return
		}
	} else if !current.storeID.IsEmpty() {
		err = errors.ErrorWithStackf(
			"blob-store-id %q has no following directories",
			current.storeID,
		)
		return
	}

	if len(groups) == 0 && len(classifyFails) == 0 {
		err = errors.ErrorWithStackf(
			"no usable directories or store-ids in arguments",
		)
		return
	}

	return
}

// classifyArg decides whether arg names a capture-source URI, a
// blob-store-id, or is unparseable. The schemeless heuristic mirrors
// madder's pre-plugin behavior: try Lstat first (so a symlink-to-directory
// is rejected — filepath.WalkDir refuses to descend it, and a one-entry
// "type=symlink" receipt would surprise a user who expected the linked
// tree's contents); on ENOENT, fall back to blob-store-id parsing. Users
// who want symlink-to-dir behavior should resolve the symlink with
// realpath before passing it in.
//
// URI args (anything with a scheme that resolves to a registered capture
// plugin) skip the Lstat dance entirely. Args with an unrecognized scheme
// fall through to the schemeless heuristic so filenames containing colons
// (e.g. `myfile:txt`) keep working — at the cost of a real edge case: a
// directory literally named `file:foo` is interpreted as the file plugin
// pointing at `foo`, not as the local directory.
//
// Schemeless directory args have their sourceURL.Path normalized via
// filepath.Clean so trailing slashes do not propagate into receipt
// entry.Root. The original arg is preserved on captureRoot.path for
// sink labels and shadow detection.
func classifyArg(arg string) classifiedArg {
	if u, err := url.Parse(arg); err == nil && u.Scheme != "" {
		if plugin, perr := cutting_garden_plugins.ResolveCapture(u.Scheme); perr == nil {
			return classifiedArg{
				kind:      argKindCapture,
				plugin:    plugin,
				sourceURL: u,
			}
		}
		// Unknown scheme — fall through to the schemeless heuristic.
	}

	info, err := os.Lstat(arg)
	switch {
	case err == nil && info.IsDir():
		plugin, _ := cutting_garden_plugins.ResolveCapture("")
		return classifiedArg{
			kind:      argKindCapture,
			plugin:    plugin,
			sourceURL: &url.URL{Path: filepath.Clean(arg)},
		}
	case err == nil:
		return classifiedArg{
			kind: argKindError,
			err: errors.ErrorWithStackf(
				"%q exists but is not a directory; capture only takes directories (resolve symlinks with realpath if needed)",
				arg,
			),
		}
	case errors.IsNotExist(err):
		// fall through to store-id parsing
	default:
		return classifiedArg{kind: argKindError, err: errors.Wrap(err)}
	}

	var id blob_store_id.Id
	if perr := id.Set(arg); perr == nil {
		return classifiedArg{kind: argKindStoreId, storeID: id}
	}

	return classifiedArg{
		kind: argKindError,
		err: errors.ErrorWithStackf(
			"%q is neither a recognized URI, an existing directory, nor a valid blob-store-id",
			arg,
		),
	}
}

// checkRootCollisions refuses two roots within a single store-group that
// resolve to the same path under filepath.Clean per RFC 0001 §Producer
// Rules §Root Collision Detection.
func checkRootCollisions(roots []captureRoot) error {
	seen := make(map[string]string, len(roots))

	for _, r := range roots {
		clean := canonicalRootKey(r)
		if first, ok := seen[clean]; ok {
			return errors.ErrorWithStackf(
				"roots %q and %q both resolve to %q after Clean\nhint: pass each directory only once per store-group",
				first, r.path, clean,
			)
		}
		seen[clean] = r.path
	}

	return nil
}

// canonicalRootKey returns the filesystem-path key used for collision
// dedup. Comparing on captureRoot.path alone misses cross-scheme aliases
// (e.g. `file:dir-a` vs `dir-a/` both resolve to `dir-a` but differ as
// raw strings); comparing on sourceURL handles URI args by preferring
// Path, falling back to Opaque for opaque URLs like `file:dir-a`.
func canonicalRootKey(r captureRoot) string {
	if r.sourceURL == nil {
		return filepath.Clean(r.path)
	}
	if p := r.sourceURL.Path; p != "" {
		return filepath.Clean(p)
	}
	return filepath.Clean(r.sourceURL.Opaque)
}

// shadowNoticeFor returns a human-readable warning when arg shadows a
// configured blob-store-id (i.e. a directory `dodder-v8-take3/` exists in
// PWD AND `dodder-v8-take3` is a configured store name). Empty string
// when no shadow is detected.
func shadowNoticeFor(arg string, candidates []blob_store_id.Id) string {
	shadowed, ok := arg_resolver.DetectShadow(arg, candidates)
	if !ok {
		return ""
	}
	return arg_resolver.FormatShadowWarning(arg, shadowed)
}

// blobStoreIds returns every configured blob-store-id, suitable for
// passing to arg_resolver.DetectShadow. Local reimplementation of
// madder's command_components.BlobStoreIds (internal/golf, not exported
// via pkgs/) — same shape as the makeBlobStoreEnv reimplementation in
// capture.go.
func blobStoreIds(m blob_stores.BlobStoreMap) []blob_store_id.Id {
	ids := make([]blob_store_id.Id, 0, len(m))
	for _, s := range m {
		ids = append(ids, s.GetId())
	}
	return ids
}
