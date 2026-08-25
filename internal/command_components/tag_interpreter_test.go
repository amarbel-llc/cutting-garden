package command_components

import "testing"

func TestResolveTagInterpreter(t *testing.T) {
	// Override wins over the field default.
	interp, err := ResolveTagInterpreter("naive", "dodder-hyphen")
	if err != nil {
		t.Fatal(err)
	}
	// dodder-hyphen's Matches is transitive; naive's is exact. Distinguish
	// the resolved interpreter by that behavior.
	if !interp.Matches([]string{"project-client"}, "project") {
		t.Error("override=dodder-hyphen did not win: got exact-match semantics")
	}

	// Empty override falls back to the field default.
	interp, err = ResolveTagInterpreter("naive", "")
	if err != nil {
		t.Fatal(err)
	}
	if interp.Matches([]string{"project-client"}, "project") {
		t.Error("empty override did not fall back to naive: got transitive match")
	}

	// An unknown name from either source rejects.
	if _, err := ResolveTagInterpreter("bogus", ""); err == nil {
		t.Error("unknown field default must reject")
	}
	if _, err := ResolveTagInterpreter("naive", "bogus"); err == nil {
		t.Error("unknown override must reject")
	}
}
