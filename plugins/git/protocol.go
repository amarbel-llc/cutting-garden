package cutting_garden_plugin_git

import (
	"context"
	"fmt"
	"io"
	"runtime/debug"
	"sort"
	"strings"

	"code.linenisgreat.com/cutting-garden/pkgs/capture_events"
	"code.linenisgreat.com/cutting-garden/pkgs/capture_plugin"
	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/storage/memory"
)

// progressLogWriter is an io.Writer that turns go-git's clone progress
// sideband — line-oriented server text using BOTH '\r' (in-place percent
// updates) and '\n' (phase end) as separators — into Reporter.Log lines.
// Each '\r'- or '\n'-delimited segment is trimmed and, if non-empty,
// flushed to log; a trailing partial segment stays buffered for the next
// Write. This is non-identity observability: it never touches captured
// bytes.
type progressLogWriter struct {
	log func(string)
	buf []byte
}

func (w *progressLogWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := indexCRorLF(w.buf)
		if i < 0 {
			break
		}
		segment := strings.TrimSpace(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
		if segment != "" {
			w.log(segment)
		}
	}
	return len(p), nil
}

// indexCRorLF returns the index of the first '\r' or '\n' in b, or -1 when
// neither is present.
func indexCRorLF(b []byte) int {
	for i, c := range b {
		if c == '\r' || c == '\n' {
			return i
		}
	}
	return -1
}

// Git-binding node type-strings under the RFC 0002 type convention.
const (
	// captureKind tags the receipt: cutting_garden-capture-receipt-git-v1.
	captureKind = "git"
	// payloadType is the git payload node: a metadata node referencing
	// every stored object plus a JCS body of capture metadata.
	payloadType = "jcs-git-capture-payload-v1"
	// pluginEnvType is the git plugin's identity-affecting environment
	// node (the git version it shelled out to).
	pluginEnvType = "jcs-git-capture-environment-v1"
	// captureFormat is the invocation `format` value for git captures.
	captureFormat = "object-graph"
)

// objectTypeString is the ref type-string for a stored git object of a
// given git type, e.g. git-capture-object-blob-v1.
func objectTypeString(gitType string) string {
	return "git-capture-object-" + gitType + "-v1"
}

// optVersion extracts the optional binary version threaded through the
// capture helpers as a trailing variadic (empty when a test omits it).
func optVersion(version []string) string {
	if len(version) > 0 {
		return version[0]
	}
	return ""
}

// CaptureProtocol implements cutting_garden_plugins.ProtocolCapturePlugin:
// it stores the branch's full object graph as content-addressed blobs
// and wraps them in an RFC 0002 receipt merkle tree (receipt → identity
// → environment, outcome, payload), returning the root receipt's markl
// id. The git objects are the payload subtree; the payload node
// references each one by oid.
func (Plugin) CaptureProtocol(
	req cutting_garden_plugins.ProtocolCaptureRequest,
) (cutting_garden_plugins.ProtocolCaptureResult, error) {
	// Non-identity observability: Plan/Progress/Log are SEMANTICS, not
	// identity — they MUST NOT influence any blob bytes or the captured
	// object graph (pinned by
	// TestCaptureProtocol_Reporter_DoesNotAffectIdentity, which compares
	// the payload digest with vs without a Reporter). ReporterOrNop makes
	// a nil Reporter a no-op so the emission sites below stay
	// unconditional.
	r := cutting_garden_plugins.ReporterOrNop(req.Reporter)

	remote, branch, err := remoteAndBranchFromArg(req.Source)
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}

	w := capture_plugin.NewBlobStoreWriter(req.BlobStore)

	// Incremental fast path: if the orchestrator supplied a prior receipt
	// for this remote+branch, fetch only the objects that differ since
	// then (gitwire negotiation) instead of re-cloning. Falls back to a
	// full capture when that isn't possible (no prior, non-fast-forward,
	// unsupported transport).
	if req.PriorReceiptDigest != "" {
		res, ok, ierr := tryIncrementalCapture(
			req.Context, req.BlobStore, w, remote, branch, req.PriorReceiptDigest, r, req.BinaryVersion,
		)
		if ierr != nil {
			return cutting_garden_plugins.ProtocolCaptureResult{}, ierr
		}
		if ok {
			return res, nil
		}
	}

	return captureProtocol(req.Context, w, remote, branch, r, req.BinaryVersion)
}

// captureProtocol is the full (clone-everything) Writer-parameterized
// core of CaptureProtocol, split out so tests can drive it with an
// in-memory Writer. It mirrors git's object graph into madder by cloning
// the single branch into an in-memory go-git storer (no `git` binary, no
// working tree) and streaming every reachable object through the bridge.
// version is a trailing variadic so the many in-package tests that drive
// captureProtocol directly compile unchanged; the production caller
// (CaptureProtocol) always supplies req.BinaryVersion.
func captureProtocol(
	ctx context.Context,
	w capture_plugin.Writer,
	remote, branch string,
	r cutting_garden_plugins.Reporter,
	version ...string,
) (cutting_garden_plugins.ProtocolCaptureResult, error) {
	// The clone phase: the old "cloning…" Log is folded into the phase
	// description; the clone-progress sideband Logs and the resolved-tip
	// Log below land inside the phase as tail detail.
	r.PhaseStart(fmt.Sprintf("clone %s (%s)", remote, branchLabel(branch)))

	repo, tip, resolvedBranch, err := cloneBranchToMemory(ctx, remote, branch, r)
	if err != nil {
		r.PhaseEnd(capture_events.Verdict{
			OK:         false,
			Diagnostic: map[string]any{"error": err.Error()},
		})
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}
	r.Log("resolved %s at %s", resolvedBranch, shortHash(tip))
	r.PhaseEnd(capture_events.Verdict{OK: true})

	objectRefs, err := storeAllObjects(ctx, w, repo.Storer, r)
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}

	res, err := writeGitReceipt(ctx, w, remote, resolvedBranch, tip, optVersion(version), objectRefs)
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}
	r.Log("receipt %s", shortHash(res.ReceiptDigest))
	return res, nil
}

// cloneBranchToMemory bare-clones a single branch of remote into an
// in-memory object store (full history, no tags, no working tree) and
// returns the repository plus the resolved tip oid and branch name. A
// nil worktree makes go-git perform a bare clone; the object database
// holds exactly the objects reachable from the branch tip. When branch is
// empty the remote's default branch (HEAD) is used.
func cloneBranchToMemory(
	ctx context.Context,
	remote, branch string,
	r cutting_garden_plugins.Reporter,
) (repo *git.Repository, tip, resolvedBranch string, err error) {
	auth, err := authMethod(remote)
	if err != nil {
		return nil, "", "", err
	}
	opts := &git.CloneOptions{
		URL:          remote,
		SingleBranch: true,
		Tags:         git.NoTags,
		Auth:         auth,
		// Live clone-phase progress (Counting/Receiving objects). Setting
		// Progress non-nil makes go-git request the server's progress
		// sideband; it does NOT change which objects are transferred, so
		// captured bytes are unchanged. A Nop reporter's Log is a no-op.
		Progress: &progressLogWriter{log: func(s string) { r.Log("%s", s) }},
	}
	if branch != "" {
		opts.ReferenceName = plumbing.NewBranchReferenceName(branch)
	}

	repo, err = git.CloneContext(ctx, memory.NewStorage(), nil, opts)
	if err != nil {
		return nil, "", "", errors.Wrapf(err, "git plugin: clone %s", remote)
	}

	head, err := repo.Head()
	if err != nil {
		return nil, "", "", errors.Wrapf(err, "git plugin: resolve HEAD of %s", remote)
	}
	return repo, head.Hash().String(), head.Name().Short(), nil
}

// storeAllObjects writes every object in the storer to madder via the
// bridge and returns one locked payload reference per object. This is the
// go-git equivalent of the old `git cat-file --batch-all-objects` stream:
// after a single-branch clone the storer holds exactly the branch's
// reachable closure.
func storeAllObjects(
	ctx context.Context,
	w capture_plugin.Writer,
	store storer.EncodedObjectStorer,
	r cutting_garden_plugins.Reporter,
) ([]capture_plugin.Ref, error) {
	// Pre-count so the Reporter can render a determinate bar, framing the
	// bar over commit+tree objects only. Blobs (file leaves) dominate the
	// object count and are too numerous to report individually; we still
	// WRITE every object below, but Plan/Progress track the structural
	// skeleton so the bar's total matches the number of Progress emissions.
	// After a single-branch in-memory clone the storer holds exactly the
	// branch's reachable closure, so this pass touches only RAM (no I/O) —
	// cheap relative to the per-object blob writes below.
	structuralCount, err := countStructuralObjects(store)
	if err != nil {
		return nil, err
	}
	// The store phase starts only after the pre-count so its description
	// carries the real total. On a write error the phase is left open and
	// the error propagates — Finalize(err) marks the run failed.
	r.PhaseStart(fmt.Sprintf("store %d objects", structuralCount))
	r.Plan(cutting_garden_plugins.ReportPlan{
		Items: int64(structuralCount),
		Label: "storing git objects",
	})

	iter, err := store.IterEncodedObjects(plumbing.AnyObject)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	defer iter.Close()

	var objectRefs []capture_plugin.Ref
	var structural int64
	if err := iter.ForEach(func(obj plumbing.EncodedObject) error {
		ref, werr := writeEncodedObject(ctx, w, obj)
		if werr != nil {
			return werr
		}
		objectRefs = append(objectRefs, ref)
		// Report only the structural skeleton (commit+tree); blobs are
		// written above but not reported individually.
		if isStructural(obj.Type()) {
			structural++
			r.Progress(cutting_garden_plugins.ReportProgress{
				Item:  typeLabel(obj.Type()) + " " + obj.Hash().String(),
				Items: structural,
			})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	r.PhaseEnd(capture_events.Verdict{OK: true})
	return objectRefs, nil
}

// countStructuralObjects counts the commit and tree objects in store via a
// metadata-only pass over the storer's iterator — used to feed the
// Reporter a determinate Plan whose total equals the number of structural
// Progress emissions in the write loop. It does not load object payloads.
func countStructuralObjects(store storer.EncodedObjectStorer) (int, error) {
	iter, err := store.IterEncodedObjects(plumbing.AnyObject)
	if err != nil {
		return 0, errors.Wrap(err)
	}
	defer iter.Close()

	var n int
	if err := iter.ForEach(func(obj plumbing.EncodedObject) error {
		if isStructural(obj.Type()) {
			n++
		}
		return nil
	}); err != nil {
		return 0, err
	}
	return n, nil
}

// isStructural reports whether a git object type is part of the structural
// skeleton (commit or tree) that Plan/Progress frame over — as opposed to
// the blob leaves (and tags) that are stored but not reported individually.
func isStructural(t plumbing.ObjectType) bool {
	return t == plumbing.CommitObject || t == plumbing.TreeObject
}

// typeLabel renders a structural git object type as a short human-readable
// Progress item prefix ("commit"/"tree").
func typeLabel(t plumbing.ObjectType) string {
	switch t {
	case plumbing.CommitObject:
		return "commit"
	case plumbing.TreeObject:
		return "tree"
	default:
		return t.String()
	}
}

// writeGitReceipt assembles the git payload node (a JCS metadata body
// plus one reference per object) and the RFC 0002 receipt tree over the
// given object references, returning the receipt's markl id. Shared by
// the full and incremental capture paths.
//
// The payload records the *resolved* branch (never empty, even when the
// arg left the branch to HEAD) so restore can recreate the preserved
// branch by name.
func writeGitReceipt(
	ctx context.Context,
	w capture_plugin.Writer,
	remote, resolvedBranch, tip, version string,
	objectRefs []capture_plugin.Ref,
) (cutting_garden_plugins.ProtocolCaptureResult, error) {
	// Sort object references by oid so the payload node is byte-stable
	// regardless of how the objects were gathered — a full clone and an
	// incremental fetch of the same repo state yield the identical
	// payload node (and thus the identical payload digest).
	sort.Slice(objectRefs, func(i, j int) bool {
		return objectRefs[i].Alias < objectRefs[j].Alias
	})

	payloadBody, err := capture_plugin.JCS(map[string]any{
		"remote":       remote,
		"branch":       resolvedBranch,
		"tip":          tip,
		"object_count": len(objectRefs),
	})
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}
	payloadDigest, _, err := w.WriteBlob(ctx,
		readerOf(capture_plugin.BuildNode(payloadType, objectRefs, payloadBody)))
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, errors.Wrap(err)
	}

	receiptDigest, err := capture_plugin.WriteReceipt(ctx, w, capture_plugin.ReceiptParams{
		Kind: captureKind,
		Invocation: capture_plugin.Invocation{
			Target:    canonicalSource(remote, resolvedBranch),
			Format:    captureFormat,
			Normalize: false,
			Options:   map[string]any{},
		},
		Host: capture_plugin.GatherHost(),
		Binary: capture_plugin.BinaryInfo{
			Name:    "cutting-garden",
			Version: version,
		},
		PluginEnv: capture_plugin.PluginEnv{
			TypeString: pluginEnvType,
			Body:       map[string]any{"git_version": goGitVersion()},
		},
		PayloadRefs: []capture_plugin.Ref{
			capture_plugin.LockedRef("payload", payloadDigest, payloadType),
		},
	})
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}

	return cutting_garden_plugins.ProtocolCaptureResult{
		ReceiptDigest: receiptDigest,
		ObjectCount:   len(objectRefs),
	}, nil
}

// readerOf wraps node bytes in an io.Reader for the Writer.
func readerOf(b []byte) io.Reader { return strings.NewReader(string(b)) }

// shortHash abbreviates an oid or markl digest for a human-readable Log
// line, mirroring git's short-hash convention. It trims any
// algorithm prefix (e.g. "sha256-") so the abbreviation is of the digest
// body, and is purely cosmetic (Reporter Logs are non-identity).
func shortHash(h string) string {
	if _, body, found := strings.Cut(h, "-"); found {
		h = body
	}
	const shortLen = 12
	if len(h) > shortLen {
		return h[:shortLen]
	}
	return h
}

// branchLabel renders a branch name for a Log line, naming the implicit
// default branch when the arg left it empty (resolved to HEAD at clone
// time).
func branchLabel(branch string) string {
	if branch == "" {
		return "HEAD"
	}
	return branch
}

// goGitModulePath is the module whose version is recorded as the git
// plugin's tool identity now that capture runs in-process via go-git.
const goGitModulePath = "github.com/go-git/go-git/v5"

// goGitVersion reports the go-git library version backing this plugin for
// the identity-affecting plugin-env node. The git capture is performed
// in-process via go-git rather than by shelling out to the `git` binary,
// so the recorded tool identity is the go-git module version. Falls back
// to "go-git (unknown)" when build info is absent (e.g. `go run`).
func goGitVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == goGitModulePath {
				return "go-git " + dep.Version
			}
		}
	}
	return "go-git (unknown)"
}
