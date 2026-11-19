//go:build !linux

package mcp

import "net"

// peerCred is unavailable off Linux; the write log then carries the
// principal alone.
func peerCred(net.Conn) (pid int32, uid uint32, ok bool) { return 0, 0, false }
