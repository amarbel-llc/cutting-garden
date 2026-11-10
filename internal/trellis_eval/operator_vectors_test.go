package trellis_eval

import (
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/trellis"
)

// TestOperatorConformance is the trellis_eval half of cutting-garden FDR
// 0028 vector A-16: the field operators agree with pkgs/ajour's on one
// shared table (trellis.OperatorVectors).
func TestOperatorConformance(t *testing.T) {
	var ev evaluator
	for _, v := range trellis.OperatorVectors {
		t.Run(v.Op.String()+" "+v.Got+" "+v.Want, func(t *testing.T) {
			if got := ev.satisfies(v.Op, v.Got, v.Want); got != v.Holds {
				t.Fatalf("%q %s %q = %v, want %v", v.Got, v.Op, v.Want, got, v.Holds)
			}
		})
	}
}
