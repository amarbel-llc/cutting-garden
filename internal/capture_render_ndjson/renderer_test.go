package capture_render_ndjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/capture_events"
	"github.com/amarbel-llc/cutting-garden/internal/capture_receipt"
	"github.com/amarbel-llc/tap/go/pkgs/ndjson"
)

// scriptedRun drives the renderer through one phase with entries and a
// failure (ending not-OK with a verdict diagnostic), an empty OK phase,
// a skip phase, and a TODO phase carrying a diagnostic — then finalizes
// cleanly. Returned along with the exact NDJSON lines the run must
// produce (encoding/json sorts map keys, so diagnostic keys appear
// alphabetically).
func scriptedRun(r capture_events.Stream) {
	r.PhaseStart("walk fs")
	r.Entry(capture_receipt.EntryV1{
		Path: "a.txt", Root: "src",
		Type: capture_receipt.TypeFile,
		Mode: 0o644, Size: 5, BlobId: "blake2b256-abc",
	})
	r.Entry(capture_receipt.EntryV1{
		Path: ".", Root: "src",
		Type: capture_receipt.TypeDir,
		Mode: 0o755,
	})
	r.Entry(capture_receipt.EntryV1{
		Path: "ln", Root: "src",
		Type: capture_receipt.TypeSymlink,
		Mode: 0o777, Target: "a.txt",
	})
	r.Failure("src1", errors.New("boom"))
	r.Log("this line must not appear in the output: %d", 42)
	r.Plan(capture_events.ReportPlan{Items: 9, Label: "ignored"})
	r.Progress(capture_events.ReportProgress{Items: 1})
	r.PhaseEnd(capture_events.Verdict{
		OK:         false,
		Diagnostic: map[string]any{"error": "walk aborted"},
	})

	r.PhaseStart("empty phase")
	r.PhaseEnd(capture_events.Verdict{OK: true})

	r.PhaseStart("skipped phase")
	r.PhaseEnd(capture_events.Verdict{
		OK:        true,
		Directive: &capture_events.Directive{Kind: capture_events.DirectiveSkip, Reason: "offline"},
	})

	// ndjson retains directive AND diagnostic together — unlike TAP
	// text, where the directive rendering drops the diagnostic.
	r.PhaseStart("todo phase")
	r.PhaseEnd(capture_events.Verdict{
		OK:         false,
		Directive:  &capture_events.Directive{Kind: capture_events.DirectiveTodo, Reason: "fell back to full capture"},
		Diagnostic: map[string]any{"error": "fetch failed"},
	})
}

var scriptedGolden = []string{
	`{"type":"test","n":1,"description":"walk fs","ok":false,"directive":null,"diagnostic":{"entries":3,"error":"walk aborted","failed":1},"output":null,"subtest":[` +
		`{"type":"test","n":1,"description":"src/a.txt","ok":true,"directive":null,"diagnostic":{"blob_id":"blake2b256-abc","mode":"0644","size":"5","type":"file"},"output":null,"subtest":null,"line":0},` +
		`{"type":"test","n":2,"description":"src","ok":true,"directive":null,"diagnostic":{"mode":"0755","type":"dir"},"output":null,"subtest":null,"line":0},` +
		`{"type":"test","n":3,"description":"src/ln","ok":true,"directive":null,"diagnostic":{"mode":"0777","target":"a.txt","type":"symlink"},"output":null,"subtest":null,"line":0},` +
		`{"type":"test","n":4,"description":"src1","ok":false,"directive":null,"diagnostic":{"error":"boom"},"output":null,"subtest":null,"line":0}` +
		`],"line":0}`,
	`{"type":"test","n":2,"description":"empty phase","ok":true,"directive":null,"diagnostic":{"entries":0,"failed":0},"output":null,"subtest":null,"line":0}`,
	`{"type":"test","n":3,"description":"skipped phase","ok":true,"directive":{"kind":"skip","reason":"offline"},"diagnostic":{"entries":0,"failed":0},"output":null,"subtest":null,"line":0}`,
	`{"type":"test","n":4,"description":"todo phase","ok":false,"directive":{"kind":"todo","reason":"fell back to full capture"},"diagnostic":{"entries":0,"error":"fetch failed","failed":0},"output":null,"subtest":null,"line":0}`,
	`{"type":"summary","passed":1,"failed":1,"skipped":1,"todo":1,"total":4,"plan_count":4,"bailed":false,"valid":true,"diagnostics":[]}`,
}

func TestRenderer_GoldenScriptedRun(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	scriptedRun(r)
	r.Finalize(nil)

	want := strings.Join(scriptedGolden, "\n") + "\n"
	if got := buf.String(); got != want {
		t.Errorf("scripted run output mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderer_FinalizeError_Bailout(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	r.PhaseStart("p")
	r.PhaseEnd(capture_events.Verdict{OK: true})
	r.Finalize(errors.New("ssh: connection lost"))

	want := strings.Join([]string{
		`{"type":"test","n":1,"description":"p","ok":true,"directive":null,"diagnostic":{"entries":0,"failed":0},"output":null,"subtest":null,"line":0}`,
		`{"type":"bailout","message":"ssh: connection lost","line":0}`,
		`{"type":"summary","passed":1,"failed":0,"skipped":0,"todo":0,"total":1,"plan_count":1,"bailed":true,"valid":true,"diagnostics":[]}`,
	}, "\n") + "\n"
	if got := buf.String(); got != want {
		t.Errorf("bailout run output mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestRenderer_SuccessSubtestCap: 1002 success entries overflow the cap
// (1000 success subtests survive), but the late failure is appended
// regardless, and the phase diagnostic carries the full counts plus the
// truncation marker.
func TestRenderer_SuccessSubtestCap(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	r.PhaseStart("big walk")
	for i := range 1002 {
		r.Entry(capture_receipt.EntryV1{
			Path: fmt.Sprintf("f%04d", i), Root: "src",
			Type: capture_receipt.TypeFile,
			Mode: 0o644, Size: 1, BlobId: "b",
		})
	}
	r.Failure("late-failure", errors.New("boom"))
	r.PhaseEnd(capture_events.Verdict{OK: false})

	var rec ndjson.TestRecord
	line, _, _ := strings.Cut(buf.String(), "\n")
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("unmarshal phase record: %v", err)
	}

	if got, want := len(rec.Subtest), successSubtestCap+1; got != want {
		t.Fatalf("subtest count = %d, want %d (cap + the failure)", got, want)
	}
	for i, sub := range rec.Subtest[:successSubtestCap] {
		if !sub.OK {
			t.Fatalf("subtest %d: OK = false, want success record", i)
		}
	}
	last := rec.Subtest[successSubtestCap]
	if last.OK || last.Description != "late-failure" || last.N != successSubtestCap+1 {
		t.Errorf("last subtest = {ok:%v desc:%q n:%d}, want the always-appended failure", last.OK, last.Description, last.N)
	}
	if got := rec.Diagnostic["entries"]; got != float64(1002) {
		t.Errorf("diagnostic entries = %v, want 1002", got)
	}
	if got := rec.Diagnostic["failed"]; got != float64(1) {
		t.Errorf("diagnostic failed = %v, want 1", got)
	}
	if got := rec.Diagnostic["subtests_truncated"]; got != true {
		t.Errorf("diagnostic subtests_truncated = %v, want true", got)
	}
}

// TestRenderer_RoundTripTapStructs cross-validates the golden lines
// against tap's own pkgs/ndjson structs: unmarshaling a golden line and
// re-marshaling through the facade types must reproduce it byte-equal,
// proving field names, order, and null conventions match tap's wire.
func TestRenderer_RoundTripTapStructs(t *testing.T) {
	reencode := func(v any) string {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			t.Fatalf("re-encode: %v", err)
		}
		return strings.TrimSuffix(buf.String(), "\n")
	}

	var test ndjson.TestRecord
	if err := json.Unmarshal([]byte(scriptedGolden[0]), &test); err != nil {
		t.Fatalf("unmarshal TestRecord: %v", err)
	}
	if got := reencode(test); got != scriptedGolden[0] {
		t.Errorf("TestRecord round-trip mismatch\ngot:  %s\nwant: %s", got, scriptedGolden[0])
	}

	var summary ndjson.SummaryRecord
	if err := json.Unmarshal([]byte(scriptedGolden[4]), &summary); err != nil {
		t.Fatalf("unmarshal SummaryRecord: %v", err)
	}
	if got := reencode(summary); got != scriptedGolden[4] {
		t.Errorf("SummaryRecord round-trip mismatch\ngot:  %s\nwant: %s", got, scriptedGolden[4])
	}
}

// TestRenderer_ConcurrentEvents exercises the Stream contract's
// concurrency tolerance (meaningful under -race).
func TestRenderer_ConcurrentEvents(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	r.PhaseStart("concurrent")

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Entry(capture_receipt.EntryV1{
				Path: fmt.Sprintf("f%02d", i), Root: "src",
				Type: capture_receipt.TypeFile, Mode: 0o644,
			})
			r.Log("noise %d", i)
			r.Progress(capture_events.ReportProgress{Items: int64(i)})
		}()
	}
	wg.Wait()
	r.PhaseEnd(capture_events.Verdict{OK: true})
	r.Finalize(nil)

	var rec ndjson.TestRecord
	line, _, _ := strings.Cut(buf.String(), "\n")
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("unmarshal phase record: %v", err)
	}
	if got := len(rec.Subtest); got != 50 {
		t.Errorf("subtest count = %d, want 50", got)
	}
	if got := rec.Diagnostic["entries"]; got != float64(50) {
		t.Errorf("diagnostic entries = %v, want 50", got)
	}
	seen := map[int]bool{}
	for _, sub := range rec.Subtest {
		seen[sub.N] = true
	}
	for n := 1; n <= 50; n++ {
		if !seen[n] {
			t.Errorf("subtest n=%d missing (numbering must be dense 1..50)", n)
		}
	}
}
