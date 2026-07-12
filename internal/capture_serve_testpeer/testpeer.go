// Package capture_serve_testpeer is the cg-owned RFC 0008 test plugin:
// a deterministic capture-serve peer that emits one fixed receipt tree
// through the blob protocol, so the orchestrator side is testable with
// no chrest dependency. It backs three consumers: the capture_serve
// package's session tests (in-process and re-exec'd), the packaged
// cutting-garden-test-capture-serve binary the bats sandbox lane runs,
// and — because every identity input is pinned — the byte-identity
// conformance assertions themselves (the tree's digests are a pure
// function of the Writer).
package capture_serve_testpeer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"code.linenisgreat.com/cutting-garden/internal/capture_plugin"
	"code.linenisgreat.com/cutting-garden/internal/capture_serve"
)

// FixedPayload is the single payload blob of the fixed receipt tree.
const FixedPayload = "fixed payload bytes for the capture-serve tracer\n"

// EmitFixedReceipt writes the tracer's deterministic receipt tree
// through w — payload first, then capture_plugin.WriteReceipt UNCHANGED,
// exactly as a real plugin binding does. Returns the root receipt's
// digest and byte size.
func EmitFixedReceipt(
	ctx context.Context, w capture_plugin.Writer,
) (digest string, size int64, err error) {
	payloadDigest, _, err := w.WriteBlob(ctx, strings.NewReader(FixedPayload))
	if err != nil {
		return "", 0, err
	}

	rec := &recordingWriter{inner: w}
	digest, err = capture_plugin.WriteReceipt(ctx, rec, capture_plugin.ReceiptParams{
		Kind: "test",
		Invocation: capture_plugin.Invocation{
			Target:    "test://fixture",
			Format:    "fixed",
			Normalize: true,
		},
		Host: capture_plugin.HostInfo{
			OS: "linux", Kernel: "0.0", Arch: "amd64", Libc: "unknown",
		},
		Binary: capture_plugin.BinaryInfo{
			Name: "cg-test-capture-serve", Version: "0.0.0",
		},
		PluginEnv: capture_plugin.PluginEnv{
			TypeString: "jcs-test-capture-environment-v1",
			Body:       map[string]any{"fixture": "v1"},
		},
		PayloadRefs: []capture_plugin.Ref{{
			Alias:      "payload",
			Digest:     payloadDigest,
			TypeString: "test-payload-v1",
		}},
		Now: func() time.Time {
			return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		},
	})
	if err != nil {
		return "", 0, err
	}
	return digest, rec.lastSize, nil
}

// recordingWriter remembers the last written blob's size: WriteReceipt
// writes the receipt node last and returns only its digest, but the
// batch result's ReceiptRef wants {id, size}.
type recordingWriter struct {
	inner    capture_plugin.Writer
	lastSize int64
}

func (w *recordingWriter) WriteBlob(
	ctx context.Context, r io.Reader,
) (string, int64, error) {
	digest, size, err := w.inner.WriteBlob(ctx, r)
	w.lastSize = size
	return digest, size, err
}

// Config is the test peer's ServeConfig: exactly one capture per batch,
// answered with the fixed receipt.
func Config() capture_serve.ServeConfig {
	return capture_serve.ServeConfig{
		Plugin: capture_serve.PluginInfo{
			Name: "cg-test-capture-serve", Version: "0.0.0",
		},
		Formats: []string{"fixed"},
		Batch: func(
			ctx context.Context,
			params capture_serve.BatchParams,
			w capture_plugin.Writer,
		) (capture_serve.BatchResult, error) {
			if len(params.Captures) != 1 {
				return capture_serve.BatchResult{}, fmt.Errorf(
					"want exactly one capture, got %d", len(params.Captures),
				)
			}
			digest, size, err := EmitFixedReceipt(ctx, w)
			if err != nil {
				return capture_serve.BatchResult{}, err
			}
			return capture_serve.BatchResult{
				Schema: capture_serve.SchemaV2,
				Plugin: capture_serve.PluginInfo{
					Name: "cg-test-capture-serve", Version: "0.0.0",
				},
				Errors: []capture_serve.ProtocolError{},
				Captures: []capture_serve.CaptureResult{{
					Name: params.Captures[0].Name,
					Receipt: &capture_serve.ReceiptRef{
						ID: digest, Size: size,
					},
				}},
			}, nil
		},
	}
}

// Main is the whole plugin process: the RFC 0008 bring-up sequence
// (cookie guard → rendezvous listen → announce on stdout → accept)
// around Serve, with the stdin-EOF lifecycle signal. argv is ignored so
// the binary can masquerade as `chrest capture-serve` in test lanes.
// Returns the process exit code: 0 after a graceful shutdown (the
// shutdown notification or stdin EOF), nonzero on bring-up failure or a
// dropped control socket.
func Main() int {
	cookie, err := capture_serve.CookieFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ln, sock, cleanup, err := capture_serve.ListenRendezvous()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer cleanup()

	line, err := capture_serve.AnnounceLine(cookie, capture_serve.Handshake{
		Version: capture_serve.SchemaV2,
		Network: capture_serve.HandshakeNetwork,
		Address: sock,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if _, err := os.Stdout.WriteString(line); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// stdin EOF is a shutdown signal (RFC 0008 §Cancellation and
	// shutdown), armed BEFORE accept so an orchestrator that dies (or a
	// smoke test that never dials) unblocks the accept via the listener
	// close instead of hanging the peer forever.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		cancel()
	}()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	conn, err := ln.AcceptUnix()
	if err != nil {
		if ctx.Err() != nil {
			return 0
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if err := capture_serve.Serve(ctx, conn, Config()); err != nil {
		if ctx.Err() != nil {
			return 0
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// MemStore is an in-memory capture_plugin.Writer digesting with sha256,
// so ids are deterministic without a madder store. Reference and
// transport runs both write into MemStores, making their digests and
// blob sets directly comparable.
type MemStore struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

func NewMemStore() *MemStore {
	return &MemStore{blobs: map[string][]byte{}}
}

func (s *MemStore) WriteBlob(
	_ context.Context, r io.Reader,
) (string, int64, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", 0, err
	}
	sum := sha256.Sum256(data)
	id := "sha256-" + hex.EncodeToString(sum[:])
	s.mu.Lock()
	s.blobs[id] = data
	s.mu.Unlock()
	return id, int64(len(data)), nil
}

// Snapshot copies the stored blob set for assertions.
func (s *MemStore) Snapshot() map[string][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string][]byte, len(s.blobs))
	for id, data := range s.blobs {
		out[id] = data
	}
	return out
}
