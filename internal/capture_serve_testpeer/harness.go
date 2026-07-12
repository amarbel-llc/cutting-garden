package capture_serve_testpeer

import (
	"net"
	"os"
	"path/filepath"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// ConnPair yields both ends of an in-process unixpacket connection for
// session-level tests that drive Serve/RunBatch without spawning. The
// socket binds under /tmp with a short name — sun_path is ~108 bytes and
// a deep $TMPDIR overflows it (the Phase 0 spike's design finding).
// cleanup closes both ends and removes the socket directory.
func ConnPair() (accept, dial *net.UnixConn, cleanup func(), err error) {
	tmpDir, err := os.MkdirTemp("/tmp", "cgpeer-")
	if err != nil {
		return nil, nil, nil, errors.Wrap(err)
	}
	sockPath := filepath.Join(tmpDir, "s")

	ln, err := net.Listen("unixpacket", sockPath)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, nil, nil, errors.Wrap(err)
	}

	accepted := make(chan *net.UnixConn, 1)
	go func() {
		conn, aerr := ln.Accept()
		if aerr != nil {
			close(accepted)
			return
		}
		accepted <- conn.(*net.UnixConn)
	}()

	dialed, err := net.Dial("unixpacket", sockPath)
	if err != nil {
		_ = ln.Close()
		_ = os.RemoveAll(tmpDir)
		return nil, nil, nil, errors.Wrap(err)
	}
	acceptConn, ok := <-accepted
	_ = ln.Close()
	if !ok {
		_ = dialed.Close()
		_ = os.RemoveAll(tmpDir)
		return nil, nil, nil, errors.ErrorWithStackf("accept failed")
	}

	cleanup = func() {
		_ = acceptConn.Close()
		_ = dialed.Close()
		_ = os.RemoveAll(tmpDir)
	}
	return acceptConn, dialed.(*net.UnixConn), cleanup, nil
}
