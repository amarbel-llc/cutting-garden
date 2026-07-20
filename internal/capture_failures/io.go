package capture_failures

import (
	"io"

	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
	"code.linenisgreat.com/madder/go/pkgs/domain_interfaces"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// WriteV1 serializes v as a hyphence-wrapped failures-v1 blob to w
// via the package's Coder.
func WriteV1(w io.Writer, v *V1) (int64, error) {
	tb := &hyphenceBlob[Blob]{
		Type: TypeStructV1,
		Blob: v,
	}

	n, err := Coder.EncodeTo(tb, w)
	if err != nil {
		return n, errors.Wrap(err)
	}

	return n, nil
}

// ReadV1 parses a failures-v1 blob from r.
func ReadV1(r io.Reader) (*V1, error) {
	tb := &hyphenceBlob[Blob]{}

	if _, err := Coder.DecodeFrom(tb, r); err != nil {
		return nil, errors.Wrap(err)
	}

	v1, ok := tb.Blob.(*V1)
	if !ok {
		return nil, errors.ErrorWithStackf(
			"capture_failures: expected *V1, got %T (type %q)",
			tb.Blob, tb.Type,
		)
	}

	return v1, nil
}

// WriteV1ToStore encodes v via WriteV1 and writes the resulting blob
// into blobStore. Returns the blob's content-addressed markl id as a
// string. Mirrors capture_receipt.WriteV1ToStore.
func WriteV1ToStore(
	blobStore blob_stores.BlobStoreInitialized,
	v *V1,
) (id string, err error) {
	wc, err := blobStore.MakeBlobWriter(nil)
	if err != nil {
		return "", errors.Wrap(err)
	}
	defer errors.DeferredCloser(&err, wc)

	if _, err = WriteV1(wc, v); err != nil {
		return "", errors.Wrap(err)
	}

	return wc.GetMarklId().String(), nil
}

// Read fetches the blob named by id from blobStore and parses it via
// ReadV1. Mirrors capture_receipt.Read.
func Read(
	blobStore domain_interfaces.BlobReaderFactory,
	id domain_interfaces.MarklId,
) (v *V1, err error) {
	reader, err := blobStore.MakeBlobReader(id)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	defer errors.DeferredCloser(&err, reader)

	if v, err = ReadV1(reader); err != nil {
		return nil, errors.Wrap(err)
	}

	return v, nil
}
