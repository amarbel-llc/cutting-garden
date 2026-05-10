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
	if errors.Is400BadRequest(err) {
		// PrintUsage already wrote to stderr; don't double-render.
		return 64 // EX_USAGE
	}
	fmt.Fprintf(os.Stderr, "%s: %s\n", utilityName, err)
	return 1
}
