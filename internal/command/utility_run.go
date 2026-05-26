package command

import (
	"fmt"
	"os"
	"runtime"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

func extendNameIfNecessary(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// userFacingErrorMessage walks past ErrorHiddenWrapper layers (notably
// dewey's `errWithoutStack` and the `http` status wrapper that backs
// BadRequestf/ConflictWrapf/etc.) so a CLI user sees the actual
// message instead of "errors.HTTP: 400 Bad Request".
//
// Workaround pending amarbel-llc/purse-first#107 — once dewey's HTTP
// status errors carry status as semantics rather than identity, this
// helper collapses to err.Error().
func userFacingErrorMessage(err error) string {
	for {
		hidden, ok := err.(interfaces.ErrorHiddenWrapper)
		if !ok || !hidden.ShouldHideUnwrap() {
			return err.Error()
		}
		underlying := hidden.Unwrap()
		if underlying == nil {
			return err.Error()
		}
		err = underlying
	}
}

// handleMainErrors formats a fatal error for the user and returns
// the appropriate exit code. cmd/cutting-garden/main.go propagates
// the returned code via os.Exit; Utility.Run itself never does so
// (kept side-effect-light for tests).
//
// Exit codes mirror diff(1) / git --exit-code:
//
//	0   success (handled by Utility.Run, not here)
//	1   clean mismatch — *command.MismatchError in the chain
//	2   trouble — any other error
//	64  EX_USAGE — errors.Is400BadRequest
func handleMainErrors(
	ctx interfaces.ActiveContext,
	utilityName string,
	err error,
) int {
	if err == nil {
		return 0
	}
	fmt.Fprintf(os.Stderr, "%s: %s\n", utilityName, userFacingErrorMessage(err))
	if errors.Is400BadRequest(err) {
		return 64 // EX_USAGE
	}
	var mismatch *MismatchError
	if errors.As(err, &mismatch) {
		return 1
	}
	return 2
}
