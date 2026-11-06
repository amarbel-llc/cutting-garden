package traversal_conformance

import (
	"encoding/json"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/traversal_serve"
)

// A filtered summary that still counts the dimension's other values was
// not filtered (cutting-garden#204), so the check must be able to fail; a
// date prefix and a fold-case spelling of the filtered value are matches.
func TestFilteredSummaryProblems(t *testing.T) {
	filter := []traversal_serve.PredicateView{
		{Dimension: "status", Value: "needs-action"},
		{Dimension: "date_due", Value: "2026-08"},
	}
	for name, tc := range map[string]struct {
		summary string
		fails   bool
	}{
		"honoured":         {`{"status":{"needs-action":3},"date_due":{"2026-08-15":2,"2026-08-01":1}}`, false},
		"fold-case":        {`{"status":{"NEEDS-ACTION":3}}`, false},
		"dimension absent": {`{"priority":{"1_should":3}}`, false},
		"ignored filter":   {`{"status":{"needs-action":3,"completed":5}}`, true},
		"wrong month":      {`{"date_due":{"2026-09-02":1}}`, true},
		"zero counts":      {`{"status":{"needs-action":3,"completed":0}}`, false},
	} {
		got := filteredSummaryProblems(json.RawMessage(tc.summary), filter)
		if (len(got) > 0) != tc.fails {
			t.Errorf("%s: problems = %v, want fails=%v", name, got, tc.fails)
		}
	}
}
