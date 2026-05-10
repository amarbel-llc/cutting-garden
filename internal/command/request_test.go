package command

import (
	"testing"

	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/collections_slice"
	"github.com/amarbel-llc/purse-first/libs/dewey/charlie/flags"
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
