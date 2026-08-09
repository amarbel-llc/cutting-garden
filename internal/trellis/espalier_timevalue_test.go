package trellis

import "testing"

// TestEspalierTimeAndDateFieldValues proves the organize box-interior date/time
// field-pred values (#47) parse as a SINGLE Bareword value token each — the
// RFC 0014 isometry requirement that a rendered espalier box stays valid
// trellis. The clock separator is the load-bearing case:
//
//   - HH-mm (hyphen) is unconditional identifier content ('-' is always an
//     IdentRune), so "09-30" is one token.
//   - HH:mm (colon) is ALSO one token: ':' is a sigil rune, but the strict
//     sigil rule (IsIdentRuneAt recursing on i+1) keeps it identifier-interior
//     because a digit follows it — so "09:30" does not split into "09" + a
//     trailing ':' sigil.
//   - The date YYYY-MM-DD scans as one Bareword rather than misparsing as a
//     DigestTerm (the digest attempt fails its !IdentRune guard on the second
//     '-' and backtracks to Bareword).
//
// If any value split or misparsed, the box would not round-trip as valid
// trellis and #47's presenter format would be unsound.
func TestEspalierTimeAndDateFieldValues(t *testing.T) {
	cases := []struct {
		src, wantField, wantValue string
	}{
		{"date_start=2026-08-15", "date_start", "2026-08-15"},
		{"time_start=09-30", "time_start", "09-30"},
		{"time_start=09:30", "time_start", "09:30"},
		{"date_due=2026-12-01", "date_due", "2026-12-01"},
		{"time_end=17:05", "time_end", "17:05"},
		{"time_end=17-05", "time_end", "17-05"},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			q := mustParse(t, tc.src)
			if len(q.Path.Steps) != 1 {
				t.Fatalf("Steps = %d, want 1", len(q.Path.Steps))
			}
			terms := q.Path.Steps[0].Terms
			if len(terms) != 1 {
				t.Fatalf("Terms = %d, want 1", len(terms))
			}
			fp, ok := terms[0].Basic.(FieldPredBasicTerm)
			if !ok {
				t.Fatalf("Basic = %T, want FieldPredBasicTerm", terms[0].Basic)
			}
			pred := fp.FieldPred
			if pred.Field.Name != tc.wantField {
				t.Errorf("field = %q, want %q", pred.Field.Name, tc.wantField)
			}
			if pred.Op.String() != "=" {
				t.Errorf("op = %q, want =", pred.Op.String())
			}
			if pred.List || len(pred.Values) != 1 {
				t.Fatalf("Values = %+v (List=%v), want exactly one bare value", pred.Values, pred.List)
			}
			bw, ok := pred.Values[0].(Bareword)
			if !ok {
				t.Fatalf("value = %T, want Bareword (a single token, not a split/misparse)", pred.Values[0])
			}
			if bw.Name != tc.wantValue {
				t.Errorf("value = %q, want %q", bw.Name, tc.wantValue)
			}
		})
	}
}

// TestEspalierBoxWithTimeFieldsParses proves a full organize box literal — the
// espalier ground fragment #47's presenter would render for an event grouped by
// month — parses as a single trellis group term, so the rendered document stays
// valid trellis end to end (RFC 0014 isometry).
func TestEspalierBoxWithTimeFieldsParses(t *testing.T) {
	src := `[dentist.ics !caldav-object-vevent-v1 date_start=2026-08-15 time_start=09-30 date_end=2026-08-15 time_end=10-00 location=HQ]`
	q := mustParse(t, src)
	if len(q.Path.Steps) != 1 || len(q.Path.Steps[0].Terms) != 1 {
		t.Fatalf("box did not parse as one group term: %+v", q.Path)
	}
	if _, ok := q.Path.Steps[0].Terms[0].Basic.(GroupBasicTerm); !ok {
		t.Fatalf("box term = %T, want GroupBasicTerm", q.Path.Steps[0].Terms[0].Basic)
	}
}
