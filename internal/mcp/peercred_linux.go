//go:build linux

package mcp

import (
	"net"
	"syscall"
)

// peerCred returns the process id and user id the kernel recorded for the
// other end of a unix socket connection (SO_PEERCRED).
func peerCred(conn net.Conn) (pid int32, uid uint32, ok bool) {
	uc, isUnix := conn.(*net.UnixConn)
	if !isUnix {
		return 0, 0, false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, 0, false
	}
	var cred *syscall.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil || credErr != nil || cred == nil {
		return 0, 0, false
	}
	return cred.Pid, cred.Uid, true
}
