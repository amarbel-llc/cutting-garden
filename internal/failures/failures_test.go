package failures

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/capture_failures"
	"code.linenisgreat.com/cutting-garden/internal/command"
	"code.linenisgreat.com/cutting-garden/internal/command_components"
	"github.com/amarbel-llc/madder/go/pkgs/blob_store_configs"
	"github.com/amarbel-llc/madder/go/pkgs/directory_layout"
	"github.com/amarbel-llc/madder/go/pkgs/ids"

	// Blank-import the markl purpose registrations: EncodeWithDigest in
	// initDefaultBlobStore digests the store config under the
	// "madder-blob_store-config-digest-v1" purpose, which panics unless
	// registered (the production binaries get this via cgapp's
	// blank-import).
	_ "github.com/amarbel-llc/madder/go/pkgs/markl_registrations"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// isolateXDG points every XDG base dir at a per-test tempdir and opts
// out of madder's cwd walk-up resolution, so env construction never
// touches the developer's real madder scope. Replicates
// internal/capture's test helper of the same name (package-private
// there).
func isolateXDG(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	for _, v := range []string{
		"XDG_DATA_HOME",
		"XDG_CONFIG_HOME",
		"XDG_STATE_HOME",
		"XDG_CACHE_HOME",
		"XDG_RUNTIME_DIR",
	} {
		t.Setenv(v, filepath.Join(tmp, v))
	}
	t.Setenv("MADDER_XDG_USER_LOCATION_ONLY", "1")
}

// initDefaultBlobStore writes a minimal local hash-bucketed store
// config into the isolated madder XDG scope so store-backed tests have
// a real default store to read failure receipts from. Replicates
// internal/capture's helper. Call after isolateXDG.
func initDefaultBlobStore(t *testing.T) {
	t.Helper()
	storeDir := filepath.Join(
		os.Getenv("XDG_DATA_HOME"), "madder", "blob_stores", "default",
	)
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	file, err := os.Create(
		filepath.Join(storeDir, directory_layout.FileNameBlobStoreConfig),
	)
	if err != nil {
		t.Fatal(err)
	}

	typed := &blob_store_configs.TypedConfig{
		Type: ids.GetOrPanic(ids.TypeTomlBlobStoreConfigV3).TypeStruct,
		Blob: &blob_store_configs.TomlV3{
			HashTypeId:      "sha256",
			HashBuckets:     []int{2},
			CompressionType: "none",
		},
	}
	if _, err := blob_store_configs.EncodeWithDigest(typed, file); err != nil {
		t.Fatalf("encode blob_store-config: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

// seedFailureReceipt writes v into the isolated scope's default blob
// store and returns its content-addressed markl id.
func seedFailureReceipt(t *testing.T, v *capture_failures.V1) string {
	t.Helper()
	ctx := errors.MakeContextDefault()
	env := command_components.MakeBlobStoreEnv(ctx)
	id, err := capture_failures.WriteV1ToStore(env.GetDefaultBlobStore(), v)
	if err != nil {
		t.Fatalf("seed failure receipt: %v", err)
	}
	return id
}

// driveFailures dispatches the failures subcommand through a fresh
// Utility (flag parsing included) with body output routed to out,
// returning the exit code. Mirrors internal/restore's makeUtility
// pattern, plus the newWithOutput injection.
func driveFailures(t *testing.T, out io.Writer, args ...string) int {
	t.Helper()
	u := command.MakeUtility("cg-test", nil)
	u.AddCmd("failures", newWithOutput(out))
	return u.Run(append([]string{"cg-test", "failures"}, args...))
}

func sampleFailuresV1() *capture_failures.V1 {
	return &capture_failures.V1{
		Meta: capture_failures.Meta{
			Ts:       "2026-06-07T12:00:00Z",
			Outcome:  capture_failures.OutcomeFailures,
			Receipt:  "sha256-pairedreceipt",
			Roots:    []string{"./", "other/"},
			Captured: 6018,
			Failed:   2,
		},
		Failures: []capture_failures.FailureV1{
			{
				Root: "./", Path: "a/b.ts", Op: capture_failures.OpBlobWrite,
				Error: "read: permission denied",
			},
			{
				Root: "./", Path: "c.txt", Op: capture_failures.OpStat,
				Error: "stale handle",
			},
		},
	}
}

func TestRun_TextGolden(t *testing.T) {
	isolateXDG(t)
	initDefaultBlobStore(t)
	id := seedFailureReceipt(t, sampleFailuresV1())

	var buf bytes.Buffer
	code := driveFailures(t, &buf, id)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; out=%q", code, buf.String())
	}

	want := "outcome: failures\n" +
		"receipt: sha256-pairedreceipt\n" +
		"roots: ./ other/\n" +
		"captured: 6018  failed: 2\n" +
		"blob-write\t./\ta/b.ts\tread: permission denied\n" +
		"stat\t./\tc.txt\tstale handle\n"
	if got := buf.String(); got != want {
		t.Errorf("text output:\ngot  %q\nwant %q", got, want)
	}
}

func TestRun_TextGolden_AbortedWithSignal(t *testing.T) {
	isolateXDG(t)
	initDefaultBlobStore(t)
	id := seedFailureReceipt(t, &capture_failures.V1{
		Meta: capture_failures.Meta{
			Ts:       "2026-06-07T12:00:00Z",
			Outcome:  capture_failures.OutcomeAborted,
			Signal:   "interrupt",
			Receipt:  "", // success-receipt write failed
			Roots:    []string{"./"},
			Captured: 3,
			Failed:   1,
		},
		Failures: []capture_failures.FailureV1{
			{
				Root: "./", Path: "sub/d.txt", Op: capture_failures.OpWalk,
				Error: "context canceled",
			},
		},
	})

	var buf bytes.Buffer
	code := driveFailures(t, &buf, id)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; out=%q", code, buf.String())
	}

	want := "outcome: aborted (interrupt)\n" +
		"receipt: (none)\n" +
		"roots: ./\n" +
		"captured: 3  failed: 1\n" +
		"walk\t./\tsub/d.txt\tcontext canceled\n"
	if got := buf.String(); got != want {
		t.Errorf("text output:\ngot  %q\nwant %q", got, want)
	}
}

// TestRun_JSONRoundTrip pins -format json as body-only NDJSON: each
// line unmarshals back into a FailureV1 and the slice round-trips; no
// metadata lines appear.
func TestRun_JSONRoundTrip(t *testing.T) {
	isolateXDG(t)
	initDefaultBlobStore(t)
	seed := sampleFailuresV1()
	id := seedFailureReceipt(t, seed)

	var buf bytes.Buffer
	code := driveFailures(t, &buf, "-format=json", id)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; out=%q", code, buf.String())
	}

	var got []capture_failures.FailureV1
	for i, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		var f capture_failures.FailureV1
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			t.Fatalf("line %d is not JSON: %v; line=%q", i, err, line)
		}
		got = append(got, f)
	}
	if !reflect.DeepEqual(got, seed.Failures) {
		t.Errorf("NDJSON round-trip:\ngot  %+v\nwant %+v", got, seed.Failures)
	}
}

func TestRun_NoArgs_MissingId(t *testing.T) {
	isolateXDG(t)
	var buf bytes.Buffer
	code := driveFailures(t, &buf)
	if code != 64 {
		t.Errorf("expected EX_USAGE (64) for no positional args, got %d", code)
	}
}

func TestRun_TwoArgs_TooManyArgs(t *testing.T) {
	isolateXDG(t)
	var buf bytes.Buffer
	code := driveFailures(t, &buf, "blake2b256-deadbeef", "extra-arg")
	if code != 64 {
		t.Errorf("expected EX_USAGE (64) for trailing arg, got %d", code)
	}
}

func TestRun_InvalidFormatIsUsageError(t *testing.T) {
	isolateXDG(t)
	var buf bytes.Buffer
	code := driveFailures(t, &buf, "-format=yaml", "blake2b256-deadbeef")
	if code != 64 {
		t.Errorf("expected EX_USAGE (64) for bad -format, got %d", code)
	}
	if buf.Len() != 0 {
		t.Errorf("output should be empty on usage error, got %q", buf.String())
	}
}

// TestRun_BogusIdIsTrouble: a string that fails markl parsing reaches
// the runtime path (not flag/arg validation), so the error is plain
// trouble — exit 2, distinct from 64 (EX_USAGE).
func TestRun_BogusIdIsTrouble(t *testing.T) {
	isolateXDG(t)
	var buf bytes.Buffer
	code := driveFailures(t, &buf, "blake2b256-deadbeef")
	if code != 2 {
		t.Errorf("expected exit 2 (trouble) for bogus id, got %d", code)
	}
}

func TestDescription(t *testing.T) {
	desc := New().GetDescription()
	if !strings.Contains(desc.Short, "failure") {
		t.Errorf("Short should mention 'failure'; got %q", desc.Short)
	}
	if !strings.Contains(desc.Long, capture_failures.TypeTagV1) {
		t.Errorf("Long should name the %s type tag; got %q",
			capture_failures.TypeTagV1, desc.Long)
	}
}
