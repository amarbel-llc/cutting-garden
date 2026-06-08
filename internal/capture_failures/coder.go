// Coder + version-dispatching machinery for failure receipts.
//
// Mirrors internal/capture_receipt/coder.go (itself the dodder
// pattern): a hyphence.CoderToTypedBlob[Blob] whose Metadata coder
// populates the typed-blob's Type during the metadata pass, and whose
// Blob dispatcher (CoderTypeMapWithoutType) selects a per-version body
// coder based on that Type during the body pass. No buffering — the
// body decoder streams from the bufio.Reader hyphence hands it.
//
// The metadata coder also consumes the `- key value` lines (ts,
// outcome, signal, receipt, root…, captured, failed), pre-allocating a
// *V1 with the captured Meta set on it so the body coder for TypeTagV1
// can stream NDJSON failures directly into the existing struct.
package capture_failures

import (
	"bufio"
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"github.com/amarbel-llc/madder/go/pkgs/hyphence"
	"github.com/amarbel-llc/madder/go/pkgs/ids"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/format"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/ohio"
)

// maxErrorBytes caps each FailureV1.Error at encode time. Tuning
// lever (design doc §Tuning levers): keeps lines greppable while
// preserving wrapped-error chains.
const maxErrorBytes = 1024

// TypeStructV1 is the wire type-id that appears on the `! ` line of a
// v1 failure receipt. Stored as ids.TypeStruct so it can compare
// directly with typedBlob.Type at dispatch time.
var TypeStructV1 = ids.MustTypeStruct(TypeTagV1)

// Coder decodes and encodes hyphence-wrapped failure receipts of any
// supported version. The metadata coder populates the typed-blob's
// Type and Meta; the Blob CoderTypeMapWithoutType then dispatches by
// Type to a version-specific body coder.
var Coder = hyphence.CoderToTypedBlob[Blob]{
	RequireMetadata: true,
	Metadata:        failuresMetadataCoder{},
	Blob: hyphence.CoderTypeMapWithoutType[Blob]{
		TypeStructV1.String(): v1BodyCoder{},
	},
}

// failuresMetadataCoder is the hyphence metadata coder for failure
// receipts. Reads the `! type` and `- key value` lines, populating
// typedBlob.Type and pre-allocating typedBlob.Blob with the captured
// Meta so the version-specific body coder can stream into it.
type failuresMetadataCoder struct{}

var _ interfaces.CoderBufferedReadWriter[*hyphence.TypedBlob[Blob]] = failuresMetadataCoder{}

func (failuresMetadataCoder) DecodeFrom(
	typedBlob *hyphence.TypedBlob[Blob],
	bufferedReader *bufio.Reader,
) (n int64, err error) {
	var meta Meta

	setMeta := func(value string) error {
		// value is `<key> <rest>` — value started after the first
		// space of the `- ` line.
		key, rest, _ := strings.Cut(value, " ")

		switch key {
		case "ts":
			meta.Ts = rest
		case "outcome":
			meta.Outcome = rest
		case "signal":
			meta.Signal = rest
		case "receipt":
			meta.Receipt = rest
		case "root":
			meta.Roots = append(meta.Roots, rest)
		case "captured":
			v, perr := strconv.ParseInt(rest, 10, 64)
			if perr != nil {
				return errors.Wrapf(perr,
					"capture_failures: parse captured %q", rest)
			}
			meta.Captured = v
		case "failed":
			v, perr := strconv.ParseInt(rest, 10, 64)
			if perr != nil {
				return errors.Wrapf(perr,
					"capture_failures: parse failed %q", rest)
			}
			meta.Failed = v
		default:
			// Other `-` keys are tolerated per hyphence(7).
		}

		return nil
	}

	if n, err = format.ReadLines(
		bufferedReader,
		ohio.MakeLineReaderRepeat(
			ohio.MakeLineReaderKeyValues(
				map[string]interfaces.FuncSetString{
					"!": typedBlob.Type.Set,
					"-": setMeta,
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
		typedBlob.Blob = &V1{Meta: meta}
	}

	return n, err
}

func (failuresMetadataCoder) EncodeTo(
	typedBlob *hyphence.TypedBlob[Blob],
	bufferedWriter *bufio.Writer,
) (n int64, err error) {
	v1, ok := typedBlob.Blob.(*V1)
	if !ok {
		return 0, errors.ErrorWithStackf(
			"capture_failures: metadata EncodeTo: expected *V1, got %T",
			typedBlob.Blob,
		)
	}

	meta := v1.Meta

	// Fixed line order for byte-stable output: ts, outcome, signal
	// (omitted when empty), receipt (omitted when empty), one root
	// line per root, captured, failed.
	lines := []string{
		"- ts " + meta.Ts,
		"- outcome " + meta.Outcome,
	}
	if meta.Signal != "" {
		lines = append(lines, "- signal "+meta.Signal)
	}
	if meta.Receipt != "" {
		lines = append(lines, "- receipt "+meta.Receipt)
	}
	for _, root := range meta.Roots {
		lines = append(lines, "- root "+root)
	}
	lines = append(
		lines,
		"- captured "+strconv.FormatInt(meta.Captured, 10),
		"- failed "+strconv.FormatInt(meta.Failed, 10),
		"! "+typedBlob.Type.StringSansOp(),
	)

	for _, line := range lines {
		var n1 int
		n1, err = bufferedWriter.WriteString(line + "\n")
		n += int64(n1)
		if err != nil {
			return n, errors.Wrap(err)
		}
	}

	return n, nil
}

// v1BodyCoder is the version-specific blob coder for v1 failure
// receipts. CoderTypeMapWithoutType dispatches to it when the metadata
// pass reports typedBlob.Type == TypeStructV1.
//
// On decode, the metadata coder has already populated *typedBlob.Blob
// with a *V1 carrying the Meta; this coder streams NDJSON failures
// from the bufferedReader into (*V1).Failures.
//
// On encode, the metadata coder has already emitted the `- key value`
// and type lines; this coder streams NDJSON failures from
// (*V1).Failures in their recorded order, truncating each Error to
// maxErrorBytes.
type v1BodyCoder struct{}

var _ interfaces.CoderBufferedReadWriter[*Blob] = v1BodyCoder{}

func (v1BodyCoder) DecodeFrom(
	blobPtr *Blob,
	bufferedReader *bufio.Reader,
) (n int64, err error) {
	v1, ok := (*blobPtr).(*V1)
	if !ok {
		v1 = &V1{}
		*blobPtr = v1
	}

	for {
		var line []byte
		line, err = readNDJSONLine(bufferedReader)
		n += int64(len(line))

		if err != nil && err != io.EOF {
			return n, errors.Wrap(err)
		}

		if len(line) > 0 {
			var failure FailureV1
			if jerr := json.Unmarshal(line, &failure); jerr != nil {
				return n, errors.Wrapf(jerr,
					"capture_failures: decode body line: %q", line)
			}

			v1.Failures = append(v1.Failures, failure)
		}

		if err == io.EOF {
			err = nil
			return n, nil
		}
	}
}

func (v1BodyCoder) EncodeTo(
	blobPtr *Blob,
	bufferedWriter *bufio.Writer,
) (n int64, err error) {
	v1, ok := (*blobPtr).(*V1)
	if !ok {
		return 0, errors.ErrorWithStackf(
			"capture_failures: v1BodyCoder.EncodeTo: expected *V1, got %T",
			*blobPtr,
		)
	}

	for _, failure := range v1.Failures {
		if len(failure.Error) > maxErrorBytes {
			failure.Error = failure.Error[:maxErrorBytes]
		}

		var line []byte
		line, err = json.Marshal(failure)
		if err != nil {
			return n, errors.Wrap(err)
		}

		var n1 int
		n1, err = bufferedWriter.Write(append(line, '\n'))
		n += int64(n1)
		if err != nil {
			return n, errors.Wrap(err)
		}
	}

	return n, nil
}

// readNDJSONLine reads one '\n'-terminated line from br, returning the
// bytes (without the trailing '\n'). io.EOF is returned when the
// stream is exhausted; the caller checks for and tolerates a final
// non-empty line without a trailing newline.
func readNDJSONLine(br *bufio.Reader) ([]byte, error) {
	line, err := br.ReadBytes('\n')

	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}

	return line, err
}
