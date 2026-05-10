package command

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

type completerCmd struct {
	emit []Completion
}

func (c completerCmd) Run(req Request) {}

func (c completerCmd) Complete(req Request, env any, cli CommandLineInput) {
	for _, comp := range c.emit {
		fmt.Fprintf(completeOut(), "%s\t%s\n", comp.Value, comp.Description)
	}
}

type plainCmd struct{}

func (plainCmd) Run(req Request) {}

func TestComplete_BareInvocation_ListsSubcommands(t *testing.T) {
	u := MakeUtility("test", nil)
	u.AddCmd("alpha", plainCmd{})
	u.AddCmd("beta", plainCmd{})

	var buf bytes.Buffer
	captureComplete(t, &u, &buf, []string{})

	out := buf.String()
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Errorf("bare complete did not list subcommands: %q", out)
	}
}

func TestComplete_PositionalDispatch_CallsCompleter(t *testing.T) {
	u := MakeUtility("test", nil)
	u.AddCmd("sub", completerCmd{
		emit: []Completion{{Value: "first", Description: "v1"}},
	})

	var buf bytes.Buffer
	captureComplete(t, &u, &buf, []string{"sub", ""})

	if !strings.Contains(buf.String(), "first") {
		t.Errorf("positional dispatch did not call Completer: %q", buf.String())
	}
}

func TestComplete_PositionalDispatch_NonCompleter_NoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("non-Completer subcommand panicked: %v", r)
		}
	}()
	u := MakeUtility("test", nil)
	u.AddCmd("plain", plainCmd{})
	var buf bytes.Buffer
	captureComplete(t, &u, &buf, []string{"plain", ""})
}

// captureComplete wires the complete subcommand against `u` and runs
// `complete <args...>`. It captures complete.go's stdout into buf via
// the testStdoutHook indirection.
func captureComplete(t *testing.T, u *Utility, buf *bytes.Buffer, args []string) {
	t.Helper()
	RegisterComplete(u)
	old := testStdoutHook
	testStdoutHook = buf
	defer func() { testStdoutHook = old }()
	full := append([]string{"test", "complete"}, args...)
	u.Run(full)
}
