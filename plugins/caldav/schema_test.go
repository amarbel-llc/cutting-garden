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
	for _, tag := range []string{typeVTODO, typeVEVENT, typeVJOURNAL} {
		if !byTag[tag] {
			t.Errorf("DescribeBodies must describe %q; got %+v", tag, bodies)
		}
	}
	if byTag[typeCalendar] {
		t.Errorf("DescribeBodies must NOT describe the container %q (not writable)", typeCalendar)
	}

	// Each example marshals to its component's objectView JSON shape and
	// round-trips back through the body normalizer (so the example is itself a
	// valid payload for that component's leaf type).
	wantComponent := map[string]string{
		typeVTODO:    "VTODO",
		typeVEVENT:   "VEVENT",
		typeVJOURNAL: "VJOURNAL",
	}
	for _, b := range bodies {
		wc, ok := wantComponent[b.Tag]
		if !ok {
			continue
		}
		if len(b.Accepts) < 2 {
			t.Errorf("%s accepts = %v, want both .ics and JSON", b.Tag, b.Accepts)
		}
		raw, err := json.Marshal(b.Example)
		if err != nil {
			t.Fatalf("marshal example: %v", err)
		}
		if !strings.Contains(string(raw), `"component":"`+wc+`"`) {
			t.Errorf("%s example JSON = %s, want a %s objectView", b.Tag, raw, wc)
		}
		if _, err := normalizeObjectBody(strings.NewReader(string(raw))); err != nil {
			t.Errorf("%s example is not a valid create body: %v", b.Tag, err)
		}
	}
}
