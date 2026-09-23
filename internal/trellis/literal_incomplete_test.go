package trellis

import (
	stderrors "errors"
	"testing"
)

// TestParseLiteralPrefix_Incomplete pins SyntaxError.Incomplete, the signal
// organize's wrapped-box reader joins continuation lines on
// (cutting-garden#261): true ONLY when src opens a group and ends inside it
// (an unclosed `[` or String), false for a genuine mid-input error or a src
// that never opens a group.
func TestParseLiteralPrefix_Incomplete(t *testing.T) {
	for src, want := range map[string]bool{
		`[t.ics`:                 true,
		`  [t.ics work `:         true,
		`[t.ics x="quoted`:       true,
		`[t.ics x="esc\`:         true,
		`[t.ics [nested] tail`:   true,
		`[t.ics x=] desc`:        false,
		`t.ics] no open bracket`: false,
		``:                       false,
		`5 dollars`:              false,
	} {
		_, _, err := ParseLiteralPrefix(src)
		var se *SyntaxError
		if !stderrors.As(err, &se) {
			t.Errorf("ParseLiteralPrefix(%q) = %v; want a *SyntaxError", src, err)
			continue
		}
		if se.Incomplete != want {
			t.Errorf("ParseLiteralPrefix(%q).Incomplete = %v, want %v (%v)", src, se.Incomplete, want, se)
		}
	}
}
