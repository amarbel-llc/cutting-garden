package cutting_garden_plugin_caldav

import (
	"bytes"
	"io"
	"testing"

	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/madder/go/pkgs/domain_interfaces"
	"github.com/amarbel-llc/madder/go/pkgs/markl"
	_ "github.com/amarbel-llc/madder/go/pkgs/markl_registrations"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/ohio"
)

// memStore is a retaining in-memory blob store for round-trip tests. It
// delegates markl-id computation to a discard-store writer (so ids are
// real, blech32-checksummed, and re-parseable via markl.Id.Set) while
// teeing the written bytes into a map keyed by id. Reads return the
// stored bytes verbatim. Mirrors the git plugin's test helper.
type memStore struct {
	hash  domain_interfaces.FormatHash
	blobs map[string][]byte
}

func newMemStore(t *testing.T) blob_stores.BlobStoreInitialized {
	t.Helper()
	return blob_stores.BlobStoreInitialized{
		BlobStore: &memStore{
			hash:  markl.FormatHashSha256,
			blobs: map[string][]byte{},
		},
	}
}

func (s *memStore) GetBlobStoreDescription() string { return "(mem)" }

func (s *memStore) GetDefaultHashType() domain_interfaces.FormatHash { return s.hash }

func (s *memStore) GetBlobStoreConfig() domain_interfaces.BlobStoreConfig { return nil }

func (s *memStore) GetBlobIOWrapper() domain_interfaces.BlobIOWrapper { return memIOWrapper{} }

func (s *memStore) HasBlob(id domain_interfaces.MarklId) bool {
	_, ok := s.blobs[id.String()]
	return ok
}

func (s *memStore) AllBlobs() interfaces.SeqError[domain_interfaces.MarklId] {
	return func(func(domain_interfaces.MarklId, error) bool) {}
}

func (s *memStore) MakeBlobReader(
	id domain_interfaces.MarklId,
) (domain_interfaces.BlobReader, error) {
	b, ok := s.blobs[id.String()]
	if !ok {
		return nil, errNotFound(id.String())
	}
	return &memBlobReader{Reader: bytes.NewReader(b), id: id}, nil
}

func (s *memStore) MakeBlobWriter(
	hashFormat domain_interfaces.FormatHash,
) (domain_interfaces.BlobWriter, error) {
	if hashFormat == nil {
		hashFormat = s.hash
	}
	inner, err := blob_stores.NewDiscardBlobStore(hashFormat).MakeBlobWriter(hashFormat)
	if err != nil {
		return nil, err
	}
	return &memBlobWriter{inner: inner, store: s}, nil
}

// memBlobWriter buffers written bytes while delegating to a discard
// writer that computes the real markl id; on Close it stores the buffer
// under that id.
type memBlobWriter struct {
	inner domain_interfaces.BlobWriter
	buf   bytes.Buffer
	store *memStore
}

func (w *memBlobWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	return w.inner.Write(p)
}

func (w *memBlobWriter) ReadFrom(r io.Reader) (int64, error) {
	return w.inner.ReadFrom(io.TeeReader(r, &w.buf))
}

func (w *memBlobWriter) GetMarklId() domain_interfaces.MarklId {
	return w.inner.GetMarklId()
}

func (w *memBlobWriter) Close() error {
	if err := w.inner.Close(); err != nil {
		return err
	}
	id := w.inner.GetMarklId().String()
	stored := make([]byte, w.buf.Len())
	copy(stored, w.buf.Bytes())
	w.store.blobs[id] = stored
	return nil
}

// memBlobReader serves stored bytes; *bytes.Reader supplies Read,
// ReadAt, Seek, and WriteTo.
type memBlobReader struct {
	*bytes.Reader
	id domain_interfaces.MarklId
}

func (r *memBlobReader) Close() error { return nil }

func (r *memBlobReader) GetMarklId() domain_interfaces.MarklId { return r.id }

type memIOWrapper struct{}

func (memIOWrapper) GetBlobEncryption() domain_interfaces.MarklId { return nil }

func (memIOWrapper) GetBlobCompression() interfaces.IOWrapper { return ohio.NopeIOWrapper{} }

func errNotFound(id string) error {
	return &notFoundError{id: id}
}

type notFoundError struct{ id string }

func (e *notFoundError) Error() string { return "mem blob not found: " + e.id }
