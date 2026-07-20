package capture_receipt

import (
	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// WriteV1ToStore encodes entries via WriteV1WithHint and writes the
// resulting blob into blobStore. Returns the blob's content-addressed
// markl id as a string. When hint is non-nil, the receipt's hyphence
// metadata block carries an RFC 0001 store-hint line; pass nil for
// hint to omit. Mirrors madder's writeReceiptBlob shape; shared by the
// capture and serve commands.
func WriteV1ToStore(
	blobStore blob_stores.BlobStoreInitialized,
	entries []EntryV1,
	hint *StoreHint,
) (id string, err error) {
	wc, err := blobStore.MakeBlobWriter(nil)
	if err != nil {
		return "", errors.Wrap(err)
	}
	defer errors.DeferredCloser(&err, wc)

	if _, err = WriteV1WithHint(wc, entries, hint); err != nil {
		return "", errors.Wrap(err)
	}

	return wc.GetMarklId().String(), nil
}
