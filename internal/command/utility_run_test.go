package command

import (
	"bytes"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

type capturingCmd struct {
	receivedArgs []string
}

func (c *capturingCmd) Run(req Request) {
	c.receivedArgs = req.PopArgs()
}

func TestUtility_Run_DispatchesToRegisteredCmd(t *testing.T) {
	u := MakeUtility("test", nil)
	c := &capturingCmd{}
	u.AddCmd("greet", c)
	code := u.Run([]string{"test", "greet", "alice", "bob"})
	if code != 0 {
		t.Errorf("expected exit code 0 on success, got %d", code)
	}
	if len(c.receivedArgs) != 2 || c.receivedArgs[0] != "alice" {
		t.Errorf("dispatch did not deliver args: got %v", c.receivedArgs)
	}
}

func TestUtility_Run_NoArgs_ReturnsEXUSAGE(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Run with no args panicked: %v", r)
		}
	}()
	u := MakeUtility("test", nil)
	code := u.Run([]string{"test"})
	if code != 64 {
		t.Errorf("expected EX_USAGE (64) for no-args, got %d", code)
	}
}

func TestUtility_Run_UnknownSubcommand_ReturnsEXUSAGE(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Run with unknown subcommand panicked: %v", r)
		}
	}()
	u := MakeUtility("test", nil)
	code := u.Run([]string{"test", "does-not-exist"})
	if code != 64 {
		t.Errorf("expected EX_USAGE (64) for unknown subcommand, got %d", code)
	}
}

func captureStderr(fn func()) string {
	r, w, _ := os.Pipe()
	old := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = old }()
	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()
	fn()
	w.Close()
	<-done
	return buf.String()
}

func TestUtility_Run_NoArgs_NoDoubleErrorLine(t *testing.T) {
	out := captureStderr(func() {
		u := MakeUtility("test", nil)
		u.Run([]string{"test"})
	})
	if !strings.Contains(out, "Usage for test") {
		t.Errorf("expected usage banner, got %q", out)
	}
	if strings.Contains(out, "errors.HTTP") || strings.Contains(out, "400 Bad Request") {
		t.Errorf("usage banner should NOT be followed by an error line, got %q", out)
	}
}

func TestUtility_Run_UnknownSubcommand_NoDoubleErrorLine(t *testing.T) {
	out := captureStderr(func() {
		u := MakeUtility("test", nil)
		u.Run([]string{"test", "no-such-subcmd"})
	})
	if strings.Contains(out, "errors.HTTP") || strings.Contains(out, "400 Bad Request") {
		t.Errorf("usage banner should NOT be followed by an error line, got %q", out)
	}
}

// Pins for #15: diff(1)-style exit-code distinction. The framework
// distinguishes "clean mismatch" (exit 1, *MismatchError in the
// chain) from "trouble" (exit 2, any other error) on top of the
// preexisting BadRequest → 64 path.

type mismatchCmd struct{}

func (mismatchCmd) Run(req Request) {
	errors.ContextCancelWithError(req.Context, Mismatchf("intentional mismatch"))
}

type troubleCmd struct{}

func (troubleCmd) Run(req Request) {
	errors.ContextCancelWithError(req.Context, troubleErr{})
}

type troubleErr struct{}

func (troubleErr) Error() string { return "intentional trouble" }

func TestUtility_Run_MismatchError_ExitsOne(t *testing.T) {
	u := MakeUtility("test", nil)
	u.AddCmd("mismatch", mismatchCmd{})
	code := u.Run([]string{"test", "mismatch"})
	if code != 1 {
		t.Errorf("expected exit 1 for MismatchError, got %d", code)
	}
}

func TestUtility_Run_NonMismatchError_ExitsTwo(t *testing.T) {
	u := MakeUtility("test", nil)
	u.AddCmd("trouble", troubleCmd{})
	code := u.Run([]string{"test", "trouble"})
	if code != 2 {
		t.Errorf("expected exit 2 for non-mismatch error, got %d", code)
	}
}

// Regression for #35: handleMainErrors must surface the user-facing
// BadRequest message on stderr, not just exit silently with code 64.
// The message is wrapped behind dewey's HTTP error / errWithoutStack
// hidden-unwrap layers; userFacingErrorMessage must walk past them.

func TestUtility_Run_NoArgs_PrintsErrorMessage(t *testing.T) {
	out := captureStderr(func() {
		u := MakeUtility("test", nil)
		u.Run([]string{"test"})
	})
	if !strings.Contains(out, "No subcommand provided") {
		t.Errorf("expected stderr to contain BadRequest message, got %q", out)
	}
}

func TestUtility_Run_UnknownSubcommand_PrintsErrorMessage(t *testing.T) {
	out := captureStderr(func() {
		u := MakeUtility("test", nil)
		u.Run([]string{"test", "no-such-subcmd"})
	})
	if !strings.Contains(out, `No subcommand "no-such-subcmd"`) {
		t.Errorf("expected stderr to name the missing subcommand, got %q", out)
	}
}

func TestUtility_PrintUsage_DescribedSortedFilterComplete(t *testing.T) {
	// PrintUsage shares the userFacingSubcommands helper with
	// GenerateUtilityManpage: sorted alphabetically, `complete`
	// filtered, short description appended when the cmd implements
	// CommandWithDescription. Pins the shape so users don't see the
	// pre-polish "list of bare names" output.
	out := captureStderr(func() {
		u := MakeUtility("test", nil)
		RegisterComplete(&u)
		u.AddCmd("zulu", describedCmd{short: "do zulu things"})
		u.AddCmd("alpha", describedCmd{short: "do alpha things"})
		u.Run([]string{"test"})
	})

	if !strings.Contains(out, "Usage for test") {
		t.Errorf("expected usage banner, got %q", out)
	}
	if !strings.Contains(out, "do alpha things") ||
		!strings.Contains(out, "do zulu things") {
		t.Errorf("short descriptions missing, got %q", out)
	}
	if strings.Contains(out, "complete") {
		t.Errorf("complete subcommand leaked into usage, got %q", out)
	}
	alphaIdx := strings.Index(out, "alpha")
	zuluIdx := strings.Index(out, "zulu")
	if alphaIdx < 0 || zuluIdx < 0 || alphaIdx > zuluIdx {
		t.Errorf("subcommands not sorted alphabetically, got %q", out)
	}
}

func TestExtendNameIfNecessary(t *testing.T) {
	got := extendNameIfNecessary("foo")
	if got == "" {
		t.Error("extendNameIfNecessary returned empty string")
	}
	if runtime.GOOS == "windows" && got != "foo.exe" {
		t.Errorf("on Windows expected foo.exe, got %q", got)
	}
	if runtime.GOOS != "windows" && got != "foo" {
		t.Errorf("on %s expected foo, got %q", runtime.GOOS, got)
	}
}
