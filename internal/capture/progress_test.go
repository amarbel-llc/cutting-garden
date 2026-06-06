package capture

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/amarbel-llc/madder/go/pkgs/output_format"
)

func TestValidateProgress(t *testing.T) {
	for _, v := range []string{progressAuto, progressAlways, progressNever} {
		if err := validateProgress(v); err != nil {
			t.Errorf("validateProgress(%q) = %v, want nil", v, err)
		}
	}

	err := validateProgress("loud")
	if err == nil {
		t.Fatalf("validateProgress(%q) = nil, want error", "loud")
	}
	for _, want := range []string{"auto", "always", "never"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing allowed value %q", err.Error(), want)
		}
	}
}

func TestProgressActive(t *testing.T) {
	// A pipe write end is a *os.File that is not a TTY — the auto branch's
	// isatty probe returns false against it.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })

	t.Run("NeverIsAlwaysFalse", func(t *testing.T) {
		if progressActive(progressNever, w) {
			t.Errorf("never = true, want false")
		}
	})

	t.Run("AlwaysIsAlwaysTrue", func(t *testing.T) {
		if !progressActive(progressAlways, w) {
			t.Errorf("always = false, want true")
		}
	})

	t.Run("AutoNonTTYIsFalse", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		os.Unsetenv("NO_COLOR")
		if progressActive(progressAuto, w) {
			t.Errorf("auto on non-TTY pipe = true, want false")
		}
	})

	t.Run("AutoWithNoColorIsFalse", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		if progressActive(progressAuto, w) {
			t.Errorf("auto with NO_COLOR set = true, want false")
		}
	})
}

func TestCaptureLabel(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"NoArgs", nil, "capture"},
		{"OnlyFlags", []string{"-format=json"}, "capture"},
		{"FirstPositional", []string{"./src"}, "capture ./src"},
		{"FlagThenPositional", []string{"-format=tap", "dir-a"}, "capture dir-a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := captureLabel(tt.args); got != tt.want {
				t.Errorf("captureLabel(%v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

func TestReporterLineWriter_SplitsOnCRAndLF(t *testing.T) {
	var got []string
	w := &reporterLineWriter{log: func(s string) { got = append(got, s) }}

	if _, err := io.WriteString(w, "alpha\nbeta\rgamma\n"); err != nil {
		t.Fatal(err)
	}

	want := []string{"alpha", "beta", "gamma"}
	if len(got) != len(want) {
		t.Fatalf("logged %d segments %q, want %d %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("segment[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestReporterLineWriter_BuffersPartialSegment(t *testing.T) {
	var got []string
	w := &reporterLineWriter{log: func(s string) { got = append(got, s) }}

	io.WriteString(w, "# (blob_store: xyz) dia")
	if len(got) != 0 {
		t.Fatalf("partial segment flushed early: %q", got)
	}

	io.WriteString(w, "ling sftp host\n")
	if len(got) != 1 || got[0] != "# (blob_store: xyz) dialing sftp host" {
		t.Fatalf("got %q, want the joined segment", got)
	}
}

func TestReporterLineWriter_SkipsEmptySegments(t *testing.T) {
	var got []string
	w := &reporterLineWriter{log: func(s string) { got = append(got, s) }}

	io.WriteString(w, "\r\n\n   \n\r")
	if len(got) != 0 {
		t.Fatalf("empty/whitespace segments logged: %q", got)
	}
}

// TestReporterLineWriter_ConcurrentWrites drives Write from many goroutines —
// the blob store may chatter from its own goroutines — and asserts no segment
// is lost. Meaningful under -race: it pins that Write serializes its buffer.
func TestReporterLineWriter_ConcurrentWrites(t *testing.T) {
	var mu sync.Mutex
	var got []string
	w := &reporterLineWriter{log: func(s string) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, s)
	}}

	const writers, lines = 8, 50
	var wg sync.WaitGroup
	for g := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range lines {
				fmt.Fprintf(w, "writer-%d line-%d\n", g, i)
			}
		}()
	}
	wg.Wait()

	if len(got) != writers*lines {
		t.Fatalf("logged %d segments, want %d", len(got), writers*lines)
	}
}

// captureStdout redirects os.Stdout to a pipe for the duration of fn and
// returns everything written. The viewport-inactive branch of setupReporting
// constructs its sink against os.Stdout directly, so this is how we observe
// the rollback-path bytes.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()
	w.Close()
	out := <-done
	r.Close()
	return out
}

// TestSetupReporting_InactiveRollbackByteIdentity is the rollback guarantee:
// with stderr a non-TTY pipe, -progress=never and -progress=auto MUST both
// take the inactive branch (nil reporter, real sink, no-op finish) and emit
// byte-identical stdout. This pins that activating the viewport flag without
// a TTY does not perturb the structured output.
func TestSetupReporting_InactiveRollbackByteIdentity(t *testing.T) {
	// Force stderr to a non-TTY so auto resolves inactive regardless of the
	// test runner's environment.
	origErr := os.Stderr
	er, ew, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = ew
	t.Cleanup(func() {
		os.Stderr = origErr
		er.Close()
		ew.Close()
	})
	t.Setenv("NO_COLOR", "")
	os.Unsetenv("NO_COLOR")

	run := func(mode string) string {
		cmd := &Capture{Format: output_format.Default, Progress: mode}
		return captureStdout(t, func() {
			rep, sink, finish := cmd.setupReporting("capture .")
			if rep != nil {
				t.Errorf("mode=%q: reporter non-nil, want inactive (nil)", mode)
			}
			sink.SetStore("")
			sink.StoreGroupReceipt("sha256-abc", 3)
			finish(nil) // must be a no-op on the inactive path
			sink.Finalize()
		})
	}

	never := run(progressNever)
	auto := run(progressAuto)

	if never != auto {
		t.Fatalf("rollback byte-identity broken:\nnever=%q\nauto =%q", never, auto)
	}
	// Sanity: the receipt actually reached stdout (i.e. not the discard sink).
	if !strings.Contains(never, "sha256-abc") {
		t.Errorf("inactive sink did not write receipt to stdout: %q", never)
	}
}
