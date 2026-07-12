package capture_serve

// Phase 0 de-risk gate (docs/rfcs/0008 + the capture-serve tracer plan): the
// whole RFC 0008 blob transport rests on Go's net delivering an SCM_RIGHTS
// file descriptor over a SOCK_SEQPACKET ("unixpacket") connection. SEQPACKET
// is the least-exercised AF_UNIX family in net, so this MUST be proven
// empirically before any product code. If it ever fails, the announce/dial
// unixpacket design is not viable on this platform and the plan's fallbacks
// (socketpair+FileConn, raw x/sys/unix, SOCK_STREAM) apply.
//
// Design finding from this spike: AF_UNIX paths are bounded by sun_path
// (~108 bytes), and the devshell's deep $TMPDIR overflows it. The real
// handshake therefore MUST bind its rendezvous socket at a SHORT path
// (a short 0700 dir under /tmp or $XDG_RUNTIME_DIR, or a Linux abstract
// socket), NOT inside a deeply-nested worktree temp dir.

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
)

func TestSpike_SCMRightsOverUnixpacket(t *testing.T) {
	if _, err := net.ResolveUnixAddr("unixpacket", "probe"); err != nil {
		t.Skipf("unixpacket unsupported on this platform: %v", err)
	}

	// Bind under /tmp with a short name — sun_path is ~108 bytes and the
	// devshell's $TMPDIR (t.TempDir) overflows it (bind: invalid argument).
	tmpDir, err := os.MkdirTemp("/tmp", "cgspike-")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	sockPath := filepath.Join(tmpDir, "s.sock")

	ln, err := net.Listen("unixpacket", sockPath)
	if err != nil {
		t.Fatalf("listen unixpacket: %v", err)
	}
	defer ln.Close()

	// A multi-blob sequence proves the datagram<->fd association holds across
	// successive messages (the blob_concurrency=1 sequential invariant).
	blobs := [][]byte{
		[]byte("first node bytes\n"),
		[]byte("second\n"),
		[]byte("third blob, a little longer than the others\n"),
	}

	// "Orchestrator" side: accept; per blob create a pipe, pass the WRITE end
	// via SCM_RIGHTS on one datagram, then read the READ end to a digest.
	gotDigests := make([]string, len(blobs))
	var orchErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, aerr := ln.Accept()
		if aerr != nil {
			orchErr = aerr
			return
		}
		defer conn.Close()
		uc := conn.(*net.UnixConn)

		for i := range blobs {
			r, w, perr := os.Pipe()
			if perr != nil {
				orchErr = perr
				return
			}
			oob := syscall.UnixRights(int(w.Fd()))
			ctrl := []byte(`{"blob":` + strconv.Itoa(i) + `}`)
			if _, _, werr := uc.WriteMsgUnix(ctrl, oob, nil); werr != nil {
				w.Close()
				r.Close()
				orchErr = werr
				return
			}
			// Drop our copy of the write end so EOF fires once the peer closes
			// its received copy.
			w.Close()

			h := sha256.New()
			_, cerr := io.Copy(h, r)
			r.Close()
			if cerr != nil {
				orchErr = cerr
				return
			}
			gotDigests[i] = hex.EncodeToString(h.Sum(nil))
		}
	}()

	// "Plugin" side: dial; per blob receive the passed fd and write the bytes.
	conn, derr := net.Dial("unixpacket", sockPath)
	if derr != nil {
		t.Fatalf("dial unixpacket: %v", derr)
	}
	uc := conn.(*net.UnixConn)
	for i := range blobs {
		buf := make([]byte, 128)
		oob := make([]byte, syscall.CmsgSpace(4)) // one fd
		_, oobn, _, _, rerr := uc.ReadMsgUnix(buf, oob)
		if rerr != nil {
			conn.Close()
			t.Fatalf("blob %d ReadMsgUnix: %v", i, rerr)
		}
		scms, perr := syscall.ParseSocketControlMessage(oob[:oobn])
		if perr != nil || len(scms) != 1 {
			conn.Close()
			t.Fatalf("blob %d ParseSocketControlMessage: %v (count=%d)", i, perr, len(scms))
		}
		fds, perr := syscall.ParseUnixRights(&scms[0])
		if perr != nil || len(fds) != 1 {
			conn.Close()
			t.Fatalf("blob %d ParseUnixRights: %v (count=%d)", i, perr, len(fds))
		}
		bw := os.NewFile(uintptr(fds[0]), "blob")
		if _, werr := bw.Write(blobs[i]); werr != nil {
			conn.Close()
			t.Fatalf("blob %d write to passed fd: %v", i, werr)
		}
		bw.Close() // EOF for the orchestrator's read end
	}
	conn.Close()

	wg.Wait()
	if orchErr != nil {
		t.Fatalf("orchestrator side: %v", orchErr)
	}
	for i, b := range blobs {
		want := sha256.Sum256(b)
		if gotDigests[i] != hex.EncodeToString(want[:]) {
			t.Fatalf("blob %d digest mismatch: got %s want %s",
				i, gotDigests[i], hex.EncodeToString(want[:]))
		}
	}
}
