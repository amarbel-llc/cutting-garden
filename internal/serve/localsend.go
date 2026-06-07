package serve

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/amarbel-llc/cutting-garden/internal/capture_log"
	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/cutting-garden/internal/plugin_blob_io"
	"github.com/amarbel-llc/madder/go/pkgs/blob_stores"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// LocalSend protocol v2 route prefix. The receiver implements only the
// receive-side endpoints; discovery-by-multicast and the send side are
// out of scope (see FDR 0011).
const apiPrefix = "/api/localsend/v2"

// protocolVersion is advertised in our device-info JSON.
const protocolVersion = "2.0"

// maxPrepareUploadBody caps the prepare-upload request body. The body
// only declares file metadata (names, sizes, hashes) — 1 MiB is a
// generous ceiling that still stops a hostile sender from allocating
// an unbounded files map. File content on /upload is exempt: it is
// legitimately unbounded and streams into the blob store.
const maxPrepareUploadBody = 1 << 20

// deviceInfo is the LocalSend device descriptor exchanged by the info
// and register handshakes and echoed inside prepare-upload requests.
type deviceInfo struct {
	Alias       string `json:"alias"`
	Version     string `json:"version"`
	DeviceModel string `json:"deviceModel,omitempty"`
	DeviceType  string `json:"deviceType,omitempty"`
	Fingerprint string `json:"fingerprint"`
	Port        int    `json:"port,omitempty"`
	Protocol    string `json:"protocol,omitempty"`
	Download    bool   `json:"download"`
}

// fileMeta is one entry in a prepare-upload's file map. Only fileName
// and size are load-bearing for the capture; the rest is advisory
// metadata the sender supplies.
type fileMeta struct {
	ID       string `json:"id"`
	FileName string `json:"fileName"`
	Size     int64  `json:"size"`
	FileType string `json:"fileType,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	Preview  string `json:"preview,omitempty"`
}

type prepareUploadRequest struct {
	Info  deviceInfo          `json:"info"`
	Files map[string]fileMeta `json:"files"`
}

type prepareUploadResponse struct {
	SessionID string            `json:"sessionId"`
	Files     map[string]string `json:"files"` // fileId -> token
}

// sessionFile is one declared file's per-session state: the metadata
// the sender announced, the upload token we minted for it, and whether
// its upload has settled (succeeded or failed).
type sessionFile struct {
	meta  fileMeta
	token string
	done  bool
}

// session is one in-flight LocalSend transfer. LocalSend receivers
// handle a single session at a time, so the server holds at most one.
type session struct {
	id          string
	senderAlias string
	files       map[string]*sessionFile // fileId -> per-file state
	pending     int                     // files not yet settled
	entries     []capture_receipt.EntryV1
}

// server holds the receiver's immutable config plus the single active
// session, guarded by mu. The blob-write and receipt-write steps are
// injectable so the protocol flow can be exercised without a real store.
type server struct {
	info           deviceInfo
	captureLogPath string
	storeName      string // "" for the default store
	log            func(format string, args ...any)

	// writeBlob streams r into the destination store and returns the
	// content-addressed id plus byte count. Defaults to a blob_stores
	// write; overridable in tests.
	writeBlob func(ctx context.Context, r io.Reader) (id string, size int64, err error)

	// writeReceipt encodes the session's entries (dir entries already
	// synthesized) into a receipt blob and returns its id. Defaults to a
	// store write with a computed store-hint; overridable in tests.
	writeReceipt func(entries []capture_receipt.EntryV1) (id string, err error)

	mu      sync.Mutex
	current *session
}

// newServer wires the default store-backed blob/receipt writers around
// store. captureLogPath is where finalized receipts are journaled.
func newServer(
	store blob_stores.BlobStoreInitialized,
	storeName, effectiveStoreId, captureLogPath string,
	info deviceInfo,
	log func(format string, args ...any),
) *server {
	s := &server{
		info:           info,
		captureLogPath: captureLogPath,
		storeName:      storeName,
		log:            log,
	}
	s.writeBlob = func(c context.Context, r io.Reader) (string, int64, error) {
		id, size, err := plugin_blob_io.WriteReaderBlob(c, store, r)
		if err != nil {
			return "", 0, err
		}
		return id.String(), size, nil
	}
	// The store's config is immutable for the server's lifetime, so the
	// receipt store-hint is computed once here, not per finalized session.
	hint, hintErr := capture_receipt.ComputeStoreHint(store, effectiveStoreId)
	if hintErr != nil {
		log("notice: omitting store-hint for store=%s: %v",
			quoteEmpty(storeName), hintErr)
		hint = nil
	}
	s.writeReceipt = func(entries []capture_receipt.EntryV1) (string, error) {
		return capture_receipt.WriteV1ToStore(store, entries, hint)
	}
	return s
}

// handler builds the route mux. Kept separate from newServer so tests
// can mount it on an httptest.Server.
func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(apiPrefix+"/info", s.handleInfo)
	mux.HandleFunc(apiPrefix+"/register", s.handleRegister)
	mux.HandleFunc(apiPrefix+"/prepare-upload", s.handlePrepareUpload)
	mux.HandleFunc(apiPrefix+"/upload", s.handleUpload)
	mux.HandleFunc(apiPrefix+"/cancel", s.handleCancel)
	return mux
}

func (s *server) handleInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.info)
}

func (s *server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// The register handshake exchanges device info; we ignore the
	// sender's body (discovery bookkeeping is out of scope) and echo
	// ours so the sender can list us as a target.
	writeJSON(w, http.StatusOK, s.info)
}

func (s *server) handlePrepareUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req prepareUploadRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxPrepareUploadBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid prepare-upload body", http.StatusBadRequest)
		return
	}

	if len(req.Files) == 0 {
		// Nothing to transfer (e.g. a message-only request). 204 is the
		// protocol's "finished, no upload needed" response.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.current != nil {
		http.Error(w, "another transfer is in progress", http.StatusConflict)
		return
	}

	sess := &session{
		id:          randToken(),
		senderAlias: req.Info.Alias,
		files:       make(map[string]*sessionFile, len(req.Files)),
		pending:     len(req.Files),
	}
	resp := prepareUploadResponse{
		SessionID: sess.id,
		Files:     make(map[string]string, len(req.Files)),
	}
	for fileID, meta := range req.Files {
		token := randToken()
		sess.files[fileID] = &sessionFile{meta: meta, token: token}
		resp.Files[fileID] = token
	}

	s.current = sess
	s.log("session %s: %d file(s) from %s",
		sess.id, len(sess.files), quoteEmpty(sess.senderAlias))

	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	sessionID := q.Get("sessionId")
	fileID := q.Get("fileId")
	token := q.Get("token")
	if sessionID == "" || fileID == "" || token == "" {
		http.Error(w, "missing sessionId, fileId, or token",
			http.StatusBadRequest)
		return
	}

	// Validate the upload against the active session under the lock, but
	// stream the (potentially large) body OUTSIDE it so concurrent
	// uploads in the same session do not serialize on the mutex.
	s.mu.Lock()
	sess := s.current
	if sess == nil || sess.id != sessionID {
		s.mu.Unlock()
		http.Error(w, "unknown session", http.StatusForbidden)
		return
	}
	f := sess.files[fileID]
	if f == nil || f.token != token {
		s.mu.Unlock()
		http.Error(w, "invalid file or token", http.StatusForbidden)
		return
	}
	if f.done {
		s.mu.Unlock()
		http.Error(w, "file already uploaded", http.StatusConflict)
		return
	}
	s.mu.Unlock()

	cleanName, err := sanitizeFileName(f.meta.FileName)
	if err != nil {
		s.failFile(sessionID, fileID, "%s: %v", f.meta.FileName, err)
		http.Error(w, "unsafe file name", http.StatusBadRequest)
		return
	}

	id, size, err := s.writeBlob(r.Context(), r.Body)
	if err != nil {
		s.failFile(sessionID, fileID, "%s: blob write failed: %v", cleanName, err)
		http.Error(w, "blob write failed", http.StatusInternalServerError)
		return
	}

	entry := capture_receipt.EntryV1{
		Path:   cleanName,
		Root:   ".",
		Type:   capture_receipt.TypeFile,
		Mode:   0o644,
		Size:   size,
		BlobId: id,
	}

	// Re-acquire to record the entry and decide whether this was the
	// last file. A concurrent cancel may have dropped the session while
	// we were streaming — if so the bytes are already durable as a blob,
	// but there is no session to fold them into.
	s.mu.Lock()
	if s.current == nil || s.current.id != sessionID {
		s.mu.Unlock()
		http.Error(w, "session cancelled", http.StatusConflict)
		return
	}
	sess = s.current
	f = sess.files[fileID]
	if f.done {
		// Raced another upload of the same file id; keep the first.
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		return
	}
	f.done = true
	sess.pending--
	sess.entries = append(sess.entries, entry)
	s.log("session %s: received %s (%d bytes)", sess.id, cleanName, size)

	var done *session
	if sess.pending == 0 {
		done = sess
		s.current = nil
	}
	s.mu.Unlock()

	if done != nil {
		s.finalize(done, "complete")
	}

	w.WriteHeader(http.StatusOK)
}

func (s *server) handleCancel(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")

	s.mu.Lock()
	sess := s.current
	if sess == nil || (sessionID != "" && sess.id != sessionID) {
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		return
	}
	s.current = nil
	s.mu.Unlock()

	// Partial-receipt-on-abort: fold whatever files arrived before the
	// cancel into a receipt, mirroring capture's durability contract.
	s.finalize(sess, "cancelled")
	w.WriteHeader(http.StatusOK)
}

// failFile settles a single file in the active session without tearing
// down the whole session, and logs the reason. A later successful
// upload of the remaining files can still finalize; if the failed file
// was the LAST pending one, the session finalizes here — folding any
// files that did arrive — so the single-session slot is released
// (otherwise every future prepare-upload would 409 until an explicit
// cancel that the protocol does not require senders to issue).
func (s *server) failFile(sessionID, fileID, format string, args ...any) {
	s.mu.Lock()
	if s.current == nil || s.current.id != sessionID {
		s.mu.Unlock()
		return
	}
	var done *session
	if f := s.current.files[fileID]; f != nil && !f.done {
		f.done = true
		s.current.pending--
		if s.current.pending == 0 {
			done = s.current
			s.current = nil
		}
	}
	s.log("session %s: "+format, append([]any{sessionID}, args...)...)
	// finalize must run outside the lock, mirroring handleUpload.
	s.mu.Unlock()

	if done != nil {
		s.finalize(done, "failed")
	}
}

// finalize writes the session's receipt (with synthesized directory
// entries) and journals it. Empty sessions write nothing. Best-effort:
// failures log and return — the blobs are already durable.
func (s *server) finalize(sess *session, reason string) {
	if len(sess.entries) == 0 {
		s.log("session %s: %s, no files captured; receipt skipped",
			sess.id, reason)
		return
	}

	entries := withDirEntries(sess.entries)

	receiptID, err := s.writeReceipt(entries)
	if err != nil {
		s.log("session %s: receipt write failed: %v", sess.id, err)
		return
	}

	s.log("receipt store=%s id=%s count=%d (%s)",
		quoteEmpty(s.storeName), receiptID, len(entries), reason)

	capture_log.Append(s.captureLogPath, s.log, []capture_log.Entry{{
		Ts:        capture_log.Timestamp(),
		ReceiptID: receiptID,
		StoreID:   s.storeName,
		Roots:     []string{"localsend:" + quoteEmpty(sess.senderAlias)},
	}})
}

// sanitizeFileName coerces a sender-supplied LocalSend fileName into a
// safe relative receipt path. LocalSend sends directory trees as a flat
// file map whose fileName carries the relative path (slash-separated);
// a hostile sender could supply absolute paths or "../" escapes. Reject
// those outright rather than silently rewriting, so the receipt records
// exactly what was sent.
func sanitizeFileName(name string) (string, error) {
	if name == "" {
		return "", errors.ErrorWithStackf("empty file name")
	}
	if strings.ContainsRune(name, 0) {
		return "", errors.ErrorWithStackf("file name contains NUL byte")
	}
	// Normalize separators: LocalSend uses '/' but tolerate a stray
	// backslash from Windows senders by treating it as a path separator.
	name = strings.ReplaceAll(name, "\\", "/")
	if path.IsAbs(name) {
		return "", errors.ErrorWithStackf("absolute file name %q", name)
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." ||
		clean == "/" || strings.HasPrefix(clean, "../") {
		return "", errors.ErrorWithStackf("file name escapes destination: %q", name)
	}
	return clean, nil
}

// withDirEntries returns fileEntries plus a synthesized dir entry for
// every intermediate directory. LocalSend never sends directory records,
// but restore's file writes use O_CREATE|O_EXCL and need their parents
// to exist; entries sort by (Root, Path) so a dir precedes its children.
func withDirEntries(fileEntries []capture_receipt.EntryV1) []capture_receipt.EntryV1 {
	dirs := make(map[string]struct{})
	for _, e := range fileEntries {
		dir := path.Dir(e.Path)
		for dir != "." && dir != "/" && dir != "" {
			dirs[dir] = struct{}{}
			dir = path.Dir(dir)
		}
	}

	out := make([]capture_receipt.EntryV1, 0, len(fileEntries)+len(dirs))
	out = append(out, fileEntries...)
	for dir := range dirs {
		out = append(out, capture_receipt.EntryV1{
			Path: dir,
			Root: ".",
			Type: capture_receipt.TypeDir,
			Mode: 0o755,
		})
	}
	return out
}

// randToken returns a 128-bit hex token, used for both session ids and
// per-file upload tokens.
func randToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is catastrophic and unrecoverable; the
		// process cannot mint secure tokens. Panic rather than emit a
		// predictable one.
		panic("serve: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// quoteEmpty renders an empty alias/store-name as "(default)" for
// user-facing log lines, matching capture's convention.
func quoteEmpty(s string) string {
	if s == "" {
		return "(default)"
	}
	return s
}
