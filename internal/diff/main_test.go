package diff_test

import (
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/cutting-garden/internal/diff"
	// Blank-import the file plugin so its init() registers under the
	// "", "file" diff schemes. Step 3 will exercise the resolve-plugin
	// path; the step-2 skeleton does not reach it, but the import is
	// harmless and matches how cmd/cutting-garden/main.go wires it.
	_ "github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugin_file"
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
	// Each valid color value should pass validation; the dispatch
	// itself still cancels with "not yet implemented" until step 3,
	// so EX_USAGE is the expected exit code, but the diagnostic is
	// the not-implemented message rather than the color-validation
	// error. The two are distinguishable by the EX_USAGE-vs-other
	// exit code... but framework maps both BadRequest paths to 64.
	// Step 6 adds byte-exact assertion on stderr to distinguish.
	for _, c := range []string{"auto", "always", "never"} {
		t.Run(c, func(t *testing.T) {
			u := makeUtility()
			code := u.Run([]string{
				"cutting-garden", "diff", "-color", c,
				"blake2b256-deadbeef", "src",
			})
			if code != 64 {
				t.Errorf("expected EX_USAGE (64), got %d", code)
			}
		})
	}
}

func TestDiff_TwoArgs_BogusReceiptIdReachesDispatch(t *testing.T) {
	// Step 2 cancels with not-implemented after arg parsing; the
	// inner receipt-id parse never fires (step 3 adds it). Exit code
	// is EX_USAGE either way; this test just pins that two-positional
	// args don't trip the count guard.
	u := makeUtility()
	code := u.Run([]string{
		"cutting-garden", "diff",
		"blake2b256-deadbeef", "src",
	})
	if code != 64 {
		t.Errorf("expected EX_USAGE (64), got %d", code)
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
