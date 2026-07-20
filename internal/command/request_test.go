package command

import (
	"testing"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/collections_slice"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/flags"
)

func newTestRequest(args ...string) Request {
	fs := flags.NewFlagSet("test", flags.ContinueOnError)
	_ = fs.Parse(args)
	return Request{
		FlagSet: fs,
		input: &CommandLineInput{
			FlagsOrArgs: collections_slice.String(args),
			Args:        collections_slice.String(args),
		},
	}
}

func TestRequest_RemainingArgCount(t *testing.T) {
	req := newTestRequest("a", "b", "c")
	if got := req.RemainingArgCount(); got != 3 {
		t.Errorf("RemainingArgCount = %d, want 3", got)
	}
}

func TestRequest_PopArg(t *testing.T) {
	req := newTestRequest("alpha", "beta")
	got := req.PopArg("first")
	if got != "alpha" {
		t.Errorf("PopArg = %q, want alpha", got)
	}
	if req.RemainingArgCount() != 1 {
		t.Errorf("RemainingArgCount after Pop = %d, want 1",
			req.RemainingArgCount())
	}
}

func TestRequest_PeekArgs(t *testing.T) {
	req := newTestRequest("a", "b")
	peek := req.PeekArgs()
	if len(peek) != 2 || peek[0] != "a" || peek[1] != "b" {
		t.Errorf("PeekArgs = %v, want [a b]", peek)
	}
	if req.RemainingArgCount() != 2 {
		t.Error("PeekArgs mutated remaining args")
	}
}

func TestRequest_LastArg_NonEmpty(t *testing.T) {
	req := newTestRequest("alpha", "beta")
	arg, ok := req.LastArg()
	if !ok || arg != "beta" {
		t.Errorf("LastArg = (%q, %v), want (beta, true)", arg, ok)
	}
}

func TestRequest_LastArg_Empty(t *testing.T) {
	req := newTestRequest()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("LastArg on empty args panicked: %v", r)
		}
	}()
	_, ok := req.LastArg()
	if ok {
		t.Error("LastArg on empty args returned ok=true")
	}
}
