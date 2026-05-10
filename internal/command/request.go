package command

import (
	"slices"

	"github.com/amarbel-llc/purse-first/libs/dewey/0/interfaces"
	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/charlie/flags"
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

func (req Request) LastArg() (arg string, ok bool) {
	if req.RemainingArgCount() > 0 {
		ok = true
		arg = req.PopArgs()[req.RemainingArgCount()-1]
	}
	return arg, ok
}

func (req Request) Must(fn func(interfaces.ActiveContext) error) {
	if err := fn(req.Context); err != nil {
		errors.ContextCancelWithError(req.Context, err)
	}
}

// Utility is forward-declared as a stub here so request.go compiles
// in isolation. The real type lands in Task 8 (utility.go); when that
// happens this stub is removed and the real one provides the same
// `GetName() string` surface that downstream code expects.
type Utility struct {
	name string
}

func (u Utility) GetName() string { return u.name }
