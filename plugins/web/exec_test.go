package cutting_garden_plugin_web

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
)

// fakeChrest installs a `chrest` shim on PATH that drains stdin and prints
// the given stdout, so capture() can be exercised without a browser. It
// also tees the stdin it received to a file for input-shape assertions.
// Like a real pre-RFC-0008 chrest, it rejects the `capture-serve`
// subcommand with a fast nonzero exit and nothing on stdout — so every
// capture() call here exercises the genuine v2-attempt→v1-fallback path,
// not just v1.
func fakeChrest(t *testing.T, stdout string) (inputPath string) {
	t.Helper()
	dir := t.TempDir()
	inputPath = filepath.Join(dir, "input.json")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"capture-serve\" ]; then\n" +
		"  echo 'unknown subcommand: capture-serve' >&2\n" +
		"  exit 2\n" +
		"fi\n" +
		"cat > " + inputPath + "\ncat <<'JSON'\n" + stdout + "\nJSON\n"
	if err := os.WriteFile(filepath.Join(dir, "chrest"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return inputPath
}

func TestCaptureParsesReceipt(t *testing.T) {
	inputPath := fakeChrest(t, `{"schema":"capture-plugin/v1","plugin":{"name":"chrest","version":"test"},"errors":[],"captures":[{"name":"cg","receipt":{"id":"blake2b256-abc","size":123}}]}`)

	id, err := capture(context.Background(), blob_stores.BlobStoreInitialized{},
		"store-x", "https://example.com", "pdf")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if id != "blake2b256-abc" {
		t.Errorf("receipt id = %q, want blake2b256-abc", id)
	}

	// The marshaled input must be a well-formed capture-plugin/v1 batch:
	// our schema, the target, a single named capture, and a writer.cmd
	// pointing at this binary's __write-blob sink bound to the store.
	raw, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{
		`"schema":"capture-plugin/v1"`,
		`"target":"https://example.com"`,
		`"format":"pdf"`,
		`"__write-blob"`,
		`"--store"`,
		`"store-x"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("batch input missing %s\ninput: %s", want, got)
		}
	}
}

func TestCapturePerCaptureError(t *testing.T) {
	fakeChrest(t, `{"schema":"capture-plugin/v1","plugin":{"name":"chrest","version":"t"},"errors":[],"captures":[{"name":"cg","error":{"kind":"fetch-failed","message":"connection reset"}}]}`)

	_, err := capture(context.Background(), blob_stores.BlobStoreInitialized{},
		"", "https://example.com", "pdf")
	if err == nil || !strings.Contains(err.Error(), "fetch-failed") {
		t.Fatalf("want fetch-failed error, got %v", err)
	}
}

func TestCaptureBatchError(t *testing.T) {
	fakeChrest(t, `{"schema":"capture-plugin/v1","plugin":{"name":"chrest","version":"t"},"errors":[{"kind":"bad-input","message":"nope"}],"captures":[]}`)

	_, err := capture(context.Background(), blob_stores.BlobStoreInitialized{},
		"", "https://example.com", "pdf")
	if err == nil || !strings.Contains(err.Error(), "bad-input") {
		t.Fatalf("want batch error, got %v", err)
	}
}

func TestCaptureRejectsWrongSchema(t *testing.T) {
	fakeChrest(t, `{"schema":"web-capture-archive/v1","plugin":{"name":"chrest","version":"t"},"errors":[],"captures":[]}`)

	_, err := capture(context.Background(), blob_stores.BlobStoreInitialized{},
		"", "https://example.com", "pdf")
	if err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("want schema error, got %v", err)
	}
}
