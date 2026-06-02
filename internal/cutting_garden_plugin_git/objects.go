package cutting_garden_plugin_git

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// objectVisitor is called once per git object enumerated by
// streamAllObjects. payload is a reader yielding exactly size bytes of
// the object's content (the raw `git cat-file` payload — no
// `<type> <size>\0` loose-object header). The visitor SHOULD consume
// payload; streamAllObjects drains any unread remainder so the stream
// stays framed regardless. A non-nil return aborts the walk.
type objectVisitor func(oid, typ string, size int64, payload io.Reader) error

// listObjectTypes runs `git cat-file --batch-all-objects --batch-check`
// in gitDir (a bare clone) and returns a map of every object's oid to
// its git type. Unlike streamAllObjects it requests only the check line
// (oid + type), transferring no object contents — the cheap enumeration
// the object-level diff needs once a tip move is already established.
func listObjectTypes(ctx context.Context, gitDir string) (map[string]string, error) {
	out, err := gitOutput(ctx, gitDir,
		"cat-file", "--batch-all-objects",
		"--batch-check=%(objectname) %(objecttype)")
	if err != nil {
		return nil, err
	}

	objs := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		objs[fields[0]] = fields[1]
	}
	return objs, nil
}

// streamAllObjects runs `git cat-file --batch-all-objects --batch` in
// gitDir (a bare clone) and invokes fn once per object in the object
// database — every commit, tree, blob, and tag reachable in the
// single-branch clone. One git process streams all objects; payloads
// are handed to fn as bounded readers so large blobs never buffer in
// memory.
//
// The `--batch` record framing is:
//
//	<oid> SP <type> SP <size> LF <payload> LF
//
// We read the header line, hand fn an io.LimitReader over the next
// <size> bytes, drain any unread remainder, then consume the trailing
// LF before the next record.
func streamAllObjects(ctx context.Context, gitDir string, fn objectVisitor) (err error) {
	binPath, err := lookGit()
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, binPath, "cat-file", "--batch-all-objects", "--batch")
	cmd.Dir = gitDir

	var stderrTail bytes.Buffer
	cmd.Stderr = newTailWriter(&stderrTail, stderrTailBytes)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return errors.Wrap(err)
	}
	if err = cmd.Start(); err != nil {
		return errors.Wrap(err)
	}

	r := bufio.NewReader(stdout)
	for {
		header, rerr := r.ReadString('\n')
		if header == "" && rerr == io.EOF {
			break
		}
		if rerr != nil && rerr != io.EOF {
			err = errors.Wrap(rerr)
			break
		}

		header = strings.TrimRight(header, "\n")
		if header == "" {
			if rerr == io.EOF {
				break
			}
			continue
		}

		fields := strings.Fields(header)
		if len(fields) < 3 {
			// `<oid> missing` or a malformed header — should not happen
			// with --batch-all-objects, but fail loudly rather than
			// desync the stream.
			err = errors.ErrorWithStackf(
				"git plugin: unexpected cat-file header %q", header)
			break
		}

		oid, typ := fields[0], fields[1]
		size, perr := strconv.ParseInt(fields[2], 10, 64)
		if perr != nil {
			err = errors.ErrorWithStackf(
				"git plugin: bad object size in cat-file header %q", header)
			break
		}

		lr := io.LimitReader(r, size)
		fnErr := fn(oid, typ, size, lr)

		// Keep the stream framed: drain whatever fn left, then consume
		// the record-terminating LF.
		if _, derr := io.Copy(io.Discard, lr); derr != nil && err == nil {
			err = errors.Wrap(derr)
		}
		if _, berr := r.ReadByte(); berr != nil && berr != io.EOF && err == nil {
			err = errors.Wrap(berr)
		}

		if fnErr != nil {
			err = fnErr
			break
		}
		if err != nil {
			break
		}
	}

	if err != nil {
		// Unblock the child (its stdout pipe may still be full) before
		// reaping it; the walk error is what we surface.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}

	if waitErr := cmd.Wait(); waitErr != nil {
		return errors.ErrorWithStackf(
			"git plugin: git cat-file failed (%v)\nstderr-tail: %s",
			waitErr, stderrTail.String(),
		)
	}

	return nil
}
