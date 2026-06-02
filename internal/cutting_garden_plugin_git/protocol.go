package cutting_garden_plugin_git

import (
	"context"
	"io"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/storage/memory"
)

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

// CaptureProtocol implements cutting_garden_plugins.ProtocolCapturePlugin:
// it stores the branch's full object graph as content-addressed blobs
// and wraps them in an RFC 0002 receipt merkle tree (receipt → identity
// → environment, outcome, payload), returning the root receipt's markl
// id. The git objects are the payload subtree; the payload node
// references each one by oid.
func (Plugin) CaptureProtocol(
	req cutting_garden_plugins.ProtocolCaptureRequest,
) (cutting_garden_plugins.ProtocolCaptureResult, error) {
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
			req.Context, req.BlobStore, w, remote, branch, req.PriorReceiptDigest)
		if ierr != nil {
			return cutting_garden_plugins.ProtocolCaptureResult{}, ierr
		}
		if ok {
			return res, nil
		}
	}

	return captureProtocol(req.Context, w, remote, branch)
}

// captureProtocol is the full (clone-everything) Writer-parameterized
// core of CaptureProtocol, split out so tests can drive it with an
// in-memory Writer. It mirrors git's object graph into madder by cloning
// the single branch into an in-memory go-git storer (no `git` binary, no
// working tree) and streaming every reachable object through the bridge.
func captureProtocol(
	ctx context.Context,
	w capture_plugin.Writer,
	remote, branch string,
) (cutting_garden_plugins.ProtocolCaptureResult, error) {
	repo, tip, resolvedBranch, err := cloneBranchToMemory(ctx, remote, branch)
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}

	objectRefs, err := storeAllObjects(ctx, w, repo.Storer)
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}

	return writeGitReceipt(ctx, w, remote, resolvedBranch, tip, objectRefs)
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
) ([]capture_plugin.Ref, error) {
	iter, err := store.IterEncodedObjects(plumbing.AnyObject)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	defer iter.Close()

	var objectRefs []capture_plugin.Ref
	if err := iter.ForEach(func(obj plumbing.EncodedObject) error {
		ref, werr := writeEncodedObject(ctx, w, obj)
		if werr != nil {
			return werr
		}
		objectRefs = append(objectRefs, ref)
		return nil
	}); err != nil {
		return nil, err
	}
	return objectRefs, nil
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
	remote, resolvedBranch, tip string,
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
			Version: cgVersion(),
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

// cgVersion returns the cutting-garden binary version for the identity
// binary node. It reads the Go build info main-module version; absent a
// tagged build (e.g. `go test`, `go run`), it reports "dev".
func cgVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}
