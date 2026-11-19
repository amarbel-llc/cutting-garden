package mcp

import (
	"context"
	"fmt"
)

// Caller identifies the connection a serve-mcp session came in on, for the
// write log: with one daemon serving every session, the process's own stderr
// no longer says which session made a write.
//
// This is an AUDIT AID, not authentication. Principal is SELF-ASSERTED: the
// bridge announces whatever session id its environment carries, and any
// process of the same user that can reach the socket can announce any other.
// PID and UID are the kernel's answer for the connection's peer
// (SO_PEERCRED), reliable where present, but they name a process, not a
// session. The socket's mode (0600) and the daemon's same-user check are the
// only access control; nothing here grants or refuses a write.
type Caller struct {
	// Principal is the id the bridge announced in its hello, "" when none.
	Principal string
	// HasPeer reports whether PID and UID are known (Linux unix sockets).
	HasPeer bool
	PID     int32
	UID     int64
}

type callerKey struct{}

func withCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// callerFrom returns the connection's Caller, or false for a stdio session
// (`mcp`), whose own stderr already is its log.
func callerFrom(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok
}

// logSuffix renders the caller for a log line: ` principal="<id>"`, then
// ` pid=<n> uid=<n>` when the kernel supplied them.
func (c Caller) logSuffix() string {
	s := fmt.Sprintf(" principal=%q", c.Principal)
	if c.HasPeer {
		s += fmt.Sprintf(" pid=%d uid=%d", c.PID, c.UID)
	}
	return s
}
