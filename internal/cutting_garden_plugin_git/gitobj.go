package cutting_garden_plugin_git

import (
	"context"
	"io"

	"github.com/amarbel-llc/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/madder/go/pkgs/markl"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/storage"
	"github.com/go-git/go-git/v5/storage/memory"
)

// gitobj.go is the madder ↔ go-git object bridge: the single seam where
// git's content-addressed objects cross into madder's blob store and
// back. go-git's plumbing.EncodedObject maps 1:1 to a madder blob holding
// the object's raw payload (the bytes after git's `<type> <size>\0`
// loose-object header — exactly what `git cat-file` emitted in the
// previous exec-based implementation), so object markl-ids are preserved
// across the migration and existing receipts stay valid.

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

// loadEncodedObject reconstructs a go-git object from the madder blob a
// payload reference points at, verifying the recreated git oid matches
// the reference alias (the integrity check the old restore path performed
// via `git hash-object -w`). The reference type-string carries the git
// object kind.
func loadEncodedObject(
	store blob_stores.BlobStoreInitialized,
	ref capture_plugin.Ref,
) (obj plumbing.EncodedObject, err error) {
	gitType := gitTypeFromObjectType(ref.TypeString)
	if gitType == "" {
		return nil, errors.ErrorWithStackf(
			"git plugin: payload reference %q has non-object type %q",
			ref.Alias, ref.TypeString)
	}
	objType, err := plumbing.ParseObjectType(gitType)
	if err != nil {
		return nil, errors.Wrapf(err, "git plugin: object type %q", gitType)
	}

	var id markl.Id
	if err = id.Set(ref.Digest); err != nil {
		return nil, errors.Wrapf(err, "parse object blob id %q", ref.Digest)
	}
	reader, err := store.MakeBlobReader(&id)
	if err != nil {
		return nil, errors.Wrapf(err, "open object blob %s", ref.Digest)
	}
	defer errors.DeferredCloser(&err, reader)

	mem := &plumbing.MemoryObject{}
	mem.SetType(objType)
	if _, err = io.Copy(mem, reader); err != nil {
		return nil, errors.Wrapf(err, "git plugin: read object blob %s", ref.Digest)
	}

	if got := mem.Hash().String(); got != ref.Alias {
		return nil, errors.ErrorWithStackf(
			"git plugin: object integrity mismatch: captured oid %q, "+
				"recreated oid %q (type %s)", ref.Alias, got, gitType)
	}
	return mem, nil
}

// priorTipRef is the local branch under which a previously-captured tip is
// advertised as a fetch `have`. go-git negotiates haves from the
// references its storer holds, so the seeded objects alone are not
// advertised — a reference pointing at the tip is what makes the server
// send only the delta.
const priorTipRef = "refs/heads/_cutting_garden_prior"

// populateNegotiationStorer seeds dst with every object a payload
// references, plus a reference at tip. It is the basis for the "have = a
// previously-captured snapshot" negotiation behind incremental capture
// and object-level diff: seed the prior objects, then fetch want=<live
// tip> so only the delta crosses the wire. dst is taken as a parameter
// (rather than created here) so a test can supply an instrumented storer.
func populateNegotiationStorer(
	dst storage.Storer,
	store blob_stores.BlobStoreInitialized,
	refs []capture_plugin.Ref,
	tip string,
) error {
	if err := writeObjectsToStorer(dst, store, refs); err != nil {
		return err
	}

	tipRef := plumbing.NewHashReference(
		plumbing.ReferenceName(priorTipRef), plumbing.NewHash(tip))
	if err := dst.SetReference(tipRef); err != nil {
		return errors.Wrapf(err, "git plugin: seed tip ref %s", tip)
	}
	return nil
}

// writeObjectsToStorer loads every referenced object out of madder (via the
// bridge, oid-verified) and writes it into dst. Shared by the negotiation
// seed (incremental/diff) and remote restore (push).
func writeObjectsToStorer(
	dst storer.EncodedObjectStorer,
	store blob_stores.BlobStoreInitialized,
	refs []capture_plugin.Ref,
) error {
	for _, ref := range refs {
		obj, err := loadEncodedObject(store, ref)
		if err != nil {
			return err
		}
		if _, err := dst.SetEncodedObject(obj); err != nil {
			return errors.Wrapf(err, "git plugin: write object %s", ref.Alias)
		}
	}
	return nil
}

// seedStorer builds a fresh in-memory negotiation storer from a payload's
// objects and tip — the production entry point over
// populateNegotiationStorer.
func seedStorer(
	store blob_stores.BlobStoreInitialized,
	refs []capture_plugin.Ref,
	tip string,
) (*memory.Storage, error) {
	st := memory.NewStorage()
	if err := populateNegotiationStorer(st, store, refs, tip); err != nil {
		return nil, err
	}
	return st, nil
}
