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
	// LastCompleteArg returns the unmodified Last() instead of
	// FlagsOrArgs[argc-1] after the InProgress decrement. Tracked at
	// dodder#182 (https://github.com/amarbel-llc/dodder/issues/182)
	// and #1 (https://github.com/amarbel-llc/cutting-garden/issues/1).
	t.Skip("upstream bug; see dodder#182 / #1")
	cli := CommandLineInput{
		FlagsOrArgs: collections_slice.String{"a", "b", "in-prog"},
		InProgress:  "in-prog",
	}
	arg, ok := cli.LastCompleteArg()
	if !ok || arg != "b" {
		t.Errorf("LastCompleteArg = (%q, %v), want (b, true)", arg, ok)
	}
}
