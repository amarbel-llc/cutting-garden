package cutting_garden_plugin_git

import (
	"context"
	"io"
	"runtime/debug"
	"strings"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugins"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
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
	return captureProtocol(req.Context, w, remote, branch)
}

// captureProtocol is the Writer-parameterized core of CaptureProtocol,
// split out so tests can drive it with an in-memory Writer.
func captureProtocol(
	ctx context.Context,
	w capture_plugin.Writer,
	remote, branch string,
) (cutting_garden_plugins.ProtocolCaptureResult, error) {
	source := canonicalSource(remote, branch)

	var (
		objectRefs     []capture_plugin.Ref
		tip            string
		resolvedBranch string
	)

	err := withBareClone(ctx, remote, branch, func(cloneDir, rb, t string) error {
		tip = t
		resolvedBranch = rb
		return streamAllObjects(ctx, cloneDir, func(oid, typ string, _ int64, payload io.Reader) error {
			digest, _, werr := w.WriteBlob(ctx, payload)
			if werr != nil {
				return errors.Wrap(werr)
			}
			objectRefs = append(objectRefs,
				capture_plugin.LockedRef(oid, digest, objectTypeString(typ)))
			return nil
		})
	})
	if err != nil {
		return cutting_garden_plugins.ProtocolCaptureResult{}, err
	}

	// Payload node: a JCS body of capture metadata plus one reference
	// per stored git object. The receipt references this single node, so
	// the receipt stays small while the object list lives one level down.
	// Record the *resolved* branch (never empty, even when the arg left
	// the branch to HEAD) so restore can recreate and check out the
	// preserved branch by name.
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

	pluginEnvBody := map[string]any{"git_version": gitVersion(ctx)}

	receiptDigest, err := capture_plugin.WriteReceipt(ctx, w, capture_plugin.ReceiptParams{
		Kind: captureKind,
		Invocation: capture_plugin.Invocation{
			Target:    source,
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
			Body:       pluginEnvBody,
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

// gitVersion returns `git version` output (trimmed) for the plugin-env
// identity node, or "unknown" if git can't be run.
func gitVersion(ctx context.Context) string {
	out, err := gitOutput(ctx, "", "version")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(out)
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
