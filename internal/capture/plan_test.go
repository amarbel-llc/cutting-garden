package capture

import (
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/capture_failures"
	"code.linenisgreat.com/cutting-garden/internal/capture_receipt"
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/plugin_blob_io"
	"code.linenisgreat.com/madder/go/pkgs/scoped_id"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// fakeSchemelessCapture stands in for the file plugin's "", "file" capture
// registration so this package's tests exercise the ORCHESTRATOR — arg
// classification, grouping, receipt and failure-receipt writing, the
// captures.log entry — without an internal/ test depending on plugins/
// sources (docs/plans/2026-09-21-invalidation-cone-moves.md D6/D7, pinned by
// internal/sdklayering). The real file plugin's walking semantics (symlinks,
// permissions, .gitignore, cancellation) are its own package's tests plus
// zz-tests_bats/capture.bats.
//
// It is a deliberately minimal tree walker rather than a no-op: the
// end-to-end Run tests below need a plugin that actually stores blobs and
// reports a per-entry blob-write failure, and a stub returning no entries
// would make them pass vacuously ("no entries captured; receipt skipped").
type fakeSchemelessCapture struct{}

func (fakeSchemelessCapture) Schemes() []string { return []string{"", "file"} }

func (fakeSchemelessCapture) TypeTag() string {
	return capture_receipt.TypeTagV1
}

func (fakeSchemelessCapture) ValidateSource(u *url.URL, raw string) error {
	if info, err := os.Lstat(fakeWalkPath(u)); err != nil {
		return errors.Wrap(err)
	} else if !info.IsDir() {
		return errors.ErrorWithStackf("%q is not a directory", raw)
	}
	return nil
}

// fakeWalkPath is the plugin-side URL → filesystem path mapping: a
// schemeless arg lands in Path, `file:dir` in Opaque.
func fakeWalkPath(u *url.URL) string {
	if u.Path != "" {
		return u.Path
	}
	return u.Opaque
}

func (fakeSchemelessCapture) CaptureRoot(
	req cutting_garden_plugins.CaptureRootRequest,
) cutting_garden_plugins.CaptureRootResult {
	stream := cutting_garden_plugins.ReporterOrNop(req.Reporter)
	dir := fakeWalkPath(req.Source)
	var res cutting_garden_plugins.CaptureRootResult

	fail := func(path, op string, err error) {
		stream.Failure(path, err)
		res.Failures = append(res.Failures, capture_failures.FailureV1{
			Root:  req.RawArg,
			Path:  path,
			Op:    op,
			Error: err.Error(),
		})
	}

	_ = filepath.WalkDir(dir, func(
		path string, entry fs.DirEntry, walkErr error,
	) error {
		if walkErr != nil {
			fail(path, capture_failures.OpStat, walkErr)
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			fail(path, capture_failures.OpStat, err)
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			fail(path, capture_failures.OpStat, err)
			return nil
		}

		record := capture_receipt.EntryV1{
			Path: filepath.ToSlash(rel),
			Root: req.RawArg,
			Mode: info.Mode(),
			Type: capture_receipt.TypeDir,
		}
		if info.Mode().IsRegular() {
			id, size, err := plugin_blob_io.WriteFileBlob(
				req.Context, req.BlobStore, path,
			)
			if err != nil {
				fail(path, capture_failures.OpBlobWrite, err)
				return nil
			}
			record.Type = capture_receipt.TypeFile
			record.Size = size
			record.BlobId = id.String()
		}

		stream.Entry(record)
		res.Entries = append(res.Entries, record)
		return nil
	})

	res.FailCount = len(res.Failures)
	return res
}

func init() {
	cutting_garden_plugins.MustRegisterCapture(fakeSchemelessCapture{})
}

// setupFS chdirs into a fresh temp directory and creates a fixed set of
// children. classifyArg's Lstat-first heuristic is sensitive to the
// filesystem, so the fixture exists per-test.
//   - dir-a, dir-b, dir-c : directories that classify as argKindCapture
//   - not-a-dir           : regular file that classifies as argKindError
//
// store-a / store-b in tests use scoped_id.Id.Set; their string form
// does not collide with any path in the fixture.
func setupFS(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Chdir(tmp)
	for _, d := range []string{"dir-a", "dir-b", "dir-c"} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile("not-a-dir", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustParseStoreId(t *testing.T, s string) scoped_id.Id {
	t.Helper()
	var id scoped_id.Id
	if err := id.Set(s); err != nil {
		t.Fatalf("scoped_id.Set(%q): %v", s, err)
	}
	return id
}

func TestClassifyArg(t *testing.T) {
	setupFS(t)

	tests := []struct {
		name        string
		arg         string
		wantKind    argKind
		wantURLPath string // expected sourceURL.Path; "" skips the check
		wantErrSubs string // expected substring of err.Error(); "" skips
	}{
		{
			name:        "ExistingDir",
			arg:         "dir-a",
			wantKind:    argKindCapture,
			wantURLPath: "dir-a",
		},
		{
			name:        "ExistingDirTrailingSlashCleansURL",
			arg:         "dir-a/",
			wantKind:    argKindCapture,
			wantURLPath: "dir-a",
		},
		{
			name:        "ExistingFileIsError",
			arg:         "not-a-dir",
			wantKind:    argKindError,
			wantErrSubs: "not a directory",
		},
		{
			name:     "ENOENT_NameClassifiesAsStoreId",
			arg:      "missing-store",
			wantKind: argKindStoreId,
		},
		{
			// id.Set("...") errors with "all dots", but classifyArg
			// reports the canonical "neither URI, dir, nor store-id"
			// message — matches madder's behavior.
			name:        "AllDotsIsError",
			arg:         "...",
			wantKind:    argKindError,
			wantErrSubs: "neither a recognized URI",
		},
		{
			// Same canonical-message pattern: id.Set("") errors with
			// "empty scoped_id" but classifyArg surfaces the
			// generic message.
			name:        "EmptyStringIsError",
			arg:         "",
			wantKind:    argKindError,
			wantErrSubs: "neither a recognized URI",
		},
		{
			name:        "FileSchemeURL",
			arg:         "file:dir-a",
			wantKind:    argKindCapture,
			wantURLPath: "", // file:dir-a → u.Opaque == "dir-a", u.Path == ""
		},
		{
			// Unknown schemes still fall through to the schemeless
			// heuristic (Lstat for colon-bearing filenames that exist),
			// but since madder#227 a blob-store-id name is strictly
			// [a-zA-Z0-9_-] — a colon can never appear in one — so a
			// nonexistent unknown-scheme arg now classifies as an
			// error rather than a phantom store-id.
			name:        "UnknownSchemeNonexistentIsError",
			arg:         "unknown:missing-store",
			wantKind:    argKindError,
			wantErrSubs: "neither a recognized URI",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyArg(tt.arg)
			if got.kind != tt.wantKind {
				t.Fatalf("kind = %d, want %d (err=%v)", got.kind, tt.wantKind, got.err)
			}
			if tt.wantURLPath != "" {
				if got.sourceURL == nil {
					t.Fatalf("nil sourceURL")
				}
				if got.sourceURL.Path != tt.wantURLPath {
					t.Fatalf("sourceURL.Path = %q, want %q", got.sourceURL.Path, tt.wantURLPath)
				}
			}
			if tt.wantErrSubs != "" {
				if got.err == nil {
					t.Fatalf("nil err, want containing %q", tt.wantErrSubs)
				}
				if !strings.Contains(got.err.Error(), tt.wantErrSubs) {
					t.Fatalf("err = %q, want containing %q", got.err.Error(), tt.wantErrSubs)
				}
			}
		})
	}
}

func TestPlanCapture(t *testing.T) {
	setupFS(t)

	storeA := mustParseStoreId(t, "store-a")
	storeB := mustParseStoreId(t, "store-b")
	candidates := []scoped_id.Id{storeA, storeB}

	t.Run("EmptyArgs_ImplicitDot", func(t *testing.T) {
		groups, fails, err := planCapture(nil, candidates)
		if err != nil {
			t.Fatal(err)
		}
		if len(fails) != 0 {
			t.Errorf("fails: %v", fails)
		}
		if len(groups) != 1 || len(groups[0].roots) != 1 {
			t.Fatalf("shape: %+v", groups)
		}
		if !groups[0].storeID.IsEmpty() {
			t.Errorf("expected empty storeID, got %v", groups[0].storeID)
		}
		if groups[0].roots[0].path != "." {
			t.Errorf("root.path = %q, want %q", groups[0].roots[0].path, ".")
		}
	})

	t.Run("SingleDir_OneGroupOneRoot", func(t *testing.T) {
		groups, fails, err := planCapture([]string{"dir-a"}, candidates)
		if err != nil || len(fails) != 0 {
			t.Fatalf("err=%v fails=%v", err, fails)
		}
		if len(groups) != 1 || len(groups[0].roots) != 1 {
			t.Fatalf("shape: %+v", groups)
		}
		if !groups[0].storeID.IsEmpty() {
			t.Errorf("expected empty storeID")
		}
		if groups[0].roots[0].path != "dir-a" {
			t.Errorf("root.path = %q", groups[0].roots[0].path)
		}
	})

	t.Run("SingleStoreId_ImplicitDotRootWithSwitchNotice", func(t *testing.T) {
		groups, _, err := planCapture([]string{"store-a"}, candidates)
		if err != nil {
			t.Fatal(err)
		}
		if len(groups) != 1 || len(groups[0].roots) != 1 {
			t.Fatalf("shape: %+v", groups)
		}
		if groups[0].switchNotice == "" {
			t.Errorf("expected non-empty switchNotice")
		}
		if groups[0].roots[0].path != "." {
			t.Errorf("root.path = %q, want %q", groups[0].roots[0].path, ".")
		}
	})

	t.Run("MultiDir_SameStoreOneGroup", func(t *testing.T) {
		groups, _, err := planCapture([]string{"dir-a", "dir-b"}, candidates)
		if err != nil {
			t.Fatal(err)
		}
		if len(groups) != 1 || len(groups[0].roots) != 2 {
			t.Fatalf("shape: %+v", groups)
		}
	})

	t.Run("DirStoreDir_TwoGroups", func(t *testing.T) {
		groups, _, err := planCapture(
			[]string{"dir-a", "store-b", "dir-b"},
			candidates,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(groups) != 2 {
			t.Fatalf("expected 2 groups, got %d", len(groups))
		}
		if !groups[0].storeID.IsEmpty() {
			t.Errorf("group 0 storeID should be empty")
		}
		if groups[1].storeID.String() != "store-b" {
			t.Errorf("group 1 storeID = %q", groups[1].storeID.String())
		}
		if groups[1].switchNotice == "" {
			t.Errorf("group 1 missing switchNotice")
		}
	})

	t.Run("LeadingStoreId_OneGroupWithSwitch", func(t *testing.T) {
		groups, _, err := planCapture(
			[]string{"store-a", "dir-a"},
			candidates,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(groups) != 1 {
			t.Fatalf("groups: %d", len(groups))
		}
		if groups[0].storeID.String() != "store-a" {
			t.Errorf("storeID = %q", groups[0].storeID.String())
		}
		if groups[0].switchNotice == "" {
			t.Errorf("missing switchNotice")
		}
	})

	t.Run("TrailingStoreId_FatalErr", func(t *testing.T) {
		_, _, err := planCapture(
			[]string{"dir-a", "store-a"},
			candidates,
		)
		if err == nil {
			t.Fatalf("expected fatal err for trailing store-id")
		}
		if !strings.Contains(err.Error(), "no following directories") {
			t.Errorf("err = %q", err.Error())
		}
	})

	t.Run("TwoStoreIdsBackToBack_FatalErr", func(t *testing.T) {
		_, _, err := planCapture(
			[]string{"store-a", "store-b"},
			candidates,
		)
		if err == nil {
			t.Fatalf("expected fatal err for back-to-back store-ids")
		}
		if !strings.Contains(err.Error(), "no following directories") {
			t.Errorf("err = %q", err.Error())
		}
	})

	t.Run("RootCollision_FatalErr", func(t *testing.T) {
		_, _, err := planCapture(
			[]string{"dir-a", "dir-a/"},
			candidates,
		)
		if err == nil {
			t.Fatalf("expected collision error")
		}
		if !strings.Contains(err.Error(), "resolve to") {
			t.Errorf("err = %q", err.Error())
		}
	})

	t.Run("MultiRoot_TrailingSlashCleanedNoCollision", func(t *testing.T) {
		// dir-a/ and dir-b/ clean to dir-a and dir-b — distinct.
		groups, _, err := planCapture(
			[]string{"dir-a/", "dir-b/"},
			candidates,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(groups) != 1 || len(groups[0].roots) != 2 {
			t.Fatalf("shape: %+v", groups)
		}
		// Original path preserved verbatim on captureRoot.path.
		if groups[0].roots[0].path != "dir-a/" {
			t.Errorf("root[0].path = %q, want %q (original arg preserved)",
				groups[0].roots[0].path, "dir-a/")
		}
		// Cleaned path on sourceURL.Path — this is what the file plugin walks.
		if groups[0].roots[0].sourceURL.Path != "dir-a" {
			t.Errorf("root[0].sourceURL.Path = %q, want %q (cleaned)",
				groups[0].roots[0].sourceURL.Path, "dir-a")
		}
		if groups[0].roots[1].path != "dir-b/" {
			t.Errorf("root[1].path = %q, want %q", groups[0].roots[1].path, "dir-b/")
		}
		if groups[0].roots[1].sourceURL.Path != "dir-b" {
			t.Errorf("root[1].sourceURL.Path = %q, want %q",
				groups[0].roots[1].sourceURL.Path, "dir-b")
		}
	})

	t.Run("AllBadArgs_FailsButNoFatalErr", func(t *testing.T) {
		// per madder: fatal err triggers only when groups==0 AND fails==0.
		// All-bad still populates fails, so no fatal err — the sink path
		// will surface the per-arg failures.
		_, fails, err := planCapture([]string{"...", "..."}, candidates)
		if err != nil {
			t.Errorf("unexpected fatal err: %v", err)
		}
		if len(fails) != 2 {
			t.Errorf("fails len = %d, want 2", len(fails))
		}
	})

	t.Run("MixedBadAndGood_PartialSuccess", func(t *testing.T) {
		groups, fails, err := planCapture(
			[]string{"...", "dir-a"},
			candidates,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(fails) != 1 {
			t.Errorf("fails len = %d, want 1", len(fails))
		}
		if len(groups) != 1 || len(groups[0].roots) != 1 {
			t.Fatalf("shape: %+v", groups)
		}
	})

	t.Run("NoUsableArgs_FatalErrWhenNoFails", func(t *testing.T) {
		// A single arg that classifies as argKindError takes a special
		// path that DOES set a fatal err. Verify that path.
		_, fails, err := planCapture([]string{"..."}, candidates)
		if err == nil {
			t.Fatalf("expected fatal err on single-arg argKindError")
		}
		if len(fails) != 1 {
			t.Errorf("fails len = %d, want 1", len(fails))
		}
	})
}

func TestCheckRootCollisions(t *testing.T) {
	t.Run("DistinctRoots", func(t *testing.T) {
		if err := checkRootCollisions([]captureRoot{
			{path: "./foo"},
			{path: "./bar"},
		}); err != nil {
			t.Errorf("unexpected err: %v", err)
		}
	})

	t.Run("CleanedSame", func(t *testing.T) {
		err := checkRootCollisions([]captureRoot{
			{path: "./foo"},
			{path: "./foo/"},
		})
		if err == nil {
			t.Errorf("expected collision error")
		}
	})

	t.Run("EmptyRoots", func(t *testing.T) {
		if err := checkRootCollisions(nil); err != nil {
			t.Errorf("unexpected err on nil: %v", err)
		}
	})

	t.Run("URI_VS_SchemelessAliasCollision", func(t *testing.T) {
		// file:dir-a and dir-a both resolve to the same filesystem path
		// via the file plugin. Comparing raw paths would miss this;
		// canonicalRootKey compares the cleaned sourceURL form.
		err := checkRootCollisions([]captureRoot{
			{path: "file:dir-a", sourceURL: &url.URL{Scheme: "file", Opaque: "dir-a"}},
			{path: "dir-a/", sourceURL: &url.URL{Path: "dir-a"}},
		})
		if err == nil {
			t.Errorf("expected collision error for cross-scheme alias")
		}
	})
}
