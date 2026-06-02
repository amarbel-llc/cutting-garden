// Package gitwire speaks the parts of git's smart transfer protocol
// cutting-garden needs to fetch only the objects that differ between a
// remote branch and a set of objects already held — the
// `want`/`have`/`done` fetch-pack negotiation (git protocol v0).
//
// It is the shared primitive behind incremental capture and object-level
// diff: given a want oid (the live tip) and have oids (a previously
// captured tip), the remote computes and sends a pack of just the delta.
// By NOT selecting the `thin-pack` capability the server returns a
// self-contained pack, so `git unpack-objects` can explode it into a
// scratch object database without the base objects present — no seeding.
//
// Transports: local (spawn `git upload-pack <dir>`) and smart-HTTP
// (`GET info/refs` + `POST git-upload-pack`). SSH and the dumb protocol
// are not implemented; callers fall back to a full clone via
// ErrUnsupportedTransport.
package gitwire

import (
	"bufio"
	"fmt"
	"io"
	"strconv"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// flushPkt is the pkt-line flush marker ("0000").
const flushPkt = "0000"

// writePkt writes payload as a single pkt-line (a 4-hex-digit length
// prefix covering the prefix itself plus the payload). payload should
// include any trailing newline the protocol expects.
func writePkt(w io.Writer, payload string) error {
	n := len(payload) + 4
	if n > 0xffff {
		return errors.ErrorWithStackf("gitwire: pkt-line payload too long: %d", n)
	}
	if _, err := io.WriteString(w, fmt.Sprintf("%04x%s", n, payload)); err != nil {
		return errors.Wrap(err)
	}
	return nil
}

// writeFlush writes a flush pkt-line.
func writeFlush(w io.Writer) error {
	if _, err := io.WriteString(w, flushPkt); err != nil {
		return errors.Wrap(err)
	}
	return nil
}

// readPkt reads one pkt-line. isFlush is true for a 0000 flush packet
// (payload is then nil). A clean io.EOF at a packet boundary is returned
// verbatim so callers can detect end-of-stream.
func readPkt(r *bufio.Reader) (payload []byte, isFlush bool, err error) {
	var hdr [4]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return nil, false, err
	}
	n, perr := strconv.ParseUint(string(hdr[:]), 16, 32)
	if perr != nil {
		return nil, false, errors.Wrapf(perr, "gitwire: bad pkt-line length %q", hdr[:])
	}
	if n == 0 {
		return nil, true, nil
	}
	if n < 4 {
		return nil, false, errors.ErrorWithStackf("gitwire: invalid pkt-line length %d", n)
	}
	buf := make([]byte, n-4)
	if _, err = io.ReadFull(r, buf); err != nil {
		return nil, false, errors.Wrap(err)
	}
	return buf, false, nil
}
