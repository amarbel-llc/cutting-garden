package cgconfig

import (
	"strings"
	"testing"
)

// `[organize] nest` is the default of the `(tree)` grouping's `_nest` lever
// (cutting-garden#353): full, 1 (one level of continuation headings) or none.
func TestOrganizeConfig_Nest(t *testing.T) {
	for _, v := range []string{"full", "1", "none"} {
		doc, err := DecodeConfigV0([]byte("[organize]\n" + `nest = "` + v + `"` + "\n"))
		if err != nil {
			t.Fatalf("nest = %q: %v", v, err)
		}
		if got := doc.Data().Organize.Nest; got != v {
			t.Errorf("nest = %q decoded as %q", v, got)
		}
		encoded, err := doc.Encode()
		if err != nil {
			t.Fatal(err)
		}
		again, err := DecodeConfigV0(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if got := again.Data().Organize.Nest; got != v {
			t.Errorf("nest = %q did not round-trip, got %q", v, got)
		}
	}

	_, err := DecodeConfigV0([]byte("[organize]\n" + `nest = "deep"` + "\n"))
	if err == nil {
		t.Fatal(`nest = "deep" must fail validation`)
	}
	if !strings.Contains(err.Error(), "full, 1, none") {
		t.Errorf("error %q must name the domain `full, 1, none`", err)
	}
}
