package command_components

import (
	"bytes"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// isolateXDG points every XDG base dir at a per-test tempdir and opts
// out of madder's cwd walk-up resolution, so env construction never
// touches the developer's real madder scope.
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

// TestMakeBlobStoreEnvWithErr_RoutesErrSinkToWriter pins the madder#228
// contract this constructor exists for: an env built with a CustomErr
// writer routes its err sink — the sink MakeBlobStore derives the
// store-chatter printer from — to that writer instead of os.Stderr.
func TestMakeBlobStoreEnvWithErr_RoutesErrSinkToWriter(t *testing.T) {
	isolateXDG(t)
	ctx := errors.MakeContextDefault()

	var buf bytes.Buffer
	env := MakeBlobStoreEnvWithErr(ctx, &buf)

	const line = "# (blob_store: xyz) dialing sftp host\n"
	if _, err := fmt.Fprint(env.GetErrFile(), line); err != nil {
		t.Fatalf("writing through env err sink: %v", err)
	}

	if got := buf.String(); got != line {
		t.Errorf("err sink output = %q, want %q", got, line)
	}
}
