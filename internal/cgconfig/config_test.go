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
