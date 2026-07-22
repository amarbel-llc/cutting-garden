package diff_test

import (
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/command"
	"code.linenisgreat.com/cutting-garden/internal/diff"

	// Blank-import the file plugin so its init() registers under the
	// "", "file" diff schemes. Step 3 will exercise the resolve-plugin
	// path; the step-2 skeleton does not reach it, but the import is
	// harmless and matches how cmd/cutting-garden/main.go wires it.
	_ "code.linenisgreat.com/cutting-garden/plugins/file"
)

func makeUtility() command.Utility {
	u := command.MakeUtility("cutting-garden", nil)
	u.AddCmd("diff", diff.New())
	return u
}

func TestDiff_NoArgs_MissingReceiptId(t *testing.T) {
	u := makeUtility()
	code := u.Run([]string{"cutting-garden", "diff"})
	if code != 64 {
		t.Errorf("expected EX_USAGE (64) for no positional args, got %d", code)
	}
}

func TestDiff_OneArg_MissingDir(t *testing.T) {
	u := makeUtility()
	code := u.Run([]string{"cutting-garden", "diff", "blake2b256-deadbeef"})
	if code != 64 {
		t.Errorf("expected EX_USAGE (64) for missing <dir>, got %d", code)
	}
}

func TestDiff_ThreeArgs_TooManyArgs(t *testing.T) {
	u := makeUtility()
	code := u.Run([]string{
		"cutting-garden", "diff",
		"blake2b256-deadbeef", "src", "extra-arg",
	})
	if code != 64 {
		t.Errorf("expected EX_USAGE (64) for trailing arg, got %d", code)
	}
}

func TestDiff_InvalidColorValue_Rejected(t *testing.T) {
	u := makeUtility()
	code := u.Run([]string{
		"cutting-garden", "diff", "-color", "bogus",
		"blake2b256-deadbeef", "src",
	})
	if code != 64 {
		t.Errorf("expected EX_USAGE (64) for invalid -color, got %d", code)
	}
}

func TestDiff_ValidColorValues_Accepted(t *testing.T) {
	// Each valid color value should pass color validation. The bogus
	// receipt id then fails markl parsing — which fires BEFORE
	// ValidateDiffDir in runDiff — so the dispatch exits with the
	// "trouble" code (2): NOT via the color-validation BadRequest path
	// (64) and NOT via the MismatchError path (1). (A nonexistent dir
	// alone would no longer discriminate here: since cutting-garden#187
	// ValidateDiffDir's refusal is itself a BadRequest/64.)
	for _, c := range []string{"auto", "always", "never"} {
		t.Run(c, func(t *testing.T) {
			u := makeUtility()
			code := u.Run([]string{
				"cutting-garden", "diff", "-color", c,
				"blake2b256-deadbeef", "src",
			})
			if code != 2 {
				t.Errorf("expected exit 2 (trouble) on bogus dir/receipt, got %d", code)
			}
		})
	}
}

func TestDiff_TwoArgs_BogusInputsReachDispatch(t *testing.T) {
	// Two positional args should NOT trip the count guard. The
	// dispatch proceeds into runDiff and errors out further down —
	// at the unparseable receipt id, which fires before
	// ValidateDiffDir. The test pins that arg parsing doesn't gate on
	// content and that the failure maps to exit 2 (trouble), not 1
	// (clean mismatch). (ValidateDiffDir's own nonexistent-dir refusal
	// is 64/EX_USAGE since cutting-garden#187 — pinned in diff.bats.)
	u := makeUtility()
	code := u.Run([]string{
		"cutting-garden", "diff",
		"blake2b256-deadbeef", "src",
	})
	if code != 2 {
		t.Errorf("expected exit 2 (trouble) on bogus inputs, got %d", code)
	}
}

func TestDiff_DescriptionShort(t *testing.T) {
	cmd := diff.New()
	desc := cmd.GetDescription()
	// "compare" is the verb the FDR uses; "diff" itself doesn't
	// appear in the short since it's the subcommand name.
	if !strings.Contains(desc.Short, "compare") {
		t.Errorf(
			"GetDescription().Short should mention 'compare'; got %q",
			desc.Short,
		)
	}
}
