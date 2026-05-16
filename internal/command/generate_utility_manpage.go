package command

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// completeSubcommandName is the hidden completion subcommand
// registered by RegisterComplete. Excluded from the toplevel
// utility manpage's SUBCOMMANDS section — it's framework machinery,
// not user-facing.
const completeSubcommandName = "complete"

// GenerateUtilityManpage writes the toplevel <utility>.1 manpage to
// <outDir>/share/man/man1/<utility>.1. The page surfaces the utility
// name, its global synopsis, the user-facing subcommand list (sorted
// alphabetically, with `complete` filtered out), and a SEE ALSO list
// of the per-subcommand manpages.
//
// Per-subcommand pages are written separately by GenerateManpages;
// the generator binary calls both in sequence.
func (utility Utility) GenerateUtilityManpage(outDir string) error {
	manDir := filepath.Join(outDir, "share", "man", "man1")
	if err := os.MkdirAll(manDir, 0o755); err != nil {
		return err
	}

	body := utility.renderUtilityManpage()
	path := filepath.Join(manDir, utility.GetName()+".1")
	return os.WriteFile(path, []byte(body), 0o644)
}

func (utility Utility) renderUtilityManpage() string {
	var b strings.Builder

	name := utility.GetName()
	fmt.Fprintf(&b, ".TH %s 1\n", strings.ToUpper(name))
	fmt.Fprintf(&b, ".SH NAME\n%s\n", name)
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

// userFacingSubcommands returns subcommands sorted alphabetically
// with the hidden `complete` subcommand filtered out. Defensive
// against future hidden subcommands by name-match; if more accrue,
// promote to an opt-in "hidden" interface.
func (utility Utility) userFacingSubcommands() []subcommandSummary {
	var out []subcommandSummary
	for name, cmd := range utility.AllCmds() {
		if name == completeSubcommandName {
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
