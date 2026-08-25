package cgconfig

import "testing"

func TestOrganizeConfig_DateGranularity(t *testing.T) {
	// Valid value decodes.
	doc, err := DecodeConfigV0([]byte(
		"[organize]\n" + `date_granularity = "month"` + "\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Data().Organize.DateGranularity; got != "month" {
		t.Errorf("DateGranularity = %q, want month", got)
	}
	// Invalid value fails Validate.
	if _, err := DecodeConfigV0([]byte(
		"[organize]\n" + `date_granularity = "week"` + "\n",
	)); err == nil {
		t.Error("date_granularity=week must fail validation")
	}
}

func TestTagsConfig_Interpreter(t *testing.T) {
	// A registered interpreter decodes.
	doc, err := DecodeConfigV0([]byte(
		"[tags]\n" + `interpreter = "dodder-hyphen"` + "\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Data().Tags.Interpreter; got != "dodder-hyphen" {
		t.Errorf("Interpreter = %q, want dodder-hyphen", got)
	}

	// The decoded value round-trips back through the generated encoder.
	encoded, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}
	roundTripped, err := DecodeConfigV0(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got := roundTripped.Data().Tags.Interpreter; got != "dodder-hyphen" {
		t.Errorf("round-tripped Interpreter = %q, want dodder-hyphen", got)
	}

	// An unknown interpreter name fails Validate.
	if _, err := DecodeConfigV0([]byte(
		"[tags]\n" + `interpreter = "bogus"` + "\n",
	)); err == nil {
		t.Error("tags.interpreter=bogus must fail validation")
	}
}

func TestTagsConfig_Validate(t *testing.T) {
	for _, name := range []string{"", "naive", "dodder-hyphen"} {
		if err := (TagsConfig{Interpreter: name}).Validate(); err != nil {
			t.Errorf("Validate(interpreter=%q) = %v, want nil", name, err)
		}
	}
	if err := (TagsConfig{Interpreter: "bogus"}).Validate(); err == nil {
		t.Error("Validate(interpreter=bogus) = nil, want a bad request")
	}
}
