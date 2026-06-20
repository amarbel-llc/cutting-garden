package caldav

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestDescribeBodies_DescribesObjectLeaf checks the schema-discovery payload:
// the object leaf is described (accepts both formats, with a parseable
// example), and the calendar container is NOT (it is not yet writable).
func TestDescribeBodies_DescribesObjectLeaf(t *testing.T) {
	bodies := Plugin{}.DescribeBodies()

	byTag := map[string]bool{}
	for _, b := range bodies {
		byTag[b.Tag] = true
	}
	if !byTag[typeObject] {
		t.Errorf("DescribeBodies must describe %q; got %+v", typeObject, bodies)
	}
	if byTag[typeCalendar] {
		t.Errorf("DescribeBodies must NOT describe the container %q (not writable)", typeCalendar)
	}

	// The example marshals to the objectView JSON shape and round-trips back
	// through the body normalizer (so the example is itself a valid payload).
	for _, b := range bodies {
		if b.Tag != typeObject {
			continue
		}
		if len(b.Accepts) < 2 {
			t.Errorf("object accepts = %v, want both .ics and JSON", b.Accepts)
		}
		raw, err := json.Marshal(b.Example)
		if err != nil {
			t.Fatalf("marshal example: %v", err)
		}
		if !strings.Contains(string(raw), `"component":"VEVENT"`) {
			t.Errorf("example JSON = %s, want a VEVENT objectView", raw)
		}
		if _, err := normalizeObjectBody(strings.NewReader(string(raw))); err != nil {
			t.Errorf("example is not a valid create body: %v", err)
		}
	}
}
