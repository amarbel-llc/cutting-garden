package restore_test

import (
	"strings"
	"testing"

	"github.com/amarbel-llc/cutting-garden/internal/command"
	"github.com/amarbel-llc/cutting-garden/internal/restore"
)

// makeUtility wires a fresh Utility with the restore cmd registered.
// Mirrors what cmd/cutting-garden/main.go does, minus capture and the
// blank-imports (which Phase 3 step 2 does not exercise).
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

func TestRestore_TwoArgs_DispatchesButNotImplemented(t *testing.T) {
	// Phase 3 step 2 only exercises arg parsing; the dispatch path
	// cancels with a not-yet-implemented BadRequest. EX_USAGE is the
	// expected exit code until step 3 wires the receipt fetch.
	u := makeUtility()
	code := u.Run([]string{
		"cutting-garden", "restore",
		"blake2b256-deadbeef", "out",
	})
	if code != 64 {
		t.Errorf(
			"expected EX_USAGE (64) for unimplemented dispatch, got %d",
			code,
		)
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
