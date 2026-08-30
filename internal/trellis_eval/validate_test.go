package trellis_eval

import (
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/trellis"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// TestValidate_RejectsQualifierTerms pins that both qualifier spellings
// (native tags design G10) — `(x)` as a term and `k=(x)` as a field value —
// are RESERVED in query position: Validate rejects them as a loud bad
// request naming the offending term, rather than letting the evaluator
// silently mismatch (organize's group-by dialect is their only consumer this
// slice).
func TestValidate_RejectsQualifierTerms(t *testing.T) {
	cases := []struct {
		query   string
		wantHas string
	}{
		{`(tags)`, "`(tags)`"},
		{`!task (tags)`, "`(tags)`"},
		{`status=(x)`, "`status=(x)`"},
		{`date_due=(month)`, "`date_due=(month)`"},
		{`k=[a, (b)]`, "`k=(b)`"},
		// Inside an OR-group and an existential subpath alike.
		{`[!task, (tags)]`, "`(tags)`"},
		{`!cal [-> status=(x)]`, "`status=(x)`"},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			q, err := trellis.Parse(tc.query)
			if err != nil {
				t.Fatalf("Parse(%q): %v (the grammar admits qualifiers; this is a validation test)", tc.query, err)
			}
			err = Validate(q)
			if err == nil {
				t.Fatalf("Validate(%q) = nil, want the reserved-qualifier rejection", tc.query)
			}
			if !errors.Is400BadRequest(err) {
				t.Fatalf("Validate(%q) = %v, want a bad request (exit 64)", tc.query, err)
			}
			msg := err.Error()
			if !strings.Contains(msg, "qualifier terms are reserved; not evaluable yet") {
				t.Fatalf("Validate(%q) = %q, want the reserved-qualifier message", tc.query, msg)
			}
			if !strings.Contains(msg, tc.wantHas) {
				t.Fatalf("Validate(%q) = %q, want it to name %s", tc.query, msg, tc.wantHas)
			}
		})
	}
}
