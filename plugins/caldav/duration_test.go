package caldav

import (
	"reflect"
	"testing"
	"time"
)

// TestParseICalDuration pins the RFC 5545 §3.3.6 dur-value forms: weeks fold
// into days, days stay calendar days (separate from the clock component), time
// designators require the T section, and negative/malformed values are
// rejected rather than guessed.
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
		{"+P1D", 1, 0, true},

		{"-P1D", 0, 0, false}, // negative: meaningless as an event length
		{"P", 0, 0, false},    // no component
		{"PT", 0, 0, false},   // no component
		{"P6", 0, 0, false},   // trailing number without designator
		{"P6H", 0, 0, false},  // time designator outside T section
		{"PT6D", 0, 0, false}, // date designator inside T section
		{"6D", 0, 0, false},   // missing P
		{"garbage", 0, 0, false},
		{"", 0, 0, false},
		{"P999999999D", 0, 0, false}, // magnitude bound
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
// DTEND carries), a date-time start keeps its clock and UTC marker, and an
// unparseable input derives nothing.
func TestEndFromStartAndDuration(t *testing.T) {
	cases := []struct {
		start, duration, want string
	}{
		{"20260813", "P6D", "20260819"},
		{"2026-08-17", "P14D", "20260831"},
		{"20260817T140000Z", "PT1H30M", "20260817T153000Z"},
		{"20260817T233000", "PT1H", "20260818T003000"}, // floating, rolls the day
		{"20260813", "P1DT12H", "20260814T120000"},     // clock on a date-only start
		{"20260813", "P0D", "20260813"},

		{"", "P6D", ""},
		{"20260813", "", ""},
		{"20260813", "-P1D", ""},
		{"garbage", "P6D", ""},
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
