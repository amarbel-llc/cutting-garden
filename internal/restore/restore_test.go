package restore_test

import (
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/command"
	// Blank-import the file plugin so its init() registers under the
	// "" and "file" restore schemes. Tests past the arg-parse stage
	// (TestRestore_TwoArgs_*) walk the resolve-plugin path; without
	// this the registry is empty and resolve fails before we exercise
	// what the test actually targets.
	_ "github.com/amarbel-llc/cutting-garden/internal/cutting_garden_plugin_file"
	"github.com/amarbel-llc/cutting-garden/internal/restore"
)

// makeUtility wires a fresh Utility with the restore cmd registered.
// Mirrors what cmd/cutting-garden/main.go does, minus capture (which
// these tests don't exercise) and the markl_registrations blank-import
// (only needed when an encrypted store config is actually loaded).
func makeUtility() command.Utility {
	u := command.MakeUtility("cutting-garden", nil)
	u.AddCmd("restore", restore.New())
	return u
}

func TestRestore_NoArgs_MissingReceiptId(t *testing.T) {
	u := makeUtility()
	code := u.Run([]string{"cutting-garden", "restore"})
	if code != 64 {
		t.Errorf("expected EX_USAGE (64) for no positional args, got %d", code)
	}
}

func TestRestore_OneArg_MissingDest(t *testing.T) {
	u := makeUtility()
	code := u.Run([]string{"cutting-garden", "restore", "blake2b256-deadbeef"})
	if code != 64 {
		t.Errorf("expected EX_USAGE (64) for missing <dest>, got %d", code)
	}
}

func TestRestore_ThreeArgs_TooManyArgs(t *testing.T) {
	u := makeUtility()
	code := u.Run([]string{
		"cutting-garden", "restore",
		"blake2b256-deadbeef", "out", "extra-arg",
	})
	if code != 64 {
		t.Errorf("expected EX_USAGE (64) for trailing arg, got %d", code)
	}
}

func TestRestore_TwoArgs_BogusReceiptIdRejected(t *testing.T) {
	// Step 3 dispatches: dest "out" → file plugin resolved →
	// ValidateDest accepts a non-existent path → receiptID.Set fails
	// because "blake2b256-deadbeef" is not a valid markl id (the
	// short string fails the blech32 checksum). The dispatch path is
	// exercised; only the inner receipt-id parse rejects. Exit code
	// is 2 (trouble), distinct from 64 (EX_USAGE) and 1 (mismatch).
	u := makeUtility()
	code := u.Run([]string{
		"cutting-garden", "restore",
		"blake2b256-deadbeef", "out",
	})
	if code != 2 {
		t.Errorf("expected exit 2 (trouble) for bogus receipt-id, got %d", code)
	}
}

func TestRestore_DescriptionShort(t *testing.T) {
	cmd := restore.New()
	desc := cmd.GetDescription()
	if !strings.Contains(desc.Short, "restore") {
		t.Errorf(
			"GetDescription().Short should mention 'restore'; got %q",
			desc.Short,
		)
	}
}
