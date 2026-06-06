package plugin_blob_io

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/madder/go/pkgs/markl"
)

func TestCtxReader_PreCancelledReturnsCtxErr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := NewCtxReader(ctx, bytes.NewReader([]byte("hello")))
	n, err := r.Read(make([]byte, 8))

	if n != 0 {
		t.Errorf("Read returned n=%d; want 0", n)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Read err = %v; want context.Canceled", err)
	}
}

func TestWriteFileBlob_PreCancelledContextAborts(t *testing.T) {
	src := writeFixture(t, 1<<16)
	store := blob_stores.NewDiscardBlobStore(markl.FormatHashSha256)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := WriteFileBlob(ctx, store, src)
	if err == nil {
		t.Fatal("WriteFileBlob returned nil error after pre-cancelled ctx")
	}
	if !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Errorf("WriteFileBlob err = %v; want it to wrap context.Canceled", err)
	}
}

func TestWriteFileBlobProgress_EmitsStridedMonotonicSamples(t *testing.T) {
	// 2.5 MiB: two stride crossings (1 MiB, 2 MiB) plus the final flush
	// with the total.
	const size = 5 << 19
	src := writeFixture(t, size)
	store := blob_stores.NewDiscardBlobStore(markl.FormatHashSha256)

	var samples []int64
	_, gotSize, err := WriteFileBlobProgress(
		context.Background(), store, src,
		func(n int64) { samples = append(samples, n) },
	)
	if err != nil {
		t.Fatalf("WriteFileBlobProgress: %v", err)
	}
	if gotSize != size {
		t.Errorf("size = %d, want %d", gotSize, size)
	}

	if len(samples) < 2 {
		t.Fatalf("onBytes called %d times, want >= 2: %v", len(samples), samples)
	}
	if last := samples[len(samples)-1]; last != size {
		t.Errorf("final sample = %d, want %d (file size)", last, size)
	}
	for i := 1; i < len(samples); i++ {
		if samples[i] < samples[i-1] {
			t.Errorf("samples[%d] = %d < samples[%d] = %d (not monotonic)",
				i, samples[i], i-1, samples[i-1])
		}
	}
}

func TestWriteFileBlobProgress_NilCallbackMatchesWriteFileBlob(t *testing.T) {
	src := writeFixture(t, 1<<16)
	store := blob_stores.NewDiscardBlobStore(markl.FormatHashSha256)

	idPlain, sizePlain, err := WriteFileBlob(context.Background(), store, src)
	if err != nil {
		t.Fatalf("WriteFileBlob: %v", err)
	}
	idNil, sizeNil, err := WriteFileBlobProgress(context.Background(), store, src, nil)
	if err != nil {
		t.Fatalf("WriteFileBlobProgress(nil): %v", err)
	}

	if idPlain.String() != idNil.String() || sizePlain != sizeNil {
		t.Errorf("nil-callback result {id:%q size:%d} differs from WriteFileBlob {id:%q size:%d}",
			idNil, sizeNil, idPlain, sizePlain)
	}
}

func writeFixture(t *testing.T, size int) string {
	t.Helper()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "fixture.bin")
	if err := os.WriteFile(src, bytes.Repeat([]byte("x"), size), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return src
}
