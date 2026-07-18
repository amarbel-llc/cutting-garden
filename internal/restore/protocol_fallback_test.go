package restore

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/internal/capture_plugin"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/madder/go/pkgs/domain_interfaces"
	_ "github.com/amarbel-llc/madder/go/pkgs/markl_registrations"
	"github.com/amarbel-llc/piggy/go/pkgs/markl"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/ohio"
)

// memStore is the same real-markl-id in-memory store fixture used by
// plugins/git/memstore_test.go and internal/capture_plugin's own
// restore_payload_test.go. Duplicated here (rather than shared) because
// no cross-package test-fixture package exists yet — the established
// per-package pattern in this repo.
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

// writeWebShapedReceipt writes a single-payload protocol receipt of kind
// "restorefallbacktest" — deliberately a kind with NO registered
// cutting_garden_plugins.ProtocolRestorePlugin anywhere in this binary, so
// the receipt is "web-shaped" (one "payload" ref, same as the real web
// plugin's receipts) but dispatches through the GENERIC fallback rather
// than a kind-specific plugin.
func writeWebShapedReceipt(
	t *testing.T, store blob_stores.BlobStoreInitialized, payload []byte,
) string {
	t.Helper()
	w := capture_plugin.NewBlobStoreWriter(store)

	// The payload blob is itself a hyphence-framed node (matching what a
	// real protocol plugin's writer produces via BuildNode/WriteNode), not
	// raw bytes — see internal/capture_plugin/restore_payload_test.go's
	// identical fixture note.
	payloadNodeType := "jcs-restorefallbacktest-capture-payload-v1"
	payloadDigest, _, err := capture_plugin.WriteNode(
		context.Background(), w, capture_plugin.BuildNode(payloadNodeType, nil, payload),
	)
	if err != nil {
		t.Fatalf("write payload node: %v", err)
	}

	receiptDigest, err := capture_plugin.WriteReceipt(context.Background(), w, capture_plugin.ReceiptParams{
		Kind: "restorefallbacktest",
		Invocation: capture_plugin.Invocation{
			Target: "https://example.com/doc", Format: "raw",
		},
		Host:   capture_plugin.HostInfo{OS: "linux", Kernel: "6.0", Arch: "x86_64", Libc: "unknown"},
		Binary: capture_plugin.BinaryInfo{Name: "cutting-garden-test", Version: "dev"},
		PluginEnv: capture_plugin.PluginEnv{
			TypeString: "jcs-restorefallbacktest-capture-environment-v1",
			Body:       map[string]any{},
		},
		PayloadRefs: []capture_plugin.Ref{
			{Alias: "payload", Digest: payloadDigest, TypeString: payloadNodeType},
		},
		Now: func() time.Time { return time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}
	return receiptDigest
}

// TestRestoreProtocolReceipt_FallsBackToGenericPayloadRestore is the
// integration proof for cutting-garden#146 decision 3: a receipt whose
// kind ("restorefallbacktest") has NO registered ProtocolRestorePlugin —
// no plugin in this test binary calls MustRegisterProtocolRestore for it —
// still restores successfully, because restoreProtocolReceipt falls back
// to capture_plugin.RestorePayload for any single-payload receipt
// regardless of kind.
func TestRestoreProtocolReceipt_FallsBackToGenericPayloadRestore(t *testing.T) {
	store := newMemStore(t)
	const content = "web-shaped payload restored via the generic path\n"
	receiptDigest := writeWebShapedReceipt(t, store, []byte(content))

	ctx := errors.MakeContextDefault()
	dest := filepath.Join(t.TempDir(), "out", "restored.bin")

	if err := restoreProtocolReceipt(
		ctx, store, "restorefallbacktest", receiptDigest, dest,
	); err != nil {
		t.Fatalf("restoreProtocolReceipt: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(got) != content {
		t.Errorf("restored content = %q, want %q", got, content)
	}
}
