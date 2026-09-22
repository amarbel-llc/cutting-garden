package diff_test

import (
	"net/url"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/capture_receipt"
	"code.linenisgreat.com/cutting-garden/internal/command"
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/diff"
)

// fakeSchemelessDiff stands in for the file plugin's "", "file" diff
// registration. These tests exercise arg parsing and exit-code mapping, not
// any backend, so a fake keeps the framework test lane off plugins/ sources
// (docs/plans/2026-09-21-invalidation-cone-moves.md D6/D7, pinned by
// internal/sdklayering); the real file plugin's diff behavior is covered
// end-to-end by zz-tests_bats/diff.bats.
//
// No test below actually reaches plugin resolution: runDiff parses the
// receipt id first (main.go:158) and returns before ResolveDiffPlugin
// (main.go:182), and no test supplies a valid markl id. The registration is
// kept anyway because it costs nothing and keeps this file's picture of the
// shipped binary honest — a reader (or a later test that does reach
// dispatch) sees the "" + "file" diff claim the real binary has. Deleting it
// would be equally correct today.
type fakeSchemelessDiff struct{}

func (fakeSchemelessDiff) Schemes() []string { return []string{"", "file"} }

func (fakeSchemelessDiff) TypeTag() string {
	return "cutting_garden-capture_receipt-fake-v1"
}

func (fakeSchemelessDiff) ValidateDiffDir(*url.URL, string) error { return nil }

func (fakeSchemelessDiff) ScanForDiff(
	cutting_garden_plugins.DiffScanRequest,
) ([]capture_receipt.EntryV1, error) {
	return nil, nil
}

func init() {
	cutting_garden_plugins.MustRegisterDiff(fakeSchemelessDiff{})
}

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
