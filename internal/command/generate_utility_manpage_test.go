package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// describedCmd is a Cmd with a short description, for the SUBCOMMANDS
// section. Mirrors what capture/restore/diff implement.
type describedCmd struct {
	short string
}

func (describedCmd) Run(Request) {}

func (c describedCmd) GetDescription() Description {
	return Description{Short: c.short}
}

// bareCmd implements only Run — no Description. Used to verify the
// "(no description)" fallback so the page stays total.
type bareCmd struct{}

func (bareCmd) Run(Request) {}

func TestGenerateUtilityManpage_BasicShape(t *testing.T) {
	dir := t.TempDir()
	u := MakeUtility("demo", nil)
	u.AddCmd("alpha", describedCmd{short: "do alpha things"})
	u.AddCmd("bravo", describedCmd{short: "do bravo things"})

	if err := u.GenerateUtilityManpage(dir); err != nil {
		t.Fatalf("GenerateUtilityManpage: %v", err)
	}

	wantPath := filepath.Join(dir, "share", "man", "man1", "demo.1")
	body, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("missing manpage at %s: %v", wantPath, err)
	}
	got := string(body)

	for _, want := range []string{
		".TH DEMO 1",
		".SH NAME\ndemo",
		".SH SYNOPSIS",
		".SH SUBCOMMANDS",
		".B alpha",
		"do alpha things",
		".B bravo",
		"do bravo things",
		".SH SEE ALSO",
		"demo-alpha(1), demo-bravo(1)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("manpage missing %q\n--- got ---\n%s", want, got)
		}
	}
}

func TestGenerateUtilityManpage_SubcommandsAlphabetical(t *testing.T) {
	// Add in non-alphabetical insertion order; render must sort.
	dir := t.TempDir()
	u := MakeUtility("demo", nil)
	u.AddCmd("zulu", describedCmd{short: "z"})
	u.AddCmd("alpha", describedCmd{short: "a"})
	u.AddCmd("mike", describedCmd{short: "m"})

	if err := u.GenerateUtilityManpage(dir); err != nil {
		t.Fatalf("GenerateUtilityManpage: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "share", "man", "man1", "demo.1"))
	got := string(body)

	alphaIdx := strings.Index(got, ".B alpha")
	mikeIdx := strings.Index(got, ".B mike")
	zuluIdx := strings.Index(got, ".B zulu")

	if alphaIdx < 0 || mikeIdx < 0 || zuluIdx < 0 {
		t.Fatalf("missing subcommand marker(s) in body:\n%s", got)
	}
	if !(alphaIdx < mikeIdx && mikeIdx < zuluIdx) {
		t.Errorf(
			"subcommands not alphabetical: alpha@%d mike@%d zulu@%d",
			alphaIdx, mikeIdx, zuluIdx,
		)
	}
}

func TestGenerateUtilityManpage_FiltersCompleteSubcommand(t *testing.T) {
	// `complete` is registered by RegisterComplete as framework
	// machinery. It MUST NOT appear in SUBCOMMANDS or SEE ALSO.
	dir := t.TempDir()
	u := MakeUtility("demo", nil)
	RegisterComplete(&u)
	u.AddCmd("alpha", describedCmd{short: "do alpha things"})

	if err := u.GenerateUtilityManpage(dir); err != nil {
		t.Fatalf("GenerateUtilityManpage: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "share", "man", "man1", "demo.1"))
	got := string(body)

	if strings.Contains(got, ".B complete") {
		t.Errorf("complete subcommand leaked into SUBCOMMANDS:\n%s", got)
	}
	if strings.Contains(got, "demo-complete(1)") {
		t.Errorf("complete subcommand leaked into SEE ALSO:\n%s", got)
	}
	if !strings.Contains(got, ".B alpha") {
		t.Errorf("non-complete subcommand was dropped:\n%s", got)
	}
}

func TestGenerateUtilityManpage_BareCmdGetsPlaceholder(t *testing.T) {
	// A subcommand without GetDescription surfaces the
	// "(no description)" placeholder so the page is total.
	dir := t.TempDir()
	u := MakeUtility("demo", nil)
	u.AddCmd("bare", bareCmd{})

	if err := u.GenerateUtilityManpage(dir); err != nil {
		t.Fatalf("GenerateUtilityManpage: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "share", "man", "man1", "demo.1"))
	got := string(body)

	if !strings.Contains(got, "(no description)") {
		t.Errorf("expected placeholder description; got:\n%s", got)
	}
}

func TestGenerateUtilityManpage_AliasesInNameAndSymlinked(t *testing.T) {
	// Aliases declared via AddAlias appear in the NAME line
	// comma-separated after the canonical name, and produce
	// `<alias>.1` symlinks pointing at the canonical `<utility>.1`
	// in the same directory. `man <alias>` follows the symlink.
	dir := t.TempDir()
	u := MakeUtility("demo", nil)
	u.AddAlias("dm")
	u.AddAlias("dem")
	u.AddCmd("alpha", describedCmd{short: "a"})

	if err := u.GenerateUtilityManpage(dir); err != nil {
		t.Fatalf("GenerateUtilityManpage: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(
		dir, "share", "man", "man1", "demo.1"))
	if err != nil {
		t.Fatalf("missing canonical manpage: %v", err)
	}
	if !strings.Contains(string(body), ".SH NAME\ndemo, dm, dem") {
		t.Errorf("expected NAME line to list aliases; got:\n%s", body)
	}

	for _, alias := range []string{"dm", "dem"} {
		linkPath := filepath.Join(
			dir, "share", "man", "man1", alias+".1")
		target, err := os.Readlink(linkPath)
		if err != nil {
			t.Errorf("expected %s.1 to be a symlink: %v", alias, err)
			continue
		}
		if target != "demo.1" {
			t.Errorf("%s.1 → %q, want demo.1", alias, target)
		}
	}
}

func TestGenerateUtilityManpage_AliasSymlinkIsIdempotent(t *testing.T) {
	// Re-running the generator replaces the existing symlink rather
	// than failing — dev iteration runs the gen binary repeatedly.
	dir := t.TempDir()
	u := MakeUtility("demo", nil)
	u.AddAlias("dm")
	u.AddCmd("alpha", describedCmd{short: "a"})

	for i := 0; i < 2; i++ {
		if err := u.GenerateUtilityManpage(dir); err != nil {
			t.Fatalf("run %d: GenerateUtilityManpage: %v", i, err)
		}
	}
	target, err := os.Readlink(filepath.Join(
		dir, "share", "man", "man1", "dm.1"))
	if err != nil {
		t.Fatalf("symlink missing after second run: %v", err)
	}
	if target != "demo.1" {
		t.Errorf("symlink target after re-run: got %q, want demo.1", target)
	}
}

func TestGenerateUtilityManpage_NoSubcommands_OmitsSections(t *testing.T) {
	// A utility with no user-facing subcommands (e.g. only the
	// hidden complete) should not emit SUBCOMMANDS or SEE ALSO
	// sections — those would be empty and meaningless.
	dir := t.TempDir()
	u := MakeUtility("demo", nil)
	RegisterComplete(&u)

	if err := u.GenerateUtilityManpage(dir); err != nil {
		t.Fatalf("GenerateUtilityManpage: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "share", "man", "man1", "demo.1"))
	got := string(body)

	if strings.Contains(got, ".SH SUBCOMMANDS") {
		t.Errorf("expected no SUBCOMMANDS section; got:\n%s", got)
	}
	if strings.Contains(got, ".SH SEE ALSO") {
		t.Errorf("expected no SEE ALSO section; got:\n%s", got)
	}
	// NAME and SYNOPSIS still present.
	if !strings.Contains(got, ".SH NAME") {
		t.Errorf("missing NAME section:\n%s", got)
	}
	if !strings.Contains(got, ".SH SYNOPSIS") {
		t.Errorf("missing SYNOPSIS section:\n%s", got)
	}
}
