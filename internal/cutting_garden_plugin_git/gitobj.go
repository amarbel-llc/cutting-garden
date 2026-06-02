package cutting_garden_plugin_git

import (
	"context"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/go-git/go-git/v5/plumbing"
)

// gitobj.go is the madder ↔ go-git object bridge: the single seam where
// git's content-addressed objects cross into madder's blob store. go-git's
// plumbing.EncodedObject maps 1:1 to a madder blob holding the object's
// raw payload (the bytes after git's `<type> <size>\0` loose-object
// header — exactly what `git cat-file` emitted in the previous exec-based
// implementation), so object markl-ids are preserved across the migration
// and existing receipts stay valid.

// writeEncodedObject stores one git object's raw payload as a
// content-addressed madder blob and returns a locked payload reference
// keyed by the object's git oid. The reference alias is the git oid; the
// digest is the madder blob id; the type-string is the git-kind leaf
// type. Mirrors the per-object ref the old streamAllObjects path built.
func writeEncodedObject(
	ctx context.Context,
	w capture_plugin.Writer,
	obj plumbing.EncodedObject,
) (capture_plugin.Ref, error) {
	r, err := obj.Reader()
	if err != nil {
		return capture_plugin.Ref{}, errors.Wrapf(err,
			"git plugin: open reader for object %s", obj.Hash())
	}
	defer errors.DeferredCloser(&err, r)

	digest, _, err := w.WriteBlob(ctx, r)
	if err != nil {
		return capture_plugin.Ref{}, errors.Wrap(err)
	}

	return capture_plugin.LockedRef(
		obj.Hash().String(),
		digest,
		objectTypeString(obj.Type().String()),
	), nil
}
