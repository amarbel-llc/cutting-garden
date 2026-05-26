package command

import (
	"slices"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/flags"
)

type Request struct {
	Utility Utility
	Context interfaces.ActiveContext
	FlagSet *flags.FlagSet
	input   *CommandLineInput
}

func (req Request) RemainingArgCount() int {
	if req.input == nil {
		return 0
	}
	return req.input.Args.Len() - req.input.Argi
}

func (req Request) PopArg(name string) string {
	if req.input == nil || req.input.Argi >= req.input.Args.Len() {
		errors.ContextCancelWithBadRequestf(req.Context,
			"missing argument %q", name)
		return ""
	}
	v := req.input.Args[req.input.Argi]
	req.input.Argi++
	req.input.consumed = append(req.input.consumed,
		consumedArg{name: name, value: v})
	return v
}

func (req Request) PopArgs() []string {
	if req.input == nil {
		return nil
	}
	rest := slices.Clone(req.input.Args[req.input.Argi:])
	req.input.Argi = req.input.Args.Len()
	return rest
}

func (req Request) PeekArgs() []string {
	if req.input == nil {
		return nil
	}
	return slices.Clone(req.input.Args[req.input.Argi:])
}

// LastArg returns the final remaining positional arg and consumes
// every remaining arg (mirrors dodder semantics: LastArg is
// destructive). Use PeekArgs() if you need a non-destructive view.
//
// Diverges from dodder HEAD's implementation, which panics by
// evaluating PopArgs() (mutating Argi to the end) and then indexing
// at RemainingArgCount()-1, which is -1. Tracked at dodder#183
// (https://github.com/amarbel-llc/dodder/issues/183).
func (req Request) LastArg() (arg string, ok bool) {
	if req.RemainingArgCount() == 0 {
		return arg, ok
	}
	rest := req.PopArgs()
	return rest[len(rest)-1], true
}

func (req Request) Must(fn func(interfaces.ActiveContext) error) {
	if err := fn(req.Context); err != nil {
		errors.ContextCancelWithError(req.Context, err)
	}
}
