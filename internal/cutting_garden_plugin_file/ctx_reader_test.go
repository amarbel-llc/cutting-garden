package cutting_garden_plugin_file

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

// TestCtxReader_PreCancelledReturnsCtxErr is the unit test for the
// wrap itself: a cancelled ctx surfaces as ctx.Err before any
// underlying Read happens.
func TestCtxReader_PreCancelledReturnsCtxErr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := newCtxReader(ctx, bytes.NewReader([]byte("hello")))
	n, err := r.Read(make([]byte, 8))

	if n != 0 {
		t.Errorf("Read returned n=%d; want 0", n)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Read err = %v; want context.Canceled", err)
	}
}

// TestWriteFileBlob_PreCancelledContextAborts confirms the ctx-cancel
// chain reaches writeFileBlob's io.Copy — the capture path. The
// assertion is that ctx-cancel surfaces as the returned error, not
// that the copy is interrupted at a specific byte (granularity is
// io.Copy's 32 KiB default, documented on ctxReader).
func TestWriteFileBlob_PreCancelledContextAborts(t *testing.T) {
	src := writeFixture(t, 1<<16)
	store := blob_stores.NewDiscardBlobStore(markl.FormatHashSha256)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := writeFileBlob(ctx, store, src)
	requireWrapsCancel(t, "writeFileBlob", err)
}

// TestHashFileViaStore_PreCancelledContextAborts covers the diff
// scan path.
func TestHashFileViaStore_PreCancelledContextAborts(t *testing.T) {
	src := writeFixture(t, 1<<16)
	store := blob_stores.NewDiscardBlobStore(markl.FormatHashSha256)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := hashFileViaStore(ctx, store, src)
	requireWrapsCancel(t, "hashFileViaStore", err)
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

func requireWrapsCancel(t *testing.T, name string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s returned nil error after pre-cancelled ctx", name)
	}
	if !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Errorf("%s err = %v; want it to wrap context.Canceled", name, err)
	}
}
