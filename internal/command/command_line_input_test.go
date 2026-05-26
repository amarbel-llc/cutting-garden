package command

import (
	"testing"

	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/collections_slice"
)

func TestCommandLineInput_LastArg(t *testing.T) {
	cli := CommandLineInput{FlagsOrArgs: collections_slice.String{"a", "b", "c"}}
	arg, ok := cli.LastArg()
	if !ok || arg != "c" {
		t.Errorf("LastArg = (%q, %v), want (c, true)", arg, ok)
	}
}

func TestCommandLineInput_LastArg_Empty(t *testing.T) {
	cli := CommandLineInput{}
	_, ok := cli.LastArg()
	if ok {
		t.Error("LastArg on empty FlagsOrArgs returned ok=true")
	}
}

func TestCommandLineInput_LastCompleteArg_StripsInProgress(t *testing.T) {
	cli := CommandLineInput{
		FlagsOrArgs: collections_slice.String{"a", "b", "in-prog"},
		InProgress:  "in-prog",
	}
	arg, ok := cli.LastCompleteArg()
	if !ok || arg != "b" {
		t.Errorf("LastCompleteArg = (%q, %v), want (b, true)", arg, ok)
	}
}

func TestCommandLineInput_LastCompleteArg_NoInProgress(t *testing.T) {
	cli := CommandLineInput{FlagsOrArgs: collections_slice.String{"a", "b"}}
	arg, ok := cli.LastCompleteArg()
	if !ok || arg != "b" {
		t.Errorf("LastCompleteArg = (%q, %v), want (b, true)", arg, ok)
	}
}

func TestCommandLineInput_LastCompleteArg_OnlyInProgress(t *testing.T) {
	cli := CommandLineInput{
		FlagsOrArgs: collections_slice.String{"in-prog"},
		InProgress:  "in-prog",
	}
	_, ok := cli.LastCompleteArg()
	if ok {
		t.Error("LastCompleteArg with only in-progress token returned ok=true")
	}
}

func TestCommandLineInput_LastCompleteArg_Empty(t *testing.T) {
	cli := CommandLineInput{}
	_, ok := cli.LastCompleteArg()
	if ok {
		t.Error("LastCompleteArg on empty input returned ok=true")
	}
}

func TestCommandLineInput_CompleteArgs_StripsInProgress(t *testing.T) {
	cli := CommandLineInput{
		FlagsOrArgs: collections_slice.String{"a", "b", "in-prog"},
		InProgress:  "in-prog",
	}
	got := cli.CompleteArgs()
	want := collections_slice.String{"a", "b"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("CompleteArgs = %v, want %v", got, want)
	}
}

func TestCommandLineInput_CompleteArgs_NoInProgress(t *testing.T) {
	cli := CommandLineInput{FlagsOrArgs: collections_slice.String{"a", "b"}}
	got := cli.CompleteArgs()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("CompleteArgs = %v, want [a b]", got)
	}
}

func TestCommandLineInput_CompleteArgs_OnlyInProgress(t *testing.T) {
	cli := CommandLineInput{
		FlagsOrArgs: collections_slice.String{"in-prog"},
		InProgress:  "in-prog",
	}
	got := cli.CompleteArgs()
	if len(got) != 0 {
		t.Errorf("CompleteArgs = %v, want []", got)
	}
}
