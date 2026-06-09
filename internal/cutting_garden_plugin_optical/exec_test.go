package cutting_garden_plugin_optical

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
)

// safeRecorder is a goroutine-safe log sink: runExternal invokes onLog
// from two concurrent scanner goroutines, so the test sink must lock.
type safeRecorder struct {
	mu       sync.Mutex
	logLines []string
}

func (r *safeRecorder) log(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logLines = append(r.logLines, line)
}

// bothStreamsScript writes to stdout AND stderr so the test can confirm
// runExternal forwards both to onLog. Exits 0 (happy path).
const bothStreamsScript = `#!/bin/sh
echo "status on stdout"
echo "diagnostic on stderr" >&2
exit 0
`

func TestRunExternal_ForwardsBothStreams(t *testing.T) {
	installFakeBin(t, "fake-rip", bothStreamsScript)

	rec := &safeRecorder{}
	if err := runExternal(context.Background(), t.TempDir(), "fake-rip", nil, rec.log); err != nil {
		t.Fatalf("runExternal: %v", err)
	}

	var sawStdout, sawStderr bool
	for _, l := range rec.logLines {
		switch l {
		case "status on stdout":
			sawStdout = true
		case "diagnostic on stderr":
			sawStderr = true
		}
	}
	if !sawStdout {
		t.Errorf("stdout line not forwarded to onLog; got %v", rec.logLines)
	}
	if !sawStderr {
		t.Errorf("stderr line not forwarded to onLog; got %v", rec.logLines)
	}
}

func TestRunExternal_NonZeroExit_SurfacesStderrTail(t *testing.T) {
	installFakeBin(t, "fake-rip", failingScript)

	err := runExternal(context.Background(), t.TempDir(), "fake-rip", nil, nil)
	if err == nil {
		t.Fatal("runExternal returned nil error on non-zero exit")
	}
	msg := err.Error()
	if !strings.Contains(msg, "stderr-tail:") {
		t.Errorf("error %q missing 'stderr-tail:' marker", msg)
	}
	if !strings.Contains(msg, "no medium found") {
		t.Errorf("error %q missing stderr diagnostic", msg)
	}
}

func TestRunExternal_BinaryMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := runExternal(context.Background(), t.TempDir(), "definitely-not-here", nil, nil)
	if err == nil {
		t.Fatal("runExternal returned nil error for missing binary")
	}
	if !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("error %q missing 'not found on PATH'", err.Error())
	}
}

func TestTailWriter_KeepsOnlyTail(t *testing.T) {
	var buf bytes.Buffer
	w := newTailWriter(&buf, 8)
	for _, chunk := range [][]byte{
		[]byte("aaaa"),
		[]byte("bbbb"),
		[]byte("cccc"),
	} {
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if got := buf.String(); got != "bbbbcccc" {
		t.Errorf("tail = %q, want %q", got, "bbbbcccc")
	}
}
