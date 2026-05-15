package restore

import (
	"bytes"
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
)

// fakeEnv is the test-only materializationEnv. The default-store
// sentinel is a zero BlobStoreInitialized — branches 4/5 return it
// from resolveMaterializationStore but never invoke methods on it,
// so the zero value is safe.
type fakeEnv struct {
	stores map[string]blob_stores.BlobStoreInitialized
}

func (f fakeEnv) GetDefaultBlobStore() blob_stores.BlobStoreInitialized {
	return blob_stores.BlobStoreInitialized{}
}

func (f fakeEnv) GetBlobStores() map[string]blob_stores.BlobStoreInitialized {
	if f.stores == nil {
		return map[string]blob_stores.BlobStoreInitialized{}
	}
	return f.stores
}

func TestResolveMaterializationStore_NoHint_EmitsFallbackNotices(t *testing.T) {
	// FDR §Store-Hint Resolution branch 5: a receipt with no hint
	// emits two notices to stderr and falls back to the active
	// default store.
	var buf bytes.Buffer

	_, err := resolveMaterializationStore(fakeEnv{}, nil, "", &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "notice: receipt carries no store hint\n" +
		"notice: falling back to active store\n"
	if got := buf.String(); got != want {
		t.Errorf("diagnostics mismatch.\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestResolveMaterializationStore_HintStoreMissing_EmitsFallbackNotices(t *testing.T) {
	// FDR §Store-Hint Resolution branch 4: hint names a store id that
	// is not locally configured. Emit a 2-line notice naming the
	// missing store id and fall back.
	hint := &capture_receipt.StoreHint{
		StoreId:       ".missing",
		ConfigMarklId: "blake2b256-abc",
	}
	var buf bytes.Buffer

	_, err := resolveMaterializationStore(fakeEnv{}, hint, "", &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "notice: receipt names store \".missing\" which is not configured locally\n" +
		"notice: falling back to active store\n"
	if got := buf.String(); got != want {
		t.Errorf("diagnostics mismatch.\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestResolveMaterializationStore_StoreOverrideMissing_ReturnsErr(t *testing.T) {
	// Branch 1: -store wins. With an unconfigured override id, the
	// resolveStoreByID error propagates (not a notice). No
	// diagnostic on stderr — the error is the visible artifact.
	var buf bytes.Buffer

	_, err := resolveMaterializationStore(fakeEnv{}, nil, ".not-configured", &buf)
	if err == nil {
		t.Fatal("expected error for unconfigured -store override, got nil")
	}
	if !strings.Contains(err.Error(), `-store ".not-configured" is not a configured blob store`) {
		t.Errorf("expected unconfigured-store error, got: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no diagnostic on -store path, got: %s", buf.String())
	}
}

// Branches 2 (match), 3 (drift), and the malformed-hint-id sub-branch
// (4a) all require a real BlobStoreInitialized with config bytes that
// ComputeStoreHint can hash — fakeEnv cannot produce one. They are
// covered end-to-end in:
//
//   - branch 2: zz-tests_bats/restore.bats →
//     restore_uses_hint_store_implicitly (must produce no stderr).
//   - branches 3 + 4a: zz-tests_bats/restore.bats FDR conformance
//     matrix (step 7).
