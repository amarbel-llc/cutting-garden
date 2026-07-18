package capture_wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
)

// fakePlugin installs a shim capturer binary that drains stdin and
// prints the given stdout, so capture() can be exercised without a
// real browser. It also tees the stdin it received to a file for
// input-shape assertions. Like a real pre-RFC-0008 chrest, it rejects
// the "capture-serve" subcommand with a fast nonzero exit and nothing
// on stdout — so every capture() call here exercises the genuine
// v2-attempt→v1-fallback path, not just v1. Adapted from
// plugins/web/exec_test.go's fakeChrest (cutting-garden#146 slice 2
// phase 2), generalized to build a *Plugin around the shim's absolute
// path instead of a PATH-resolved "chrest" name.
func fakePlugin(t *testing.T, stdout string) (p *Plugin, inputPath string) {
	t.Helper()
	dir := t.TempDir()
	inputPath = filepath.Join(dir, "input.json")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"capture-serve\" ]; then\n" +
		"  echo 'unknown subcommand: capture-serve' >&2\n" +
		"  exit 2\n" +
		"fi\n" +
		"cat > " + inputPath + "\ncat <<'JSON'\n" + stdout + "\nJSON\n"
	binPath := filepath.Join(dir, "fakecapturer")
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return New(Spec{
		Name:    "fakecapturer",
		Command: []string{binPath},
		Schemes: []string{"fakecapturer"},
	}), inputPath
}

func TestCaptureParsesReceipt(t *testing.T) {
	p, inputPath := fakePlugin(t, `{"schema":"capture-plugin/v1","plugin":{"name":"fake","version":"test"},"errors":[],"captures":[{"name":"cg","receipt":{"id":"blake2b256-abc","size":123}}]}`)

	id, err := p.capture(context.Background(), blob_stores.BlobStoreInitialized{},
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
	p, _ := fakePlugin(t, `{"schema":"capture-plugin/v1","plugin":{"name":"fake","version":"t"},"errors":[],"captures":[{"name":"cg","error":{"kind":"fetch-failed","message":"connection reset"}}]}`)

	_, err := p.capture(context.Background(), blob_stores.BlobStoreInitialized{},
		"", "https://example.com", "pdf")
	if err == nil || !strings.Contains(err.Error(), "fetch-failed") {
		t.Fatalf("want fetch-failed error, got %v", err)
	}
}

func TestCaptureBatchError(t *testing.T) {
	p, _ := fakePlugin(t, `{"schema":"capture-plugin/v1","plugin":{"name":"fake","version":"t"},"errors":[{"kind":"bad-input","message":"nope"}],"captures":[]}`)

	_, err := p.capture(context.Background(), blob_stores.BlobStoreInitialized{},
		"", "https://example.com", "pdf")
	if err == nil || !strings.Contains(err.Error(), "bad-input") {
		t.Fatalf("want batch error, got %v", err)
	}
}

func TestCaptureRejectsWrongSchema(t *testing.T) {
	p, _ := fakePlugin(t, `{"schema":"web-capture-archive/v1","plugin":{"name":"fake","version":"t"},"errors":[],"captures":[]}`)

	_, err := p.capture(context.Background(), blob_stores.BlobStoreInitialized{},
		"", "https://example.com", "pdf")
	if err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("want schema error, got %v", err)
	}
}

func TestCaptureNoBinaryFallsThroughToV1NotFoundDiagnostic(t *testing.T) {
	p := New(Spec{
		Name:    "missingcapturer",
		Command: []string{filepath.Join(t.TempDir(), "does-not-exist")},
		Schemes: []string{"missingcapturer"},
	})

	_, err := p.capture(context.Background(), blob_stores.BlobStoreInitialized{},
		"", "https://example.com", "pdf")
	if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
		t.Fatalf("want not-found diagnostic, got %v", err)
	}
}
