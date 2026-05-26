package command

import "github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"

// SupportsCompletion is a marker interface a Cmd may implement to
// declare it offers shell completion candidates. Phase 1 doesn't yet
// dispatch on this — kept for parity with dodder's surface so the
// future complete-discovery code has a hook.
type SupportsCompletion interface {
	SupportsCompletion()
}

// Completion is a single (value, description) pair offered to the
// shell completion driver.
type Completion struct {
	Value, Description string
}

// Completer is implemented by commands and flag values that provide
// shell completions. The env parameter is application-specific —
// cutting-garden commands type-assert it to env_local.Env. Kept as
// `any` for framework portability (cf. dodder, which types this as
// env_local.Env directly).
type Completer interface {
	Complete(Request, any, CommandLineInput)
}

// FuncCompleter is the convenience func adapter for Completer.
type FuncCompleter func(Request, any, CommandLineInput)

// FlagValueCompleter wraps a flag.Value with a completion function so
// the same value participates in flag parsing AND tab completion.
type FlagValueCompleter struct {
	interfaces.FlagValue
	FuncCompleter
}

func (completer FlagValueCompleter) String() string {
	if completer.FlagValue == nil {
		return ""
	}
	return completer.FlagValue.String()
}

func (completer FlagValueCompleter) Complete(
	req Request,
	env any,
	commandLine CommandLineInput,
) {
	completer.FuncCompleter(req, env, commandLine)
}
