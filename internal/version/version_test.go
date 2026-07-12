package version

import (
	"bytes"
	"io"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/buildinfo"
	"code.linenisgreat.com/cutting-garden/internal/command"
)

// driveVersion dispatches the version subcommand through a fresh Utility
// (flag parsing included) with output routed to out, returning the exit
// code. Mirrors health_test.driveHealth.
func driveVersion(t *testing.T, out io.Writer, args ...string) int {
	t.Helper()
	u := command.MakeUtility("cg-test", nil)
	u.AddCmd("version", newWithOutput(out))
	return u.Run(append([]string{"cg-test", "version"}, args...))
}

func TestRun_PrintsSelfLine(t *testing.T) {
	var buf bytes.Buffer
	if code := driveVersion(t, &buf); code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, buf.String())
	}
	// Under `go test` there are no ldflags, so buildinfo carries its
	// defaults; the line is "<name> <version>+<commit>".
	want := progName + " " + buildinfo.String() + "\n"
	if got := buf.String(); got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}

func TestRun_TrailingArgIsUsageError(t *testing.T) {
	var buf bytes.Buffer
	if code := driveVersion(t, &buf, "extra"); code != 64 {
		t.Fatalf("exit = %d, want 64 (EX_USAGE)", code)
	}
}
