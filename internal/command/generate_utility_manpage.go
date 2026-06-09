package command

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// GenerateUtilityManpage writes the toplevel <utility>.1 manpage to
// <outDir>/share/man/man1/<utility>.1. The page surfaces the utility
// name plus any registered aliases, its global synopsis, the
// user-facing subcommand list (sorted alphabetically, with CommandHidden
// commands filtered out), and a SEE ALSO list of the per-subcommand
// manpages.
//
// Per-subcommand pages are written separately by GenerateManpages;
// the generator binary calls both in sequence.
//
// Each registered alias gets a `<alias>.1` symlink pointing at
// `<utility>.1` in the same directory, so `man <alias>` follows the
// symlink and renders the canonical page. Per-subcommand pages
// (`<utility>-<sub>.1`) are NOT mirrored per alias; the SEE ALSO
// list points at the canonical names.
func (utility Utility) GenerateUtilityManpage(outDir string) error {
	manDir := filepath.Join(outDir, "share", "man", "man1")
	if err := os.MkdirAll(manDir, 0o755); err != nil {
		return err
	}

	body := utility.renderUtilityManpage()
	canonical := utility.GetName() + ".1"
	canonicalPath := filepath.Join(manDir, canonical)
	if err := os.WriteFile(canonicalPath, []byte(body), 0o644); err != nil {
		return err
	}

	for _, alias := range utility.GetAliases() {
		linkPath := filepath.Join(manDir, alias+".1")
		// Replace any existing entry so reruns are idempotent — the
		// gen binary may run multiple times during dev.
		_ = os.Remove(linkPath)
		if err := os.Symlink(canonical, linkPath); err != nil {
			return err
		}
	}
	return nil
}

func (utility Utility) renderUtilityManpage() string {
	var b strings.Builder

	name := utility.GetName()
	fmt.Fprintf(&b, ".TH %s 1\n", strings.ToUpper(name))

	// NAME lists every registered name (canonical first, then
	// aliases) comma-separated, mirroring the convention of pages
	// like `ls(1)` / `cp(1)` that document multiple invocations.
	allNames := append([]string{name}, utility.GetAliases()...)
	fmt.Fprintf(&b, ".SH NAME\n%s\n", strings.Join(allNames, ", "))

	fmt.Fprintf(&b, ".SH SYNOPSIS\n.B %s\n[\\fIflags\\fR]\n"+
		"\\fIsubcommand\\fR\n[\\fIargs\\fR]\n", name)

	subs := utility.userFacingSubcommands()
	if len(subs) > 0 {
		fmt.Fprintln(&b, ".SH SUBCOMMANDS")
		for _, sub := range subs {
			fmt.Fprintf(&b, ".TP\n.B %s\n%s\n", sub.name, sub.short)
		}
		fmt.Fprintln(&b, ".SH SEE ALSO")
		fmt.Fprintln(&b, formatSeeAlso(name, subs))
	}

	return b.String()
}

// subcommandSummary is the per-subcommand record renderUtilityManpage
// emits. The short description is "(no description)" when a
// subcommand doesn't implement CommandWithDescription so the page
// remains total.
type subcommandSummary struct {
	name  string
	short string
}

// userFacingSubcommands returns subcommands sorted alphabetically with
// commands that implement CommandHidden (framework plumbing like
// `complete` and `__write-blob`) filtered out.
func (utility Utility) userFacingSubcommands() []subcommandSummary {
	var out []subcommandSummary
	for name, cmd := range utility.AllCmds() {
		if isHidden(cmd) {
			continue
		}
		short := "(no description)"
		if d, ok := cmd.(CommandWithDescription); ok {
			if s := d.GetDescription().Short; s != "" {
				short = s
			}
		}
		out = append(out, subcommandSummary{name: name, short: short})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// formatSeeAlso renders the SEE ALSO list as a comma-separated string
// of `<utility>-<sub>(1)` references.
func formatSeeAlso(name string, subs []subcommandSummary) string {
	refs := make([]string, len(subs))
	for i, s := range subs {
		refs[i] = fmt.Sprintf("%s-%s(1)", name, s.name)
	}
	return strings.Join(refs, ", ")
}
