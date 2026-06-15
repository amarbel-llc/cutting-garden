package cutting_garden_plugin_git

import (
	"context"
	"testing"

	"github.com/amarbel-llc/cutting-garden/pkgs/capture_plugin"
	"github.com/go-git/go-git/v5/plumbing"
)

// memObject builds an in-memory go-git object of the given type and
// content, the way the capture path receives objects from a clone.
func memObject(t *testing.T, typ plumbing.ObjectType, content []byte) *plumbing.MemoryObject {
	t.Helper()
	o := &plumbing.MemoryObject{}
	o.SetType(typ)
	if _, err := o.Write(content); err != nil {
		t.Fatalf("write object content: %v", err)
	}
	return o
}

// TestWriteEncodedObject_KeysRefByGitOid verifies the bridge's write
// contract: the payload reference is keyed by the object's git oid (not
// the madder digest) and carries the git-kind leaf type, so receipts
// address objects by oid the way RFC 0004 specifies.
func TestWriteEncodedObject_KeysRefByGitOid(t *testing.T) {
	store := newMemStore(t)
	w := capture_plugin.NewBlobStoreWriter(store)

	src := memObject(t, plumbing.BlobObject, []byte("the quick brown fox\n"))
	wantOid := src.Hash().String()

	ref, err := writeEncodedObject(context.Background(), w, src)
	if err != nil {
		t.Fatalf("writeEncodedObject: %v", err)
	}
	if ref.Alias != wantOid {
		t.Fatalf("ref alias = %q, want git oid %q", ref.Alias, wantOid)
	}
	if ref.TypeString != "git-capture-object-blob-v1" {
		t.Fatalf("ref type = %q, want git-capture-object-blob-v1", ref.TypeString)
	}
	if ref.Digest == wantOid {
		t.Fatalf("digest %q must be the madder blob id, not the git oid", ref.Digest)
	}
	if ref.Digest == "" {
		t.Fatal("empty madder digest")
	}
}
