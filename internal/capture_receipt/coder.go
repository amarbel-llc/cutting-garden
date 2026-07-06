// Coder + version-dispatching machinery for capture receipts.
//
// Mirrors the dodder pattern (delta/blob_store_configs/coding.go):
// a hyphence.CoderToTypedBlob[Blob] whose Metadata coder populates
// the typed-blob's Type during the metadata pass, and whose Blob
// dispatcher (CoderTypeMapWithoutType) selects a per-version body
// coder based on that Type during the body pass. No buffering — the
// body decoder streams from the bufio.Reader hyphence hands it.
//
// The store-hint metadata line (RFC 0001 §Producer Rules §Receipt
// Metadata: Store Hint) is also consumed by the metadata coder. It
// pre-allocates a *V1 with the captured Hint set on it, so the body
// coder for TypeTagV1 can stream NDJSON entries directly into the
// existing struct.
package capture_receipt

import (
	"bufio"
	"strings"

	"github.com/amarbel-llc/hyphence/go/hyphence"
	"github.com/amarbel-llc/madder/go/pkgs/domain_interfaces"
	"github.com/amarbel-llc/madder/go/pkgs/ids"
	"github.com/amarbel-llc/piggy/go/pkgs/markl"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/format"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/ohio"
)

// The canonical hyphence TypedBlob is generic over the type-tag and digest
// types; we bind those to madder's native ids.TypeStruct / markl.Id so the
// existing ids.MustTypeStruct / `==` / StringSansOp call sites keep working
// unchanged and the store-hint hyphenceBlob[blob_store_configs.Config]
// (store_hint_compute.go) unifies with madder's blob_store_configs.Coder.
// markl.Id's zero value is null, so a receipt that sets no digest still omits
// the `@` line — wire output is unchanged from the pre-extraction facade.
type (
	hyphenceBlob[BLOB any]       = hyphence.TypedBlob[ids.TypeStruct, *ids.TypeStruct, markl.Id, *markl.Id, BLOB]
	hyphenceCoder[BLOB any]      = hyphence.CoderToTypedBlob[ids.TypeStruct, *ids.TypeStruct, markl.Id, *markl.Id, BLOB]
	hyphenceBlobCoders[BLOB any] = hyphence.CoderTypeMapWithoutType[ids.TypeStruct, *ids.TypeStruct, markl.Id, *markl.Id, BLOB]
)

// TypeStructV1 is the wire type-id that appears on the `! ` line of a
// v1 receipt. Stored as ids.TypeStruct so it can compare directly
// with typedBlob.Type at dispatch time.
var TypeStructV1 = ids.MustTypeStruct(TypeTagV1)

// Coder decodes and encodes hyphence-wrapped receipts of any
// supported version. The metadata coder populates the typed-blob's
// Type and Hint; the Blob CoderTypeMapWithoutType then dispatches by
// Type to a version-specific body coder.
var Coder = hyphenceCoder[Blob]{
	RequireMetadata: true,
	Metadata:        receiptMetadataCoder{},
	Blob: hyphenceBlobCoders[Blob]{
		TypeStructV1.String(): v1BodyCoder{},
	},
}

// Read fetches the blob named by id from blobStore, parses it via
// Coder, and returns the populated Blob (currently always *V1) plus
// its type-tag.
func Read(
	blobStore domain_interfaces.BlobReaderFactory,
	id domain_interfaces.MarklId,
) (Blob, ids.TypeStruct, error) {
	reader, err := blobStore.MakeBlobReader(id)
	if err != nil {
		return nil, ids.TypeStruct{}, errors.Wrap(err)
	}

	defer errors.DeferredCloser(&err, reader)

	tb := &hyphenceBlob[Blob]{}

	if _, err = Coder.DecodeFrom(tb, reader); err != nil {
		return nil, tb.Type, errors.Wrap(err)
	}

	return tb.Blob, tb.Type, nil
}

// receiptMetadataCoder is the hyphence metadata coder for receipts.
// Reads the `! type` and (RFC 0001) `- store/<id> < <markl-id>`
// lines, populating typedBlob.Type and pre-allocating typedBlob.Blob
// so the version-specific body coder can attach the hint to its
// output.
type receiptMetadataCoder struct{}

var _ interfaces.CoderBufferedReadWriter[*hyphenceBlob[Blob]] = receiptMetadataCoder{}

func (receiptMetadataCoder) DecodeFrom(
	typedBlob *hyphenceBlob[Blob],
	bufferedReader *bufio.Reader,
) (n int64, err error) {
	var hint *StoreHint

	setHint := func(value string) error {
		// value is `<id> < <markl-id>` — value started after the first
		// space, so the prefix `store/` is part of value.
		if !strings.HasPrefix(value, "store/") {
			// Other `-` keys are tolerated per hyphence(7).
			return nil
		}
		rest := strings.TrimPrefix(value, "store/")

		const sep = " < "
		i := strings.Index(rest, sep)
		if i < 0 {
			return errors.ErrorWithStackf(
				"capture_receipt: malformed store-hint line: %q", value,
			)
		}

		hint = &StoreHint{
			StoreId:       rest[:i],
			ConfigMarklId: rest[i+len(sep):],
		}
		return nil
	}

	if n, err = format.ReadLines(
		bufferedReader,
		ohio.MakeLineReaderRepeat(
			ohio.MakeLineReaderKeyValues(
				map[string]interfaces.FuncSetString{
					"!": typedBlob.Type.Set,
					"-": setHint,
				},
			),
		),
	); err != nil {
		err = errors.Wrap(err)
		return n, err
	}

	// Per the dodder pattern: pre-populate the version-specific Blob
	// container so the body coder can stream into it. The dispatcher
	// looks at typedBlob.Type to pick which body coder runs.
	if typedBlob.Type == TypeStructV1 {
		typedBlob.Blob = &V1{Hint: hint}
	}

	return n, err
}

func (receiptMetadataCoder) EncodeTo(
	typedBlob *hyphenceBlob[Blob],
	bufferedWriter *bufio.Writer,
) (n int64, err error) {
	var hint *StoreHint
	if v1, ok := typedBlob.Blob.(*V1); ok && v1 != nil {
		hint = v1.Hint
	}

	if hint != nil {
		var n1 int
		n1, err = bufferedWriter.WriteString(
			"- store/" + hint.StoreId + " < " + hint.ConfigMarklId + "\n",
		)
		n += int64(n1)
		if err != nil {
			return n, errors.Wrap(err)
		}
	}

	var n1 int
	n1, err = bufferedWriter.WriteString(
		"! " + typedBlob.Type.StringSansOp() + "\n",
	)
	n += int64(n1)
	if err != nil {
		return n, errors.Wrap(err)
	}

	return n, nil
}
