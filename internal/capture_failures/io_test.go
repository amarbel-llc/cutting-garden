package capture_failures

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func sample() *V1 {
	return &V1{
		Meta: Meta{
			Ts:       "2026-06-07T12:00:00Z",
			Outcome:  OutcomeAborted,
			Signal:   "interrupt",
			Receipt:  "sha256-abc",
			Roots:    []string{"./", "other/"},
			Captured: 6018,
			Failed:   2,
		},
		Failures: []FailureV1{
			{Root: "./", Path: "a/b.ts", Op: OpBlobWrite, Error: "read: permission denied"},
			{Root: "./", Path: "c.txt", Op: OpStat, Error: "stale handle"},
		},
	}
}

func TestWriteV1ReadV1_RoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if _, err := WriteV1(&buf, sample()); err != nil {
		t.Fatalf("WriteV1: %v", err)
	}
	wire := buf.String()
	if !strings.Contains(wire, "! "+TypeTagV1) {
		t.Fatalf("missing type tag in wire: %q", wire)
	}

	got, err := ReadV1(strings.NewReader(wire))
	if err != nil {
		t.Fatalf("ReadV1: %v", err)
	}

	if want := sample(); !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip mismatch:\n  got:  %+v\n  want: %+v", got, want)
	}
}

func TestReadV1_RejectsUnknownTypeTag(t *testing.T) {
	const tag = "cutting_garden-capture_failures-v999"
	blob := "---\n! " + tag + "\n---\n\n"

	_, err := ReadV1(strings.NewReader(blob))
	if err == nil {
		t.Fatal("expected error for unknown type-tag, got nil")
	}
	if !strings.Contains(err.Error(), tag) {
		t.Errorf("error does not mention the tag %q: %v", tag, err)
	}
}

func TestWriteV1_OmitsEmptySignalAndReceipt(t *testing.T) {
	v := &V1{
		Meta: Meta{
			Ts:       "2026-06-07T12:00:00Z",
			Outcome:  OutcomeFailures,
			Roots:    []string{"./"},
			Captured: 3,
			Failed:   1,
		},
		Failures: []FailureV1{
			{Root: "./", Path: "c.txt", Op: OpStat, Error: "stale handle"},
		},
	}

	var buf bytes.Buffer
	if _, err := WriteV1(&buf, v); err != nil {
		t.Fatalf("WriteV1: %v", err)
	}
	wire := buf.String()

	if strings.Contains(wire, "- signal") {
		t.Errorf("wire contains a signal line despite empty Signal: %q", wire)
	}
	if strings.Contains(wire, "- receipt") {
		t.Errorf("wire contains a receipt line despite empty Receipt: %q", wire)
	}

	got, err := ReadV1(strings.NewReader(wire))
	if err != nil {
		t.Fatalf("ReadV1: %v", err)
	}
	if got.Meta.Signal != "" || got.Meta.Receipt != "" {
		t.Errorf("zero values not preserved: signal=%q receipt=%q",
			got.Meta.Signal, got.Meta.Receipt)
	}
	if !reflect.DeepEqual(got, v) {
		t.Errorf("round-trip mismatch:\n  got:  %+v\n  want: %+v", got, v)
	}
}

func TestWriteV1_TruncatesErrorAt1KiB(t *testing.T) {
	long := strings.Repeat("e", maxErrorBytes+100)
	v := &V1{
		Meta: Meta{
			Ts:       "2026-06-07T12:00:00Z",
			Outcome:  OutcomeFailures,
			Roots:    []string{"./"},
			Captured: 0,
			Failed:   1,
		},
		Failures: []FailureV1{
			{Root: "./", Path: "c.txt", Op: OpBlobWrite, Error: long},
		},
	}

	var buf bytes.Buffer
	if _, err := WriteV1(&buf, v); err != nil {
		t.Fatalf("WriteV1: %v", err)
	}

	got, err := ReadV1(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("ReadV1: %v", err)
	}
	if len(got.Failures) != 1 {
		t.Fatalf("failures len: got %d want 1", len(got.Failures))
	}
	if gotLen := len(got.Failures[0].Error); gotLen != maxErrorBytes {
		t.Errorf("error length: got %d want %d", gotLen, maxErrorBytes)
	}
	// Truncation happens at encode; the caller's struct is untouched.
	if len(v.Failures[0].Error) != len(long) {
		t.Errorf("WriteV1 mutated the caller's Error string")
	}
}
