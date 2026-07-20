package capture_receipt

import (
	"testing"

	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
)

func TestComputeStoreHint_EmptyStoreIdReturnsNil(t *testing.T) {
	// Empty storeIdString is the "couldn't resolve to a real id"
	// sentinel; per RFC 0001 producers MAY omit the hint. The
	// function returns (nil, nil) without touching the blobStore —
	// so the zero-value blobStore here never has its methods called.
	hint, err := ComputeStoreHint(blob_stores.BlobStoreInitialized{}, "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if hint != nil {
		t.Errorf("hint = %+v, want nil", hint)
	}
}
