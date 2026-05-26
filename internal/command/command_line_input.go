package command

import (
	"fmt"

	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/collections_slice"
)

// TODO complete merging Args, consumed and FlagsOrArgs for use by Run/Complete
type CommandLineInput struct {
	FlagsOrArgs          collections_slice.String
	InProgress           string
	ContainsDoubleHyphen bool

	Args collections_slice.String
	Argi int

	consumed collections_slice.Slice[consumedArg]
}

type consumedArg struct {
	name, value string
}

func (arg consumedArg) String() string {
	if arg.name == "" {
		return fmt.Sprintf("%q", arg.value)
	}
	return fmt.Sprintf("%s:%q", arg.name, arg.value)
}

func (commandLine CommandLineInput) LastArg() (arg string, ok bool) {
	argc := commandLine.FlagsOrArgs.Len()
	if argc > 0 {
		ok = true
		arg = commandLine.FlagsOrArgs.Last()
	}
	return arg, ok
}

// CompleteArgs returns the fully-typed arguments — every element of
// FlagsOrArgs except the trailing in-progress token (when one is set).
// Shell completion stubs pass the partial token both as the last
// element of FlagsOrArgs and as the InProgress flag; completers that
// want only the context of what the user has *finished* typing should
// start here.
//
// When InProgress is empty, this is FlagsOrArgs unchanged. When it is
// set, the last element of FlagsOrArgs is dropped.
func (commandLine CommandLineInput) CompleteArgs() collections_slice.String {
	if commandLine.InProgress == "" {
		return commandLine.FlagsOrArgs
	}
	n := commandLine.FlagsOrArgs.Len()
	if n == 0 {
		return commandLine.FlagsOrArgs
	}
	return commandLine.FlagsOrArgs[:n-1]
}

// LastCompleteArg returns the last fully-typed argument — the element
// before any in-progress token. Convenience wrapper over CompleteArgs
// for the common case of "what did the user just finish typing?".
func (commandLine CommandLineInput) LastCompleteArg() (arg string, ok bool) {
	complete := commandLine.CompleteArgs()
	if complete.Len() == 0 {
		return "", false
	}
	return complete.Last(), true
}
