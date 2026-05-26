// Package capture_sink carries the per-entry result stream for
// `cutting-garden capture`. Each filesystem entry becomes one TAP test
// point or one NDJSON record; each store group ends with a summary
// (TAP `ok` test point or NDJSON `store_group_receipt` record). Notices
// (store switches, shadow warnings) and per-arg failures use the
// dedicated methods.
//
// blob_write_sink covers the `madder write` event shape; capture's
// per-entry events carry filesystem metadata that doesn't fit there, so
// it gets its own sink.
package capture_sink

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"

	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/madder/go/pkgs/tap_diagnostics"
	tap "github.com/amarbel-llc/tap/go/pkgs/writer"
)

// summaryRecordType is the wire `type` value the NDJSON sink writes on
// the per-store-group summary record. Filesystem entry types ("file",
// "dir", "symlink", "other") are deliberately distinct so a single
// `type` field discriminates entries from summaries.
const summaryRecordType = "store_group_receipt"

// Sink streams capture results in either TAP or NDJSON form. Each
// concrete sink is single-threaded; capture's walk is sequential.
type Sink interface {
	// SetStore sets the store-id stamped onto subsequent Entry and
	// StoreGroupReceipt records. Called once per store group before its
	// entries are emitted. Empty string means the default store.
	SetStore(store string)

	// Entry reports one filesystem entry that was successfully captured
	// under the active store.
	Entry(e capture_receipt.EntryV1)

	// StoreGroupReceipt reports the receipt blob produced for the
	// active store group, after every entry under that group has been
	// emitted.
	StoreGroupReceipt(receiptID string, count int)

	// Notice reports informational events (store switches, shadow
	// warnings). TAP renders as a comment; NDJSON routes to stderr.
	// Signature mirrors fmt.Printf — callers that already hold a
	// pre-formatted message should pass "%s" as the first arg.
	Notice(format string, args ...any)

	// Failure reports a per-source error (capture-root missing, file
	// open failure, copy failure, etc.).
	Failure(source string, err error)

	// Finalize is called once after the last store group has been
	// emitted. TAP emits the plan; NDJSON flushes the buffered writer.
	Finalize()
}

// NewTAP constructs a TAP sink writing to w. Caller must invoke
// Finalize to emit the plan.
func NewTAP(w io.Writer) Sink {
	return &tapSink{tw: tap.NewWriter(w)}
}

// NewNDJSON constructs an NDJSON sink. out is the record stream;
// errOut receives Notice messages. The record stream is buffered;
// Finalize flushes.
func NewNDJSON(out, errOut io.Writer) Sink {
	buf := bufio.NewWriter(out)
	return &jsonSink{
		buf:    buf,
		enc:    json.NewEncoder(buf),
		errOut: errOut,
	}
}

type tapSink struct {
	tw    *tap.Writer
	store string
}

func (s *tapSink) SetStore(store string) { s.store = store }

func (s *tapSink) Entry(e capture_receipt.EntryV1) {
	s.tw.Ok(formatTAPEntry(e))
}

func (s *tapSink) StoreGroupReceipt(receiptID string, count int) {
	s.tw.Ok(fmt.Sprintf(
		"receipt store=%s id=%s count=%d",
		quoteIfEmpty(s.store), receiptID, count,
	))
}

func (s *tapSink) Notice(format string, args ...any) {
	s.tw.Comment(format, args...)
}

func (s *tapSink) Failure(source string, err error) {
	s.tw.NotOk(source, tap_diagnostics.FromError(err))
}

func (s *tapSink) Finalize() {
	s.tw.Plan()
}

func formatTAPEntry(e capture_receipt.EntryV1) string {
	rel := joinRootPath(e.Root, e.Path)
	mode := fmt.Sprintf("%04o", e.Mode.Perm())

	switch e.Type {
	case capture_receipt.TypeFile:
		return fmt.Sprintf("%s file mode=%s size=%d blob=%s", rel, mode, e.Size, e.BlobId)
	case capture_receipt.TypeDir:
		return fmt.Sprintf("%s dir mode=%s", rel, mode)
	case capture_receipt.TypeSymlink:
		return fmt.Sprintf("%s symlink mode=%s target=%s", rel, mode, e.Target)
	default:
		return fmt.Sprintf("%s %s mode=%s", rel, e.Type, mode)
	}
}

// joinRootPath formats Root+Path for human-readable TAP output. The
// receipt itself stores them separately for parser clarity.
func joinRootPath(root, path string) string {
	if path == "." || path == "" {
		return root
	}
	return root + "/" + path
}

func quoteIfEmpty(s string) string {
	if s == "" {
		return `""`
	}
	return s
}

type jsonSink struct {
	buf    *bufio.Writer
	enc    *json.Encoder
	errOut io.Writer
	store  string
}

// entryRecord is the wire shape of one captured entry on the NDJSON
// stream. Mirrors capture_receipt's recordV1 (so consumers can
// share a parser) and adds `store`. Source/Error are populated only
// for failure records, which omit the entry fields.
type entryRecord struct {
	Path   string `json:"path,omitempty"`
	Root   string `json:"root,omitempty"`
	Type   string `json:"type,omitempty"`
	Mode   string `json:"mode,omitempty"`
	Size   int64  `json:"size,omitempty"`
	BlobId string `json:"blob_id,omitempty"`
	Target string `json:"target,omitempty"`
	Store  string `json:"store,omitempty"`
	Source string `json:"source,omitempty"`
	Error  string `json:"error,omitempty"`
}

type summaryRecord struct {
	Type      string `json:"type"`
	Store     string `json:"store"`
	ReceiptID string `json:"receipt_id"`
	Count     int    `json:"count"`
}

func (s *jsonSink) SetStore(store string) { s.store = store }

func (s *jsonSink) Entry(e capture_receipt.EntryV1) {
	rec := entryRecord{
		Path:  e.Path,
		Root:  e.Root,
		Type:  e.Type,
		Mode:  fmt.Sprintf("%04o", e.Mode.Perm()),
		Store: s.store,
	}

	switch e.Type {
	case capture_receipt.TypeFile:
		rec.Size = e.Size
		rec.BlobId = e.BlobId
	case capture_receipt.TypeSymlink:
		rec.Target = e.Target
	}

	_ = s.enc.Encode(rec)
}

func (s *jsonSink) StoreGroupReceipt(receiptID string, count int) {
	_ = s.enc.Encode(summaryRecord{
		Type:      summaryRecordType,
		Store:     s.store,
		ReceiptID: receiptID,
		Count:     count,
	})
}

func (s *jsonSink) Notice(format string, args ...any) {
	fmt.Fprintln(s.errOut, fmt.Sprintf(format, args...))
}

func (s *jsonSink) Failure(source string, err error) {
	_ = s.enc.Encode(entryRecord{
		Source: source,
		Error:  err.Error(),
	})
}

func (s *jsonSink) Finalize() {
	_ = s.buf.Flush()
}
