package command

import "testing"

type completerFunc func(Request, any, CommandLineInput)

func (f completerFunc) Complete(req Request, env any, cli CommandLineInput) {
	f(req, env, cli)
}

var _ Completer = completerFunc(nil)

func TestCompleter_Implementable(t *testing.T) {
	called := false
	c := completerFunc(func(req Request, env any, cli CommandLineInput) {
		called = true
	})
	c.Complete(Request{}, nil, CommandLineInput{})
	if !called {
		t.Error("Completer.Complete was not invoked")
	}
}

func TestFlagValueCompleter_NilFlagValue_StringEmpty(t *testing.T) {
	fvc := FlagValueCompleter{}
	if got := fvc.String(); got != "" {
		t.Errorf("nil FlagValue String() = %q, want empty", got)
	}
}

func TestCompletion_Fields(t *testing.T) {
	c := Completion{Value: "v", Description: "d"}
	if c.Value != "v" || c.Description != "d" {
		t.Errorf("Completion roundtrip failed: %+v", c)
	}
}
