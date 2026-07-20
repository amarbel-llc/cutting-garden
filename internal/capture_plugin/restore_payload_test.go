package capture_plugin

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
	"code.linenisgreat.com/madder/go/pkgs/domain_interfaces"
	_ "code.linenisgreat.com/madder/go/pkgs/markl_registrations"
	"code.linenisgreat.com/piggy/go/pkgs/markl"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/ohio"
)

// memStore is a retaining in-memory blob store for RestorePayload's
// round-trip tests: real, blech32-checksummed markl ids (delegated to a
// discard-store writer) with bytes retained in a map, so a written node
// can be read back through the same store.MakeBlobReader path
// RestorePayload uses. Mirrors plugins/git/memstore_test.go's memStore
// (no shared helper package exists across plugins/ and internal/ today;
// each test package that needs a real store fixture builds its own, the
// established pattern in this repo).
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
		return nil, errNotFoundMemStore(id.String())
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

type memBlobReader struct {
	*bytes.Reader
	id domain_interfaces.MarklId
}

func (r *memBlobReader) Close() error { return nil }

func (r *memBlobReader) GetMarklId() domain_interfaces.MarklId { return r.id }

type memIOWrapper struct{}

func (memIOWrapper) GetBlobEncryption() domain_interfaces.MarklId { return nil }

func (memIOWrapper) GetBlobCompression() interfaces.IOWrapper { return ohio.NopeIOWrapper{} }

type notFoundErrorMemStore struct{ id string }

func (e *notFoundErrorMemStore) Error() string { return "mem blob not found: " + e.id }

func errNotFoundMemStore(id string) error { return &notFoundErrorMemStore{id: id} }

// writeTestReceipt writes a payload blob plus a full protocol receipt
// referencing it as "payload" into store, using the real WriteReceipt path
// (not a hand-built node) so the fixture matches what an actual protocol
// plugin produces. kind lets callers pin a receipt kind distinct from any
// registered ProtocolRestorePlugin, for fallback-dispatch tests elsewhere.
func writeTestReceipt(
	t *testing.T, store blob_stores.BlobStoreInitialized, kind string, payload []byte,
) string {
	t.Helper()
	w := NewBlobStoreWriter(store)

	// The payload blob is itself a hyphence-framed node (matching what a
	// real protocol plugin's writer produces via BuildNode/WriteNode), not
	// raw bytes — OpenNodeBody parses the node header off it and streams
	// only the body. Using w.WriteBlob directly here would write UNFRAMED
	// bytes that OpenNodeBody's ParseNodeHeader then fails to parse.
	payloadNodeType := "jcs-" + kind + "-capture-payload-v1"
	payloadDigest, _, err := WriteNode(
		context.Background(), w, BuildNode(payloadNodeType, nil, payload),
	)
	if err != nil {
		t.Fatalf("write payload node: %v", err)
	}

	receiptDigest, err := WriteReceipt(context.Background(), w, ReceiptParams{
		Kind: kind,
		Invocation: Invocation{
			Target: "https://example.com/doc", Format: "raw",
		},
		Host:   HostInfo{OS: "linux", Kernel: "6.0", Arch: "x86_64", Libc: "unknown"},
		Binary: BinaryInfo{Name: "cutting-garden-test", Version: "dev"},
		PluginEnv: PluginEnv{
			TypeString: "jcs-" + kind + "-capture-environment-v1",
			Body:       map[string]any{},
		},
		PayloadRefs: []Ref{
			{Alias: "payload", Digest: payloadDigest, TypeString: "jcs-" + kind + "-capture-payload-v1"},
		},
		Now: func() time.Time { return time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}
	return receiptDigest
}

func TestRestorePayload_StreamsPayloadToDest(t *testing.T) {
	store := newMemStore(t)
	const content = "generic single-payload restore content\n"
	receiptDigest := writeTestReceipt(t, store, "gentest", []byte(content))

	dest := filepath.Join(t.TempDir(), "out", "restored.bin")
	if err := RestorePayload(store, receiptDigest, dest); err != nil {
		t.Fatalf("RestorePayload: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(got) != content {
		t.Errorf("restored content = %q, want %q", got, content)
	}
}

func TestRestorePayload_RefusesExistingDestination(t *testing.T) {
	store := newMemStore(t)
	receiptDigest := writeTestReceipt(t, store, "gentest", []byte("x"))

	dest := filepath.Join(t.TempDir(), "already-there")
	if err := os.WriteFile(dest, []byte("preexisting"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := RestorePayload(store, receiptDigest, dest)
	if err == nil {
		t.Fatal("expected error for existing destination, got nil")
	}
}

func TestRestorePayload_ErrorsOnReceiptWithNoPayloadRef(t *testing.T) {
	store := newMemStore(t)
	w := NewBlobStoreWriter(store)

	// A metadata-only node with no "payload" alias — stands in for a
	// receipt shape the generic restorer does not support (e.g. a
	// multi-object tree like git's).
	nodeBytes := BuildNode("cutting_garden-capture_receipt-notarealkind-v1", nil, nil)
	digest, _, err := WriteNode(context.Background(), w, nodeBytes)
	if err != nil {
		t.Fatalf("WriteNode: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "out")
	err = RestorePayload(store, digest, dest)
	if err == nil {
		t.Fatal("expected error for receipt with no payload ref, got nil")
	}
}
