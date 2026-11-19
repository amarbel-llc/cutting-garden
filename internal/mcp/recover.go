package mcp

import (
	"fmt"
	"os"
	"runtime/debug"
)

// recoverCall, deferred at the top of a request handler, turns a panic in the
// handler (in practice a plugin bug) into an error for that one request and
// logs its stack to stderr. go-mcp runs each request on its own goroutine and
// does not recover, so without this one bad call ends the whole process: with
// the shared serve-mcp daemon, every session's tools at once.
func recoverCall(what string, err *error) {
	if r := recover(); r != nil {
		fmt.Fprintf(os.Stderr, "cutting-garden: %s panicked: %v\n%s\n", what, r, debug.Stack())
		*err = fmt.Errorf("%s panicked: %v", what, r)
	}
}
