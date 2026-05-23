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

func writeFixture(t *testing.T, size int) string {
	t.Helper()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "fixture.bin")
	if err := os.WriteFile(src, bytes.Repeat([]byte("x"), size), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return src
}
