package command

import (
	"runtime"
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
	u.Run([]string{"test", "greet", "alice", "bob"})
	if len(c.receivedArgs) != 2 || c.receivedArgs[0] != "alice" {
		t.Errorf("dispatch did not deliver args: got %v", c.receivedArgs)
	}
}

func TestUtility_Run_NoArgs_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Run with no args panicked: %v", r)
		}
	}()
	u := MakeUtility("test", nil)
	u.Run([]string{"test"})
}

func TestUtility_Run_UnknownSubcommand_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Run with unknown subcommand panicked: %v", r)
		}
	}()
	u := MakeUtility("test", nil)
	u.Run([]string{"test", "does-not-exist"})
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
