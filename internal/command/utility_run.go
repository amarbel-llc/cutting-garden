package command

import (
	"fmt"
	"os"
	"runtime"

	"github.com/amarbel-llc/purse-first/libs/dewey/0/interfaces"
	"github.com/amarbel-llc/purse-first/libs/dewey/bravo/errors"
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
// the appropriate exit code. Phase 1 callers ignore the return value
// (Utility.Run does not os.Exit, to keep the framework testable).
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
	return 1
}
