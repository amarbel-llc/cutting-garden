package capture_receipt

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strconv"

	"github.com/amarbel-llc/madder/go/pkgs/hyphence"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"
)

// HeaderV1 is the literal byte sequence that precedes the NDJSON body
// in every v1 receipt when no StoreHint is present. Derived from the
// hyphence boundary + type-tag line; exposed for tests that strip it
// to inspect just the body.
const HeaderV1 = hyphence.Boundary + "\n" +
	"! " + TypeTagV1 + "\n" +
	hyphence.Boundary + "\n\n"

// WriteV1 serializes entries as a hyphence-wrapped v1 receipt to w.
// Equivalent to WriteV1WithHint(w, entries, nil).
func WriteV1(w io.Writer, entries []EntryV1) (int64, error) {
	return WriteV1WithHint(w, entries, nil)
}

// WriteV1WithHint serializes entries as a hyphence-wrapped v1 receipt
// to w via the package's Coder, optionally prefixing a store-hint
// metadata line per RFC 0001 §Producer Rules §Receipt Metadata: Store
// Hint.
func WriteV1WithHint(w io.Writer, entries []EntryV1, hint *StoreHint) (int64, error) {
	tb := &hyphence.TypedBlob[Blob]{
		Type: TypeStructV1,
		Blob: &V1{Hint: hint, Entries: entries},
	}

	n, err := Coder.EncodeTo(tb, w)
	if err != nil {
		return n, errors.Wrap(err)
	}

	return n, nil
}

// v1BodyCoder is the version-specific blob coder for v1 receipts.
// CoderTypeMapWithoutType dispatches to it when the metadata pass
// reports typedBlob.Type == TypeStructV1.
//
// On decode, the metadata coder has already populated *typedBlob.Blob
// with a *V1 carrying the optional Hint; this coder streams NDJSON
// entries from the bufferedReader into (*V1).Entries.
//
// On encode, the metadata coder has already emitted the type and
// hint lines; this coder streams sorted NDJSON entries from
// (*V1).Entries.
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
			var rec recordV1
			if jerr := json.Unmarshal(line, &rec); jerr != nil {
				return n, errors.Wrapf(jerr,
					"capture_receipt: decode body line: %q", line)
			}

			entry, derr := decodeV1Record(rec)
			if derr != nil {
				return n, errors.Wrap(derr)
			}

			v1.Entries = append(v1.Entries, entry)
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
			"capture_receipt: v1BodyCoder.EncodeTo: expected *V1, got %T", *blobPtr)
	}

	entries := append([]EntryV1(nil), v1.Entries...)
	sortEntries(entries)

	for i := range entries {
		var line []byte
		line, err = encodeV1Entry(entries[i])
		if err != nil {
			return n, errors.Wrap(err)
		}

		var n1 int
		n1, err = bufferedWriter.Write(line)
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

// recordV1 is the on-disk JSON shape for one entry in
// cutting_garden-capture_receipt-fs-v1. omitempty keeps file-only fields out
// of dir/symlink records and vice versa.
type recordV1 struct {
	Path   string `json:"path"`
	Root   string `json:"root"`
	Type   string `json:"type"`
	Mode   string `json:"mode"`
	Size   int64  `json:"size,omitempty"`
	BlobId string `json:"blob_id,omitempty"`
	Target string `json:"target,omitempty"`
}

func encodeV1Entry(e EntryV1) ([]byte, error) {
	rec := recordV1{
		Path: e.Path,
		Root: e.Root,
		Type: e.Type,
		Mode: fmt.Sprintf("%04o", e.Mode.Perm()),
	}

	switch e.Type {
	case TypeFile:
		rec.Size = e.Size
		rec.BlobId = e.BlobId

	case TypeSymlink:
		rec.Target = e.Target

	case TypeDir, TypeOther:
		// no extra fields

	default:
		return nil, errors.ErrorWithStackf(
			"capture_receipt: unknown entry type %q (path=%q root=%q)",
			e.Type, e.Path, e.Root,
		)
	}

	body, err := json.Marshal(rec)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return append(body, '\n'), nil
}

func decodeV1Record(rec recordV1) (EntryV1, error) {
	mode, err := strconv.ParseUint(rec.Mode, 8, 32)
	if err != nil {
		return EntryV1{}, errors.Wrapf(err, "capture_receipt: parse mode %q", rec.Mode)
	}

	entry := EntryV1{
		Path: rec.Path,
		Root: rec.Root,
		Type: rec.Type,
		Mode: fs.FileMode(mode),
	}

	switch rec.Type {
	case TypeFile:
		entry.Size = rec.Size
		entry.BlobId = rec.BlobId

	case TypeSymlink:
		entry.Target = rec.Target

	case TypeDir, TypeOther:
		// no extra fields

	default:
		return EntryV1{}, errors.ErrorWithStackf(
			"capture_receipt: unknown entry type %q (path=%q root=%q)",
			rec.Type, rec.Path, rec.Root,
		)
	}

	return entry, nil
}

// sortEntries sorts in place by (Root, Path). Producer-side
// determinism: equivalent inputs yield byte-identical receipts.
func sortEntries(entries []EntryV1) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Root != entries[j].Root {
			return entries[i].Root < entries[j].Root
		}
		return entries[i].Path < entries[j].Path
	})
}
