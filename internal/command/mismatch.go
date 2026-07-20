package command

import "code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"

// MismatchError signals a clean command-level mismatch — the command
// ran to completion and reported a non-empty difference, not a fault
// that prevented completion. handleMainErrors picks this up via
// errors.As and maps it to diff(1)-style exit code 1 ("ran fine,
// found differences") instead of the default 2 ("trouble — could not
// run to completion").
type MismatchError struct {
	Underlying error
}

// Mismatchf builds a *MismatchError carrying a freshly-formatted
// stack-traced error as its underlying message.
func Mismatchf(format string, args ...any) *MismatchError {
	return &MismatchError{Underlying: errors.ErrorWithStackf(format, args...)}
}

func (err *MismatchError) Error() string { return err.Underlying.Error() }

func (err *MismatchError) Unwrap() error { return err.Underlying }
