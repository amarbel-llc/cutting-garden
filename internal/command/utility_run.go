package command

import (
	"fmt"
	"os"
	"runtime"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/interfaces"
)

func extendNameIfNecessary(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
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
	// err.Error() carries the user-facing message directly since dewey's
	// RFC 0002 (purse-first#107): HTTP-status errors render their
	// underlying message, with the status carried as semantics via
	// errors.As. The pre-RFC hidden-unwrap walking (userFacingErrorMessage)
	// is gone.
	fmt.Fprintf(os.Stderr, "%s: %s\n", utilityName, err)
	if errors.Is400BadRequest(err) {
		return 64 // EX_USAGE
	}
	var mismatch *MismatchError
	if errors.As(err, &mismatch) {
		return 1
	}
	return 2
}
