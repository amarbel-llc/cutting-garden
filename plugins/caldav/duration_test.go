package caldav

import (
	"reflect"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"
)

// TestParseICalDuration pins the RFC 5545 §3.3.6 dur-value grammar: weeks stand
// alone, days may precede a time section, time designators come in fixed H-M-S
// order inside the T section, components fold as calendar days vs clock, and
// negative / malformed / RFC-forbidden shapes are rejected rather than guessed.
func TestParseICalDuration(t *testing.T) {
	cases := []struct {
		in    string
		days  int
		clock time.Duration
		ok    bool
	}{
		{"P6D", 6, 0, true},
		{"P2W", 14, 0, true},
		{"PT2H30M", 0, 2*time.Hour + 30*time.Minute, true},
		{"P1DT12H", 1, 12 * time.Hour, true},
		{"PT15M", 0, 15 * time.Minute, true},
		{"PT5S", 0, 5 * time.Second, true},
		{"P0D", 0, 0, true},
		{"PT0S", 0, 0, true},
		{"+P1D", 1, 0, true},

		{"-P1D", 0, 0, false},   // negative: meaningless as an event length
		{"P", 0, 0, false},      // no component
		{"PT", 0, 0, false},     // no component
		{"P1DT", 0, 0, false},   // T section with no time component
		{"P6", 0, 0, false},     // trailing number without designator
		{"P1H", 0, 0, false},    // time designator outside the T section
		{"P6H", 0, 0, false},    // same
		{"PT6D", 0, 0, false},   // date designator inside the T section
		{"P1W2D", 0, 0, false},  // weeks stand alone (§3.3.6)
		{"P1D1D", 0, 0, false},  // repeated designator
		{"PT1M1H", 0, 0, false}, // out-of-order time designators
		{"6D", 0, 0, false},     // missing P
		{"garbage", 0, 0, false},
		{"", 0, 0, false},
		{"PT9999999H", 0, 0, false}, // 7 digits: over the magnitude cap
		{"P99999999D", 0, 0, false}, // 8 digits: over the magnitude cap
	}
	for _, c := range cases {
		days, clock, ok := parseICalDuration(c.in)
		if days != c.days || clock != c.clock || ok != c.ok {
			t.Errorf("parseICalDuration(%q) = (%d, %v, %t), want (%d, %v, %t)",
				c.in, days, clock, ok, c.days, c.clock, c.ok)
		}
	}
}

// TestEndFromStartAndDuration pins the derived end's shape parity with the
// start: date-only + whole days stays date-only (the exclusive end an all-day
// DTEND carries), a date-time start keeps its clock and UTC marker, the
// lenient forms splitICalDateTime renders (hyphenated date, lowercase t/z)
// derive too, and the reject paths — unparseable input, a time-component
// duration on a DATE start (§3.8.2.5), a result past year 9999 — derive
// nothing rather than a wrong or garbled end.
func TestEndFromStartAndDuration(t *testing.T) {
	cases := []struct {
		start, duration, want string
	}{
		{"20260813", "P6D", "20260819"},
		{"2026-08-17", "P14D", "20260831"},
		{"20260813", "P2W", "20260827"},
		{"20260817T140000Z", "PT1H30M", "20260817T153000Z"},
		{"20260817T233000", "PT1H", "20260818T003000"}, // floating, rolls the day
		{"2026-08-17T140000", "PT1H", "20260817T150000"},
		{"20260817t140000z", "PT1H", "20260817T150000Z"},
		{"20260813", "P0D", "20260813"},

		{"", "P6D", ""},
		{"20260813", "", ""},
		{"20260813", "-P1D", ""},
		{"garbage", "P6D", ""},
		{"20260813", "P1DT12H", ""}, // time-component duration on a DATE start
		{"99990101", "P365D", ""},   // result past year 9999
	}
	for _, c := range cases {
		if got := endFromStartAndDuration(c.start, c.duration); got != c.want {
			t.Errorf("endFromStartAndDuration(%q, %q) = %q, want %q",
				c.start, c.duration, got, c.want)
		}
	}
}

// TestDateCodec_EndFromDuration drives cutting-garden#233 at the codec level: a
// DURATION-carrying event presents the same date_end/time_end atoms a
// DTEND-carrying one does, an explicit DTEND still wins, and an event with
// neither presents no end.
func TestDateCodec_EndFromDuration(t *testing.T) {
	codec := caldavDateCodec{
		storedKey: listingFieldDtEnd, suffix: "end",
		writable: false, endFromDuration: true,
	}

	t.Run("all-day span from DURATION", func(t *testing.T) {
		got, err := codec.Format(map[string]any{
			listingFieldDtStart:  "20260813",
			listingFieldDuration: "P6D",
		})
		if err != nil {
			t.Fatal(err)
		}
		want := map[string][]string{"date_end": {"2026-08-19"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Format = %v, want %v", got, want)
		}
	})

	t.Run("timed span from DURATION", func(t *testing.T) {
		got, err := codec.Format(map[string]any{
			listingFieldDtStart:  "20260817T140000Z",
			listingFieldDuration: "PT1H30M",
		})
		if err != nil {
			t.Fatal(err)
		}
		want := map[string][]string{
			"date_end": {"2026-08-17"},
			"time_end": {"15-30"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Format = %v, want %v", got, want)
		}
	})

	t.Run("explicit DTEND wins over DURATION", func(t *testing.T) {
		got, err := codec.Format(map[string]any{
			listingFieldDtStart:  "20260813",
			listingFieldDtEnd:    "20260815",
			listingFieldDuration: "P6D",
		})
		if err != nil {
			t.Fatal(err)
		}
		want := map[string][]string{"date_end": {"2026-08-15"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Format = %v, want %v", got, want)
		}
	})

	t.Run("neither derives nothing", func(t *testing.T) {
		got, err := codec.Format(map[string]any{
			listingFieldDtStart: "20260813",
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("Format = %v, want empty", got)
		}
	})
}

// TestPresentBoxAtoms_DerivesDurationEnd pins the PRODUCTION wiring, not a
// locally built codec: the dtend instance declared in unifiedFieldSets (and
// carried into the derived unifiedCodecs union PresentBoxAtoms consumes) must
// actually have the endFromDuration fallback enabled — a declaration refactor
// that drops the flag regresses #233 with the codec-level tests still green.
func TestPresentBoxAtoms_DerivesDurationEnd(t *testing.T) {
	node := cutting_garden_plugins.Node{Fields: map[string]any{
		listingFieldDtStart:  "20260813",
		listingFieldDuration: "P6D",
	}}
	atoms := (Plugin{}).PresentBoxAtoms(node)

	var end *cutting_garden_plugins.BoxAtom
	for i := range atoms {
		if atoms[i].Name == "date_end" {
			end = &atoms[i]
		}
	}
	if end == nil {
		t.Fatalf("no date_end atom derived for a DURATION event: %+v", atoms)
	}
	if end.Value != "2026-08-19" {
		t.Errorf("date_end = %q, want 2026-08-19", end.Value)
	}
	if end.Field != listingFieldDtEnd {
		t.Errorf("date_end attributes to %q, want %q", end.Field, listingFieldDtEnd)
	}
}
