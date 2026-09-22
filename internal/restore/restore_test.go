package restore_test

import (
	"net/url"
	"strings"
	"testing"

	"code.linenisgreat.com/cutting-garden/internal/command"
	"code.linenisgreat.com/cutting-garden/internal/cutting_garden_plugins"
	"code.linenisgreat.com/cutting-garden/internal/restore"
)

// fakeSchemelessRestore stands in for the file plugin's "", "file" restore
// registration. These tests exercise arg parsing and exit-code mapping, not
// any backend, so a fake keeps the framework test lane off plugins/ sources
// (docs/plans/2026-09-21-invalidation-cone-moves.md D6/D7, pinned by
// internal/sdklayering); the real file plugin's restore behavior is covered
// end-to-end by zz-tests_bats/restore.bats.
//
// No test below actually reaches plugin resolution: runRestore parses the
// receipt id first (restore.go:109) and returns before ResolveRestorePlugin
// (restore.go:134), and no test supplies a valid markl id. The registration
// is kept anyway because it costs nothing and keeps this file's picture of
// the shipped binary honest — a reader (or a later test that does reach
// dispatch) sees the "" + "file" restore claim the real binary has. Deleting
// it would be equally correct today.
type fakeSchemelessRestore struct{}

func (fakeSchemelessRestore) Schemes() []string { return []string{"", "file"} }

func (fakeSchemelessRestore) TypeTag() string {
	return "cutting_garden-capture_receipt-fake-v1"
}

func (fakeSchemelessRestore) ValidateDest(*url.URL, string) error { return nil }

func (fakeSchemelessRestore) Restore(
	cutting_garden_plugins.RestoreRequest,
) error {
	return nil
}

func init() {
	cutting_garden_plugins.MustRegisterRestore(fakeSchemelessRestore{})
}

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
	// Two positional args clear the arg-count guard and dispatch into
	// runRestore, which rejects at receiptID.Set — "blake2b256-deadbeef"
	// is not a valid markl id (the short string fails the blech32
	// checksum) — before it reaches ResolveRestorePlugin. Exit code is 2
	// (trouble), distinct from 64 (EX_USAGE) and 1 (mismatch).
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
