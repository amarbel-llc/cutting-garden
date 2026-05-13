package command

import (
	"bytes"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
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
